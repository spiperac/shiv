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

	a := app.NewWithID("io.shiv.proxy")
	a.SetIcon(fyne.NewStaticResource("logo.png", assets.Logo))

	ui.ShowLaunchScreen(a, func(projectPath string, launchWin fyne.Window) {
		if projectPath == "" {
			a.Quit()
			return
		}

		st, err := store.Open(projectPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			a.Quit()
			return
		}

		ps := st.LoadProxySettings()
		addr := fmt.Sprintf("%s:%d", ps.Host, ps.Port)

		p, err := proxy.New(addr, st)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			st.Close()
			a.Quit()
			return
		}

		if p.CA().Fresh() {
			go func() {
				msg, err := p.CA().InstallCA()
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("CA install failed — import it manually from your system config dir:\n%v", err), launchWin)
					} else {
						dialog.ShowInformation("CA Installed", msg+"\n\nRestart your browser for changes to take effect.", launchWin)
					}
				})
			}()
		}

		if ps.Enabled {
			go func() {
				if err := p.Start(); err != nil {
					logger.Error("proxy: %v", err)
				}
			}()
		}

		ui.ShowMainWindow(a, st, p, ps, launchWin)
	})

	a.Run()
}
