package app

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"

	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/core"
)

// TrayController manages the OS menu bar / notification area tray icon and menu.
type TrayController struct {
	mu      sync.Mutex
	tray    *mygo.Tray
	svc     *AppService
	showWin func()
	quitApp func()

	icons map[core.State][]byte
}

func NewTrayController(svc *AppService, showWin func(), quitApp func()) (*TrayController, error) {
	icons := map[core.State][]byte{
		core.StateIdle:           renderTrayPNG(color.NRGBA{R: 120, G: 132, B: 150, A: 255}, false),
		core.StateConnecting:     renderTrayPNG(color.NRGBA{R: 245, G: 158, B: 11, A: 255}, false),
		core.StateAuthenticating: renderTrayPNG(color.NRGBA{R: 245, G: 158, B: 11, A: 255}, false),
		core.StateReconnecting:   renderTrayPNG(color.NRGBA{R: 245, G: 158, B: 11, A: 255}, false),
		core.StateDisconnecting:  renderTrayPNG(color.NRGBA{R: 120, G: 132, B: 150, A: 255}, false),
		core.StateConnected:      renderTrayPNG(color.NRGBA{R: 16, G: 185, B: 129, A: 255}, true),
		core.StateError:          renderTrayPNG(color.NRGBA{R: 239, G: 68, B: 68, A: 255}, true),
	}

	tc := &TrayController{
		svc:     svc,
		showWin: showWin,
		quitApp: quitApp,
		icons:   icons,
	}

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "inficonnect-tray",
		Icon:    icons[core.StateIdle],
		ToolTip: "InfiConnect — Disconnected",
		Menu:    tc.buildMenu(),
	})
	if err != nil {
		return nil, err
	}
	tc.tray = tray
	return tc, nil
}

// Refresh updates the tray icon, tooltip, and menu to reflect current state.
func (tc *TrayController) Refresh() {
	tc.mu.Lock()
	tray := tc.tray
	tc.mu.Unlock()
	if tray == nil {
		return
	}

	status := tc.svc.GetStatus()
	icon := tc.icons[status.State]
	if icon == nil {
		icon = tc.icons[core.StateIdle]
	}
	_ = tray.SetIcon(icon, false)
	tray.SetToolTip(formatTrayTooltip(status))
	tray.SetMenu(tc.buildMenu())
}

func (tc *TrayController) Destroy() {
	tc.mu.Lock()
	tray := tc.tray
	tc.tray = nil
	tc.mu.Unlock()
	if tray != nil {
		tray.Destroy()
	}
}

func (tc *TrayController) buildMenu() *mygo.Menu {
	status := tc.svc.GetStatus()
	profilesRes := tc.svc.ListProfiles()
	sysProxy := tc.svc.GetSystemProxyState()

	isActive := status.State == core.StateConnected ||
		status.State == core.StateConnecting ||
		status.State == core.StateAuthenticating ||
		status.State == core.StateReconnecting

	toggleLabel := "Connect"
	if isActive {
		toggleLabel = "Disconnect"
	}

	profileItems := make([]*mygo.MenuItem, 0, len(profilesRes.Profiles))
	for _, p := range profilesRes.Profiles {
		profID := p.ID
		profileItems = append(profileItems, &mygo.MenuItem{
			Label:   fmt.Sprintf("%s (%s)", p.Name, p.Protocol),
			Type:    mygo.MenuItemRadio,
			Checked: profID == profilesRes.ActiveProfileID,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					_ = tc.svc.SetActiveProfile(profID)
					tc.Refresh()
				}()
			},
		})
	}
	if len(profileItems) == 0 {
		profileItems = append(profileItems, &mygo.MenuItem{
			Label:    "No Profiles Configured",
			Disabled: true,
		})
	}

	items := []*mygo.MenuItem{
		{
			Label:    formatTrayStatusLine(status),
			Disabled: true,
		},
		mygo.Separator(),
		{
			Label: toggleLabel,
			Click: func(*mygo.MenuItem, *mygo.Window) {
				go func() {
					if isActive {
						_ = tc.svc.Disconnect(context.Background())
					} else {
						_ = tc.svc.Connect(context.Background(), "")
					}
				}()
			},
		},
		{
			Label:    "System Proxy",
			Type:     mygo.MenuItemCheckbox,
			Checked:  sysProxy.Enabled,
			Disabled: !sysProxy.Supported || status.State != core.StateConnected,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				target := item.Checked
				go func() {
					_, _ = tc.svc.SetSystemProxy(target)
				}()
			},
		},
		{
			Label:   "Switch Profile",
			Submenu: profileItems,
		},
		mygo.Separator(),
	}

	if status.HTTPAddr != "" {
		addr := "http://" + status.HTTPAddr
		items = append(items, &mygo.MenuItem{
			Label: "Copy HTTP Proxy (" + status.HTTPAddr + ")",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				_ = tc.svc.CopyText(addr)
			},
		})
	}
	if status.SocksAddr != "" {
		addr := "socks5://" + status.SocksAddr
		items = append(items, &mygo.MenuItem{
			Label: "Copy SOCKS5 Proxy (" + status.SocksAddr + ")",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				_ = tc.svc.CopyText(addr)
			},
		})
	}

	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{
			Label: "Open InfiConnect",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if tc.showWin != nil {
					tc.showWin()
				}
			},
		},
		&mygo.MenuItem{
			Label: "Quit InfiConnect",
			Click: func(*mygo.MenuItem, *mygo.Window) {
				if tc.quitApp != nil {
					tc.quitApp()
				} else {
					mygo.App.Quit()
				}
			},
		},
	)

	return mygo.NewMenu(items)
}

func formatTrayTooltip(s core.StatusSnapshot) string {
	switch s.State {
	case core.StateConnected:
		return fmt.Sprintf("InfiConnect — Connected (%s)", s.ProfileName)
	case core.StateConnecting:
		return "InfiConnect — Connecting..."
	case core.StateAuthenticating:
		return "InfiConnect — Waiting for Authentication..."
	case core.StateReconnecting:
		return fmt.Sprintf("InfiConnect — Reconnecting (#%d)...", s.ReconnectAttempt)
	case core.StateError:
		return "InfiConnect — Connection Error"
	default:
		return "InfiConnect — Disconnected"
	}
}

func formatTrayStatusLine(s core.StatusSnapshot) string {
	switch s.State {
	case core.StateConnected:
		if s.VirtualIP != "" {
			return fmt.Sprintf("Connected • %s (%s)", s.ProfileName, s.VirtualIP)
		}
		return fmt.Sprintf("Connected • %s", s.ProfileName)
	case core.StateConnecting:
		return "Connecting…"
	case core.StateAuthenticating:
		return "Authentication Required…"
	case core.StateReconnecting:
		return fmt.Sprintf("Reconnecting (Attempt #%d)…", s.ReconnectAttempt)
	case core.StateDisconnecting:
		return "Disconnecting…"
	case core.StateError:
		return "Error"
	default:
		return "Disconnected"
	}
}

// renderTrayPNG draws an anti-aliased 32x32 status glyph PNG cleanly in pure Go.
func renderTrayPNG(accent color.NRGBA, filled bool) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	cx, cy := 15.5, 15.5

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Hypot(dx, dy)

			var alpha float64
			if filled {
				// Solid circle r=11 with crisp white inner dot r=4
				if dist < 11.5 {
					alpha = clamp01(11.5 - dist)
					c := accent
					if dist < 4.2 {
						blend := clamp01(4.2 - dist)
						c = color.NRGBA{
							R: uint8(float64(accent.R)*(1-blend) + 255*blend),
							G: uint8(float64(accent.G)*(1-blend) + 255*blend),
							B: uint8(float64(accent.B)*(1-blend) + 255*blend),
							A: 255,
						}
					}
					c.A = uint8(alpha * 255)
					img.SetNRGBA(x, y, c)
				}
			} else {
				// Ring outer r=11.5, inner r=7.0, plus center dot r=3.2
				ring := clamp01(11.5-dist) * clamp01(dist-7.0)
				dot := clamp01(3.4 - dist)
				alpha = math.Max(ring, dot)
				if alpha > 0 {
					c := accent
					c.A = uint8(alpha * 255)
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
