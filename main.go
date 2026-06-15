package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/shiv/assets"
	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/plugin"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui"
)

func main() {
	verbose := flag.Bool("verbose", false, "enable verbose debug logging")
	profile := flag.Bool("profile", false, "enable memory profiling")
	flag.Parse()

	if *profile {
		go func() {
			for {
				time.Sleep(30 * time.Second)
				runtime.GC()
				f, err := os.Create("heap.prof")
				if err != nil {
					println("err:", err.Error())
					continue
				}
				pprof.WriteHeapProfile(f)
				f.Close()

				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				println("heap:", m.HeapAlloc)
			}
		}()
	}
	logger.Init(*verbose)

	fyneApp := app.NewWithID("net.shivapp")
	fyneApp.SetIcon(fyne.NewStaticResource("logo.png", assets.Logo))

	var openProject func(string, fyne.Window)
	openProject = func(projectPath string, launchWin fyne.Window) {
		if projectPath == "" {
			fyneApp.Quit()
			return
		}

		projectStore, err := store.Open(projectPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			fyneApp.Quit()
			return
		}

		bus := events.NewBus()
		bus.Register(projectStore.Intercept)
		bus.Register(projectStore)

		prefs := fyneApp.Preferences()

		pluginsDir := prefs.StringWithFallback("plugins_dir", fyneApp.Storage().RootURI().Path()+"/plugins")
		engine, err := plugin.NewEngine(pluginsDir, projectStore, bus)
		if err != nil {
			logger.Error("plugin engine: %v", err)
		} else if engine != nil {
			bus.Register(engine)
		}

		proxyAddr := fmt.Sprintf("%s:%d",
			prefs.StringWithFallback("proxy_host", "127.0.0.1"),
			prefs.IntWithFallback("proxy_port", 9090),
		)
		proxyServer, err := proxy.New(proxyAddr, bus)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			projectStore.Close()
			fyneApp.Quit()
			return
		}

		if proxyServer.CA().Fresh() && launchWin != nil {
			go func() {
				msg, err := proxyServer.CA().InstallCA()
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("CA install failed — import it manually from your system config dir:\n%v", err), launchWin)
					} else {
						dialog.ShowInformation("CA Installed", msg+"\n\nRestart your browser for changes to take effect.", launchWin)
					}
				})
			}()
		}

		if prefs.BoolWithFallback("proxy_enabled", true) {
			bus.EmitProxyRestart(events.ProxyRestartEvent{Addr: proxyAddr})
		}

		ui.ShowMainWindow(fyneApp, projectStore, bus, launchWin, func(newPath string) {
			bus.EmitProxyStop(events.ProxyStopEvent{})
			if engine != nil {
				engine.Close()
			}
			projectStore.Close()
			openProject(newPath, nil)
		})
	}

	ui.ShowLaunchScreen(fyneApp, openProject)

	fyneApp.Run()
}
