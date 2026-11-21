package app

import (
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/core"
)

// ExternalLoginManager opens an isolated, untrusted mygo WebView child window
// for CAS / OAuth2 logins and intercepts navigation to the callback URL via
// Page.OnWillNavigate.
type ExternalLoginManager struct {
	mu  sync.Mutex
	win *mygo.Window
}

func NewExternalLoginManager() *ExternalLoginManager {
	return &ExternalLoginManager{}
}

// Open launches an untrusted login window for loginURL and invokes onCallback
// automatically as soon as the WebView navigates to the CAS/OAuth2 callback URL.
func (m *ExternalLoginManager) Open(
	parent *mygo.Window,
	challengeID string,
	kind core.ExternalLoginKind,
	loginURL string,
	onCallback func(callbackURL string),
) error {
	u, err := url.Parse(strings.TrimSpace(loginURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid external login URL: %q", loginURL)
	}

	m.Close()

	title := "External Sign-In"
	switch kind {
	case core.ExternalLoginCAS:
		title = "CAS Sign-In — InfiConnect"
	case core.ExternalLoginOAuth2:
		title = "OAuth2 Sign-In — InfiConnect"
	}

	win := mygo.NewWindow(mygo.WindowOptions{
		Title:           title,
		Width:           540,
		Height:          700,
		MinWidth:        420,
		MinHeight:       520,
		Parent:          parent,
		URL:             loginURL,
		BackgroundColor: "light-dark(#ffffff, #181a20)",
		Page: mygo.PageOptions{
			// Untrusted keeps external IdP pages completely isolated from bound Go APIs.
			Untrusted: true,
		},
	})

	var once sync.Once
	handleURL := func(raw string) bool {
		if !IsExternalLoginCallbackURL(kind, raw) {
			return false
		}
		once.Do(func() {
			go func() {
				if onCallback != nil {
					onCallback(raw)
				}
				win.Close()
			}()
		})
		return true
	}

	if page := win.Page(); page != nil {
		// Allow normal navigation inside the untrusted login window while
		// intercepting the final callback redirect before the browser loads it.
		page.OnWillNavigate(func(e *mygo.NavigateEvent) {
			if handleURL(e.URL) {
				e.PreventDefault()
			}
		})
		page.OnDidNavigate(func(navURL string) {
			handleURL(navURL)
		})
	}

	win.OnClosed(func() {
		m.mu.Lock()
		if m.win == win {
			m.win = nil
		}
		m.mu.Unlock()
	})

	m.mu.Lock()
	m.win = win
	m.mu.Unlock()
	return nil
}

// Close closes any currently open external login child window.
func (m *ExternalLoginManager) Close() {
	m.mu.Lock()
	win := m.win
	m.win = nil
	m.mu.Unlock()
	if win != nil && !win.IsDestroyed() {
		win.Close()
	}
}

// IsExternalLoginCallbackURL returns true when rawURL is a valid aTrust CAS or
// OAuth2 callback redirect containing the expected authorization parameter.
func IsExternalLoginCallbackURL(kind core.ExternalLoginKind, rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	q := u.Query()
	switch kind {
	case core.ExternalLoginCAS:
		return u.Path == "/passport/v1/auth/cas" && q.Get("ticket") != ""
	case core.ExternalLoginOAuth2:
		return u.Path == "/passport/v1/auth/httpsOauth2" && q.Get("code") != ""
	default:
		if u.Path == "/passport/v1/auth/cas" && q.Get("ticket") != "" {
			return true
		}
		if u.Path == "/passport/v1/auth/httpsOauth2" && q.Get("code") != "" {
			return true
		}
		return false
	}
}
