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

	sessions := tc.svc.GetSessions()
	aggState := aggregateTrayState(sessions)
	icon := tc.icons[aggState]
	if icon == nil {
		icon = tc.icons[core.StateIdle]
	}
	_ = tray.SetIcon(icon, false)
	tray.SetToolTip(formatTrayTooltip(sessions))
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

func aggregateTrayState(sessions []core.StatusSnapshot) core.State {
	hasBusy := false
	hasErr := false
	for _, s := range sessions {
		if s.State == core.StateConnected {
			return core.StateConnected
		}
		if s.State == core.StateConnecting || s.State == core.StateAuthenticating || s.State == core.StateReconnecting || s.State == core.StateDisconnecting {
			hasBusy = true
		}
		if s.State == core.StateError {
			hasErr = true
		}
	}
	if hasBusy {
		return core.StateConnecting
	}
	if hasErr {
		return core.StateError
	}
	return core.StateIdle
}

func isSessionActiveState(st core.State) bool {
	return st == core.StateConnected ||
		st == core.StateConnecting ||
		st == core.StateAuthenticating ||
		st == core.StateReconnecting ||
		st == core.StateDisconnecting
}

func (tc *TrayController) buildMenu() *mygo.Menu {
	sessions := tc.svc.GetSessions()
	profilesRes := tc.svc.ListProfiles()
	sysProxy := tc.svc.GetSystemProxyState()

	sessionByID := make(map[string]core.StatusSnapshot, len(sessions))
	var connectedSessions []core.StatusSnapshot
	activeCount := 0
	for _, s := range sessions {
		sessionByID[s.ProfileID] = s
		if isSessionActiveState(s.State) {
			activeCount++
		}
		if s.State == core.StateConnected {
			connectedSessions = append(connectedSessions, s)
		}
	}

	items := []*mygo.MenuItem{
		{
			Label:    formatTrayStatusLine(sessions),
			Disabled: true,
		},
		mygo.Separator(),
	}

	// Individual profile toggle items so multiple connections can be started/stopped independently from the tray.
	if len(profilesRes.Profiles) == 0 {
		items = append(items, &mygo.MenuItem{
			Label:    "No Profiles Configured",
			Disabled: true,
		})
	} else {
		for _, p := range profilesRes.Profiles {
			profID := p.ID
			sess, hasSess := sessionByID[profID]
			isActive := hasSess && isSessionActiveState(sess.State)

			label := fmt.Sprintf("%s (%s)", p.Name, p.Protocol)
			if hasSess && sess.State == core.StateConnected && sess.VirtualIP != "" {
				label = fmt.Sprintf("%s (%s)", p.Name, sess.VirtualIP)
			} else if hasSess && isActive && sess.State != core.StateConnected {
				label = fmt.Sprintf("%s — %s", p.Name, sess.State)
			}

			items = append(items, &mygo.MenuItem{
				Label:   label,
				Type:    mygo.MenuItemCheckbox,
				Checked: isActive,
				Click: func(*mygo.MenuItem, *mygo.Window) {
					go func() {
						if isActive {
							_ = tc.svc.Disconnect(context.Background(), profID)
						} else {
							_ = tc.svc.Connect(context.Background(), profID)
						}
					}()
				},
			})
		}
	}

	if activeCount > 0 {
		disconnectLabel := "Disconnect"
		if activeCount > 1 {
			disconnectLabel = fmt.Sprintf("Disconnect All (%d)", activeCount)
		}
		items = append(items,
			mygo.Separator(),
			&mygo.MenuItem{
				Label: disconnectLabel,
				Click: func(*mygo.MenuItem, *mygo.Window) {
					go func() {
						_ = tc.svc.DisconnectAll(context.Background())
					}()
				},
			},
		)
	}

	sysProxyLabel := "System Proxy"
	if sysProxy.Enabled && sysProxy.ProfileName != "" {
		sysProxyLabel = fmt.Sprintf("System Proxy (%s)", sysProxy.ProfileName)
	}
	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{
			Label:    sysProxyLabel,
			Type:     mygo.MenuItemCheckbox,
			Checked:  sysProxy.Enabled,
			Disabled: !sysProxy.Supported || len(connectedSessions) == 0,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				target := item.Checked
				targetProfID := ""
				if target && len(connectedSessions) > 0 {
					targetProfID = connectedSessions[0].ProfileID
				}
				go func() {
					_, _ = tc.svc.SetSystemProxy(targetProfID, target)
				}()
			},
		},
	)

	if len(connectedSessions) == 1 {
		s := connectedSessions[0]
		if s.HTTPAddr != "" || s.SocksAddr != "" {
			items = append(items, mygo.Separator())
		}
		if s.HTTPAddr != "" {
			addr := "http://" + s.HTTPAddr
			items = append(items, &mygo.MenuItem{
				Label: "Copy HTTP Proxy (" + s.HTTPAddr + ")",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					_ = tc.svc.CopyText(addr)
				},
			})
		}
		if s.SocksAddr != "" {
			addr := "socks5://" + s.SocksAddr
			items = append(items, &mygo.MenuItem{
				Label: "Copy SOCKS5 Proxy (" + s.SocksAddr + ")",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					_ = tc.svc.CopyText(addr)
				},
			})
		}
	} else if len(connectedSessions) > 1 {
		proxySub := make([]*mygo.MenuItem, 0, len(connectedSessions)*2)
		for _, s := range connectedSessions {
			if s.HTTPAddr != "" {
				addr := "http://" + s.HTTPAddr
				proxySub = append(proxySub, &mygo.MenuItem{
					Label: fmt.Sprintf("%s · HTTP (%s)", s.ProfileName, s.HTTPAddr),
					Click: func(*mygo.MenuItem, *mygo.Window) {
						_ = tc.svc.CopyText(addr)
					},
				})
			}
			if s.SocksAddr != "" {
				addr := "socks5://" + s.SocksAddr
				proxySub = append(proxySub, &mygo.MenuItem{
					Label: fmt.Sprintf("%s · SOCKS5 (%s)", s.ProfileName, s.SocksAddr),
					Click: func(*mygo.MenuItem, *mygo.Window) {
						_ = tc.svc.CopyText(addr)
					},
				})
			}
		}
		if len(proxySub) > 0 {
			items = append(items,
				mygo.Separator(),
				&mygo.MenuItem{
					Label:   "Copy Local Proxy",
					Submenu: proxySub,
				},
			)
		}
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

func formatTrayTooltip(sessions []core.StatusSnapshot) string {
	var connected []core.StatusSnapshot
	for _, s := range sessions {
		if s.State == core.StateConnected {
			connected = append(connected, s)
		}
	}
	if len(connected) == 1 {
		return fmt.Sprintf("InfiConnect — Connected (%s)", connected[0].ProfileName)
	}
	if len(connected) > 1 {
		return fmt.Sprintf("InfiConnect — %d Connections Active", len(connected))
	}
	switch aggregateTrayState(sessions) {
	case core.StateConnecting:
		return "InfiConnect — Connecting..."
	case core.StateError:
		return "InfiConnect — Connection Error"
	default:
		return "InfiConnect — Disconnected"
	}
}

func formatTrayStatusLine(sessions []core.StatusSnapshot) string {
	var connected []core.StatusSnapshot
	for _, s := range sessions {
		if s.State == core.StateConnected {
			connected = append(connected, s)
		}
	}
	if len(connected) == 1 {
		s := connected[0]
		if s.VirtualIP != "" {
			return fmt.Sprintf("Connected • %s (%s)", s.ProfileName, s.VirtualIP)
		}
		return fmt.Sprintf("Connected • %s", s.ProfileName)
	}
	if len(connected) > 1 {
		return fmt.Sprintf("Connected • %d active tunnels", len(connected))
	}
	for _, s := range sessions {
		switch s.State {
		case core.StateConnecting:
			return fmt.Sprintf("Connecting • %s…", s.ProfileName)
		case core.StateAuthenticating:
			return fmt.Sprintf("Auth Required • %s…", s.ProfileName)
		case core.StateReconnecting:
			return fmt.Sprintf("Reconnecting • %s (#%d)…", s.ProfileName, s.ReconnectAttempt)
		case core.StateDisconnecting:
			return fmt.Sprintf("Disconnecting • %s…", s.ProfileName)
		case core.StateError:
			return fmt.Sprintf("Error • %s", s.ProfileName)
		}
	}
	return "Disconnected"
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
