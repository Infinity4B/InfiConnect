package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/things-go/go-socks5"
)

// TrafficCounter tracks cumulative bytes transferred and active connections.
type TrafficCounter struct {
	sent        atomic.Uint64
	recv        atomic.Uint64
	activeConns atomic.Int64

	mu       sync.Mutex
	lastSent uint64
	lastRecv uint64
	lastTime time.Time
}

func NewTrafficCounter() *TrafficCounter {
	return &TrafficCounter{lastTime: time.Now()}
}

func (t *TrafficCounter) WrapConn(c net.Conn) net.Conn {
	if c == nil {
		return nil
	}
	t.activeConns.Add(1)
	return &countingConn{Conn: c, counter: t}
}

func (t *TrafficCounter) Snapshot() TrafficStats {
	s := t.sent.Load()
	r := t.recv.Load()
	ac := t.activeConns.Load()
	if ac < 0 {
		ac = 0
	}

	t.mu.Lock()
	now := time.Now()
	dt := now.Sub(t.lastTime).Seconds()
	var rateSent, rateRecv uint64
	if dt >= 0.1 {
		if s >= t.lastSent {
			rateSent = uint64(float64(s-t.lastSent) / dt)
		}
		if r >= t.lastRecv {
			rateRecv = uint64(float64(r-t.lastRecv) / dt)
		}
		t.lastSent = s
		t.lastRecv = r
		t.lastTime = now
	}
	t.mu.Unlock()

	return TrafficStats{
		BytesSent:   s,
		BytesRecv:   r,
		RateSent:    rateSent,
		RateRecv:    rateRecv,
		ActiveConns: ac,
	}
}

type countingConn struct {
	net.Conn
	counter   *TrafficCounter
	closeOnce sync.Once
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.counter.recv.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.counter.sent.Add(uint64(n))
	}
	return n, err
}

func (c *countingConn) Close() error {
	c.closeOnce.Do(func() {
		c.counter.activeConns.Add(-1)
	})
	return c.Conn.Close()
}

// DialTarget is the active dialer + resolver backend used by ProxyServer.
type DialTarget struct {
	Dial       func(ctx context.Context, network, addr string) (net.Conn, error)
	DialIPPort func(ctx context.Context, network, addr string) (net.Conn, error)
	Resolve    func(ctx context.Context, name string) (context.Context, net.IP, error)
	OnDialErr  func(err error)
}

// SwappableTarget lets ProxyServer keep listening across reconnects and atomically
// swap in the new VPN dialer once reconnected.
type SwappableTarget struct {
	cur atomic.Pointer[DialTarget]
}

func (s *SwappableTarget) Set(t *DialTarget) {
	s.cur.Store(t)
}

func (s *SwappableTarget) Get() *DialTarget {
	return s.cur.Load()
}

type swappableResolver struct {
	target *SwappableTarget
}

func (r swappableResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	t := r.target.Get()
	if t == nil || t.Resolve == nil {
		return ctx, nil, errors.New("VPN tunnel is reconnecting")
	}
	return t.Resolve(ctx, name)
}

// ProxyServer hosts cancellable local SOCKS5 and HTTP/CONNECT proxies with
// deterministic listener cleanup and traffic accounting.
type ProxyServer struct {
	target  *SwappableTarget
	counter *TrafficCounter
	logger  *stdlog.Logger

	socksLn net.Listener
	httpLn  net.Listener
	httpSrv *http.Server
	httpPxy *managedHTTPProxy

	socksAddr string
	httpAddr  string

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// ProxyConfig configures local proxy listeners.
type ProxyConfig struct {
	SocksBind string
	SocksUser string
	SocksPass string
	HTTPBind  string
}

func StartProxyServer(cfg ProxyConfig, target *SwappableTarget, counter *TrafficCounter, logWriter io.Writer) (*ProxyServer, error) {
	if counter == nil {
		counter = NewTrafficCounter()
	}
	if logWriter == nil {
		logWriter = io.Discard
	}
	ps := &ProxyServer{
		target:  target,
		counter: counter,
		logger:  stdlog.New(logWriter, "[PROXY] ", 0),
	}

	if cfg.SocksBind != "" {
		ln, err := net.Listen("tcp", cfg.SocksBind)
		if err != nil {
			return nil, fmt.Errorf("listen SOCKS5 on %s: %w", cfg.SocksBind, err)
		}
		ps.socksLn = ln
		ps.socksAddr = normalizeBoundAddr(ln.Addr().String(), cfg.SocksBind)

		var authMethods []socks5.Authenticator
		if cfg.SocksUser != "" && cfg.SocksPass != "" {
			authMethods = append(authMethods, socks5.UserPassAuthenticator{
				Credentials: socks5.StaticCredentials{cfg.SocksUser: cfg.SocksPass},
			})
		} else {
			authMethods = append(authMethods, socks5.NoAuthAuthenticator{})
		}

		s5 := socks5.NewServer(
			socks5.WithAuthMethods(authMethods),
			socks5.WithResolver(swappableResolver{target: target}),
			socks5.WithDial(func(ctx context.Context, network, addr string) (net.Conn, error) {
				t := target.Get()
				if t == nil || t.DialIPPort == nil {
					return nil, errors.New("VPN tunnel is reconnecting")
				}
				conn, err := t.DialIPPort(ctx, network, addr)
				if err != nil {
					if t.OnDialErr != nil {
						t.OnDialErr(err)
					}
					return nil, err
				}
				return counter.WrapConn(conn), nil
			}),
			socks5.WithLogger(socks5.NewLogger(stdlog.New(logWriter, "[SOCKS5] ", 0))),
		)

		ps.wg.Add(1)
		go func() {
			defer ps.wg.Done()
			if err := s5.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
				ps.logger.Printf("SOCKS5 server stopped: %v", err)
			}
		}()
	}

	if cfg.HTTPBind != "" {
		ln, err := net.Listen("tcp", cfg.HTTPBind)
		if err != nil {
			_ = ps.Close()
			return nil, fmt.Errorf("listen HTTP proxy on %s: %w", cfg.HTTPBind, err)
		}
		ps.httpLn = ln
		ps.httpAddr = normalizeBoundAddr(ln.Addr().String(), cfg.HTTPBind)

		pxy := newManagedHTTPProxy(target, counter)
		srv := &http.Server{
			Handler:           pxy,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
		}
		ps.httpPxy = pxy
		ps.httpSrv = srv

		ps.wg.Add(1)
		go func() {
			defer ps.wg.Done()
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				ps.logger.Printf("HTTP proxy server stopped: %v", err)
			}
		}()
	}

	return ps, nil
}

func (p *ProxyServer) SocksAddr() string { return p.socksAddr }
func (p *ProxyServer) HTTPAddr() string  { return p.httpAddr }

// Close terminates listeners and all active CONNECT tunnels cleanly,
// guaranteeing port release before returning.
func (p *ProxyServer) Close() error {
	var firstErr error
	p.closeOnce.Do(func() {
		if p.socksLn != nil {
			if err := p.socksLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				firstErr = err
			}
		}
		if p.httpPxy != nil {
			p.httpPxy.close()
		}
		if p.httpSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := p.httpSrv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				_ = p.httpSrv.Close()
			}
			cancel()
		} else if p.httpLn != nil {
			_ = p.httpLn.Close()
		}
		p.wg.Wait()
	})
	return firstErr
}

func normalizeBoundAddr(actual, requested string) string {
	host, port, err := net.SplitHostPort(actual)
	if err != nil {
		return actual
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		if reqHost, _, reqErr := net.SplitHostPort(requested); reqErr == nil && reqHost != "" {
			return net.JoinHostPort(reqHost, port)
		}
		return net.JoinHostPort("127.0.0.1", port)
	}
	return actual
}

type managedTunnel struct {
	client net.Conn
	target net.Conn
}

type managedHTTPProxy struct {
	target    *SwappableTarget
	counter   *TrafficCounter
	client    *http.Client
	tunnelsMu sync.Mutex
	tunnels   map[*managedTunnel]struct{}
}

func newManagedHTTPProxy(target *SwappableTarget, counter *TrafficCounter) *managedHTTPProxy {
	p := &managedHTTPProxy{
		target:  target,
		counter: counter,
		tunnels: make(map[*managedTunnel]struct{}),
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = p.dialContext
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.MaxConnsPerHost = 50
	tr.IdleConnTimeout = 90 * time.Second
	tr.ResponseHeaderTimeout = 30 * time.Second
	p.client = &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return p
}

func (p *managedHTTPProxy) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	t := p.target.Get()
	if t == nil || t.Dial == nil {
		return nil, errors.New("VPN tunnel is reconnecting")
	}
	conn, err := t.Dial(ctx, network, addr)
	if err != nil {
		if t.OnDialErr != nil {
			t.OnDialErr(err)
		}
		return nil, err
	}
	return p.counter.WrapConn(conn), nil
}

func (p *managedHTTPProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		p.handleConnect(w, req)
		return
	}
	outReq := req.Clone(req.Context())
	outReq.RequestURI = ""
	resp, err := p.client.Do(outReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	dstHdr := w.Header()
	for k, vv := range resp.Header {
		for _, v := range vv {
			dstHdr.Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *managedHTTPProxy) handleConnect(w http.ResponseWriter, req *http.Request) {
	targetConn, err := p.dialContext(req.Context(), "tcp", req.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = targetConn.Close()
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, buf, err := hijacker.Hijack()
	if err != nil {
		_ = targetConn.Close()
		return
	}

	tunnel := &managedTunnel{client: clientConn, target: targetConn}
	p.tunnelsMu.Lock()
	p.tunnels[tunnel] = struct{}{}
	p.tunnelsMu.Unlock()
	defer func() {
		p.tunnelsMu.Lock()
		delete(p.tunnels, tunnel)
		p.tunnelsMu.Unlock()
		_ = clientConn.Close()
		_ = targetConn.Close()
	}()

	if _, err := buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buf.Flush(); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(targetConn, buf)
		if cw, ok := targetConn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, targetConn)
		if cw, ok := clientConn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	_ = clientConn.Close()
	_ = targetConn.Close()
	<-done
}

func (p *managedHTTPProxy) close() {
	p.tunnelsMu.Lock()
	list := make([]*managedTunnel, 0, len(p.tunnels))
	for t := range p.tunnels {
		list = append(list, t)
	}
	p.tunnelsMu.Unlock()
	for _, t := range list {
		_ = t.client.Close()
		_ = t.target.Close()
	}
	p.client.CloseIdleConnections()
}
