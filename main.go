package main

import (
	"flag"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/shiv/assets"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui"
)

func main() {
	verbose := flag.Bool("verbose", false, "enable verbose debug logging")
	flag.Parse()

	logger.Init(*verbose)

	fyneApp := app.NewWithID("io.shiv.proxy")
	fyneApp.SetIcon(fyne.NewStaticResource("logo.png", assets.Logo))
	settings := store.LoadSettings()
	fyneApp.Settings().SetTheme(ui.NewVagueTheme(settings.DarkTheme))

	ui.ShowLaunchScreen(fyneApp, func(projectPath string, launchWin fyne.Window) {
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

		proxyAddr := fmt.Sprintf("%s:%d", settings.ProxyHost, settings.ProxyPort)

		proxyServer, err := proxy.New(proxyAddr, projectStore)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			projectStore.Close()
			fyneApp.Quit()
			return
		}

		if proxyServer.CA().Fresh() {
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

		if settings.ProxyEnabled {
			go func() {
				if err := proxyServer.Start(); err != nil {
					logger.Error("proxy: %v", err)
				}
			}()
		}

		ui.ShowMainWindow(fyneApp, projectStore, proxyServer, settings, launchWin)
	})

	fyneApp.Run()
}
