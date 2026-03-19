package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
)

func ShowMainWindow(app fyne.App, st *store.Store, p *proxy.Proxy, ps store.ProxySettings, launchWin fyne.Window) {
	w := app.NewWindow("Shiv")
	w.Resize(fyne.NewSize(1280, 800))
	w.SetMaster()

	repeater := newRepeaterTab(st, w)
	loot := &lootTab{st: st, win: w, repeater: repeater, selectedIdx: -1}
	historyTab := newHistoryTab(st, w, repeater, loot)
	interceptTab := newInterceptTab(st)

	lootContent := loot.build()
	tabs := container.NewAppTabs(
		container.NewTabItem("History", historyTab),
		container.NewTabItem("Intercept", interceptTab),
		container.NewTabItem("Repeater", repeater.build()),

		container.NewTabItem("Loot", lootContent),
		container.NewTabItem("Settings", newSettingsTab(w, st, p, ps)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(tabs)
	w.Show()

	launchWin.Close()
}

func placeholderTab(msg string) fyne.CanvasObject {
	return container.NewCenter(newLabel(msg))
}
