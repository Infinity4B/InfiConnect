package core

import (
	"context"
	"net"
	"time"

	"github.com/mythologyli/zju-connect/client/authchallenge"
)

type loopbackMockSession struct {
	broken chan error
}

func (m *loopbackMockSession) DialTarget() *DialTarget {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &DialTarget{
		Dial:       d.DialContext,
		DialIPPort: d.DialContext,
		Resolve: func(ctx context.Context, name string) (context.Context, net.IP, error) {
			if ip := net.ParseIP(name); ip != nil {
				return ctx, ip, nil
			}
			return ctx, net.ParseIP("127.0.0.1"), nil
		},
	}
}

func (m *loopbackMockSession) Resources() ResourceSnapshot {
	return ResourceSnapshot{
		VirtualIP: "10.0.0.2",
		IPResources: []IPResourceItem{
			{IPMin: "10.0.0.0", IPMax: "10.255.255.255", CIDR: "10.0.0.0/8", PortMin: 1, PortMax: 65535, Protocol: "all"},
		},
	}
}

func (m *loopbackMockSession) KeepAlive(context.Context) error { return nil }
func (m *loopbackMockSession) Broken() <-chan error            { return m.broken }
func (m *loopbackMockSession) Close() error                    { return nil }

// NewLoopbackMockSessionFactory returns a SessionFactory that dials locally
// without requiring a live remote VPN server (used by cmd/probe --mock).
func NewLoopbackMockSessionFactory() SessionFactory {
	return func(ctx context.Context, profile Profile, handler authchallenge.Handler, reuseSession bool, logFn func(LogLevel, string, string)) (ActiveSession, error) {
		return &loopbackMockSession{broken: make(chan error, 1)}, nil
	}
}
