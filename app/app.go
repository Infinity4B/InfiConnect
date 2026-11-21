package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/inficonnect/inficonnect/app/sysproxy"
	"github.com/inficonnect/inficonnect/core"
	"github.com/inficonnect/inficonnect/core/rpc"
)

// Run launches the InfiConnect desktop GUI application using mygo and the
// Scheme-B subprocess core engine.
func Run() error {
	application := mygo.App
	application.SetName("InfiConnect")

	svc := NewAppService(nil, nil, nil)
	mygo.Bind(svc)

	var (
		store    *Store
		sp       *sysproxy.Manager
		engine   core.Engine
		mainWin  *mygo.Window
		tray     *TrayController
		quitting bool
	)

	showMainWindow := func() {
		if mainWin == nil || mainWin.IsDestroyed() {
			return
		}
		mainWin.Show()
		mainWin.Focus()
	}

	cleanupSync := func() {
		if sp != nil {
			_ = sp.Disable()
		}
		if engine != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = engine.Disconnect(ctx)
			cancel()
			_ = engine.Close()
		}
		svc.close()
		if tray != nil {
			tray.Destroy()
		}
	}

	application.WhenReady(func() {
		var err error
		store, err = OpenStore("", nil)
		if err != nil {
			log.Printf("open profile store: %v", err)
			application.Quit()
			return
		}
		sp = sysproxy.NewManager(store.Dir())

		// Use Scheme B (self-spawned `--core` subprocess) so disconnect is 100% leak-free
		// and upstream crashes (log.Fatalf / os.Exit) can never bring down the GUI.
		engine = rpc.NewSubprocessEngine(rpc.SubprocessOptions{
			WorkerArgs: []string{"--core"},
		})
		svc.initRuntime(engine, store, sp)

		// Ensure SIGINT/SIGTERM also restores system proxy cleanly.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			quitting = true
			cleanupSync()
			application.Quit()
		}()

		// Crash-recovery check: restore orphaned system proxy from any abnormal prior exit.
		if recovered, rErr := sp.RecoverIfDirty(); rErr != nil {
			svc.appendLog(core.LogEntry{
				Timestamp: time.Now(),
				Level:     core.LogWarn,
				Source:    "sysproxy",
				Message:   fmt.Sprintf("Failed to recover dirty system proxy state: %v", rErr),
			})
		} else if recovered {
			svc.appendLog(core.LogEntry{
				Timestamp: time.Now(),
				Level:     core.LogInfo,
				Source:    "sysproxy",
				Message:   "Recovered and restored system proxy settings left behind by an abnormal exit",
			})
		}

		cfg := store.GetSettings()
		applyTheme(cfg.Theme)

		application.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Role: mygo.RoleAppMenu},
			{Label: "File", Submenu: []*mygo.MenuItem{
				{Label: "Import ZJU-Connect TOML…", Accelerator: "CmdOrCtrl+I", Click: func(_ *mygo.MenuItem, w *mygo.Window) {
					go func() {
						_, _ = svc.ImportTOMLFile(context.Background())
					}()
				}},
				mygo.Separator(),
				{Role: mygo.RoleClose},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleViewMenu},
			{Role: mygo.RoleWindowMenu},
		}))

		mainWin = mygo.NewWindow(mygo.WindowOptions{
			Title:           "InfiConnect",
			Width:           1020,
			Height:          700,
			MinWidth:        840,
			MinHeight:       580,
			Hidden:          application.WasOpenedAtLogin(),
			TitleBarStyle:   mygo.TitleBarHidden,
			TitleBarHeight:  44,
			BackgroundColor: "light-dark(#f5f7fb, #0f1117)",
			URL:             "/",
		})

		mainWin.OnClose(func(e *mygo.CloseEvent) {
			if !quitting && store.GetSettings().CloseToTray {
				e.PreventDefault()
				mainWin.Hide()
			}
		})

		var trayErr error
		tray, trayErr = NewTrayController(svc, showMainWindow, func() {
			quitting = true
			application.Quit()
		})
		if trayErr != nil {
			log.Printf("tray unavailable: %v", trayErr)
		}

		svc.attachUI(mainWin, tray)
	})

	application.OnWindowAllClosed(func() {
		// Keep running in tray unless quitting.
	})

	application.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			showMainWindow()
		}
	})

	application.OnBeforeQuit(func(*mygo.QuitEvent) {
		quitting = true
		cleanupSync()
	})

	return application.Run()
}
