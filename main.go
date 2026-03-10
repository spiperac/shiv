package main

import (
	"flag"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui"
)

func main() {
	verbose := flag.Bool("verbose", false, "enable verbose debug logging")
	addr := flag.String("addr", ":8080", "proxy listen address")
	flag.Parse()

	logger.Init(*verbose)

	a := app.NewWithID("io.shiv.proxy")

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

		p, err := proxy.New(*addr, st)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[shiv] fatal: %v\n", err)
			st.Close()
			a.Quit()
			return
		}

		go func() {
			if err := p.Start(); err != nil {
				logger.Error("proxy: %v", err)
			}
		}()

		ui.ShowMainWindow(a, st, launchWin)
	})

	a.Run()
}
