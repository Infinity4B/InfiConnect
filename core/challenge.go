package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mythologyli/zju-connect/client/authchallenge"
)

// DefaultChallengeTimeout is how long the bridge waits for UI input before timing out.
const DefaultChallengeTimeout = 5 * time.Minute

type challengeReply struct {
	answer ChallengeAnswer
	err    error
}

type pendingChallenge struct {
	challenge Challenge
	ch        chan challengeReply
}

// ChallengeBridge converts zju-connect's synchronous authchallenge.Handler
// callbacks into asynchronous Challenge events answered by the UI.
type ChallengeBridge struct {
	mu       sync.Mutex
	pending  map[string]*pendingChallenge
	timeout  time.Duration
	emitFn   func(Challenge)
	resolveFn func(challengeID string)
	ctxFn    func() context.Context
}

// NewChallengeBridge creates a ChallengeBridge.
// timeout <= 0 uses DefaultChallengeTimeout.
func NewChallengeBridge(
	timeout time.Duration,
	ctxFn func() context.Context,
	emitFn func(Challenge),
	resolveFn func(challengeID string),
) *ChallengeBridge {
	if timeout <= 0 {
		timeout = DefaultChallengeTimeout
	}
	return &ChallengeBridge{
		pending:   make(map[string]*pendingChallenge),
		timeout:   timeout,
		emitFn:    emitFn,
		resolveFn: resolveFn,
		ctxFn:     ctxFn,
	}
}

// Active returns a copy of the currently pending challenge, if any.
func (b *ChallengeBridge) Active() *Challenge {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.pending {
		c := p.challenge
		return &c
	}
	return nil
}

// Submit delivers a user's answer for a pending challenge ID.
func (b *ChallengeBridge) Submit(answer ChallengeAnswer) error {
	b.mu.Lock()
	p, ok := b.pending[answer.ID]
	if ok {
		delete(b.pending, answer.ID)
	}
	b.mu.Unlock()
	if !ok {
		return ErrChallengeNotFound
	}
	select {
	case p.ch <- challengeReply{answer: answer}:
	default:
	}
	if b.resolveFn != nil {
		b.resolveFn(answer.ID)
	}
	return nil
}

// Cancel cancels a specific pending challenge ID.
func (b *ChallengeBridge) Cancel(challengeID string) error {
	b.mu.Lock()
	p, ok := b.pending[challengeID]
	if ok {
		delete(b.pending, challengeID)
	}
	b.mu.Unlock()
	if !ok {
		return ErrChallengeNotFound
	}
	select {
	case p.ch <- challengeReply{err: ErrChallengeCanceled}:
	default:
	}
	if b.resolveFn != nil {
		b.resolveFn(challengeID)
	}
	return nil
}

// CancelAll cancels every pending challenge (called on Disconnect/Close).
func (b *ChallengeBridge) CancelAll() {
	b.mu.Lock()
	items := make([]*pendingChallenge, 0, len(b.pending))
	for id, p := range b.pending {
		items = append(items, p)
		delete(b.pending, id)
	}
	b.mu.Unlock()

	for _, p := range items {
		select {
		case p.ch <- challengeReply{err: ErrChallengeCanceled}:
		default:
		}
		if b.resolveFn != nil {
			b.resolveFn(p.challenge.ID)
		}
	}
}

func (b *ChallengeBridge) wait(ch Challenge) (ChallengeAnswer, error) {
	now := time.Now()
	ch.ID = newChallengeID()
	ch.CreatedAt = now
	ch.ExpiresAt = now.Add(b.timeout)

	waiter := &pendingChallenge{
		challenge: ch,
		ch:        make(chan challengeReply, 1),
	}

	b.mu.Lock()
	b.pending[ch.ID] = waiter
	b.mu.Unlock()

	if b.emitFn != nil {
		b.emitFn(ch)
	}

	var ctxDone <-chan struct{}
	if b.ctxFn != nil {
		if ctx := b.ctxFn(); ctx != nil {
			ctxDone = ctx.Done()
		}
	}

	timer := time.NewTimer(b.timeout)
	defer timer.Stop()

	cleanup := func() {
		b.mu.Lock()
		_, stillThere := b.pending[ch.ID]
		if stillThere {
			delete(b.pending, ch.ID)
		}
		b.mu.Unlock()
		if stillThere && b.resolveFn != nil {
			b.resolveFn(ch.ID)
		}
	}

	select {
	case rep := <-waiter.ch:
		cleanup()
		return rep.answer, rep.err
	case <-ctxDone:
		cleanup()
		return ChallengeAnswer{}, ErrChallengeCanceled
	case <-timer.C:
		cleanup()
		return ChallengeAnswer{}, ErrChallengeTimeout
	}
}

// HandleCodeChallenge implements authchallenge.Handler.
func (b *ChallengeBridge) HandleCodeChallenge(challenge authchallenge.CodeChallenge) (authchallenge.CodeResponse, error) {
	ans, err := b.wait(Challenge{
		Type:                 ChallengeTypeCode,
		CodeKind:             CodeKind(challenge.Kind),
		Message:              challenge.Message,
		CanSkipSecondaryAuth: challenge.CanSkipSecondaryAuth,
	})
	if err != nil {
		return authchallenge.CodeResponse{}, err
	}
	code := strings.TrimSpace(ans.Code)
	if code == "" {
		return authchallenge.CodeResponse{}, errors.New("verification code is empty")
	}
	return authchallenge.CodeResponse{
		Code:              code,
		SkipSecondaryAuth: ans.SkipSecondaryAuth && challenge.CanSkipSecondaryAuth,
	}, nil
}

// HandleTextCaptcha implements authchallenge.Handler.
func (b *ChallengeBridge) HandleTextCaptcha(challenge authchallenge.TextCaptchaChallenge) (authchallenge.TextCaptchaResponse, error) {
	mime, w, h := inspectImage(challenge.Image)
	ans, err := b.wait(Challenge{
		Type:        ChallengeTypeTextCaptcha,
		Message:     challenge.Message,
		ImageBase64: base64.StdEncoding.EncodeToString(challenge.Image),
		ImageMime:   mime,
		ImageWidth:  w,
		ImageHeight: h,
	})
	if err != nil {
		return authchallenge.TextCaptchaResponse{}, err
	}
	resp := authchallenge.TextCaptchaResponse{Code: strings.TrimSpace(ans.Code)}
	if err := challenge.Validate(resp); err != nil {
		return authchallenge.TextCaptchaResponse{}, err
	}
	return resp, nil
}

// HandleClickCaptcha implements authchallenge.Handler.
func (b *ChallengeBridge) HandleClickCaptcha(challenge authchallenge.ClickCaptchaChallenge) (authchallenge.ClickCaptchaResponse, error) {
	mime, w, h := inspectImage(challenge.Image)
	ans, err := b.wait(Challenge{
		Type:        ChallengeTypeClickCaptcha,
		Message:     challenge.Message,
		ImageBase64: base64.StdEncoding.EncodeToString(challenge.Image),
		ImageMime:   mime,
		ImageWidth:  w,
		ImageHeight: h,
	})
	if err != nil {
		return authchallenge.ClickCaptchaResponse{}, err
	}
	if len(ans.Points) == 0 {
		return authchallenge.ClickCaptchaResponse{}, errors.New("click captcha coordinates are empty")
	}
	pts := make([]authchallenge.Point, len(ans.Points))
	for i, pt := range ans.Points {
		pts[i] = authchallenge.Point{X: pt.X, Y: pt.Y}
	}
	respW, respH := ans.Width, ans.Height
	if respW <= 0 || respH <= 0 {
		respW, respH = w, h
	}
	return authchallenge.ClickCaptchaResponse{
		Points: pts,
		Width:  respW,
		Height: respH,
	}, nil
}

// HandleExternalLogin implements authchallenge.Handler.
func (b *ChallengeBridge) HandleExternalLogin(challenge authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
	ans, err := b.wait(Challenge{
		Type:         ChallengeTypeExternalLogin,
		ExternalKind: ExternalLoginKind(challenge.Kind),
		LoginURL:     challenge.LoginURL,
		Message:      challenge.Message,
	})
	if err != nil {
		return authchallenge.ExternalLoginResponse{}, err
	}
	cb := strings.TrimSpace(ans.CallbackURL)
	if cb == "" {
		return authchallenge.ExternalLoginResponse{}, errors.New("external login callback URL is empty")
	}
	return authchallenge.ExternalLoginResponse{CallbackURL: cb}, nil
}

func inspectImage(data []byte) (mime string, width, height int) {
	if len(data) == 0 {
		return "image/png", 0, 0
	}
	mime = http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		mime = "image/png"
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		width, height = cfg.Width, cfg.Height
	}
	return mime, width, height
}

func newChallengeID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("ch-%d", time.Now().UnixNano())
	}
	return "ch-" + hex.EncodeToString(buf[:])
}
