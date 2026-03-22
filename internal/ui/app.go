package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/assets"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
)

func ShowMainWindow(app fyne.App, st *store.Store, p *proxy.Proxy, ps store.ProxySettings, launchWin fyne.Window) {
	w := app.NewWindow("Shiv")
	w.Resize(fyne.NewSize(1280, 800))
	w.SetMaster()

	dark := true

	toggleBtn := widget.NewButtonWithIcon("", theme.ColorChromaticIcon(), nil)
	toggleBtn.OnTapped = func() {
		dark = !dark
		app.Settings().SetTheme(NewVagueTheme(dark))
	}

	logo := canvas.NewImageFromResource(fyne.NewStaticResource("logo.png", assets.Logo))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(24, 24))

	appName := widget.NewLabel("Shiv")
	appName.TextStyle = fyne.TextStyle{Bold: true}

	functionBar := container.NewBorder(nil, nil,
		container.NewHBox(logo, appName),
		toggleBtn,
		layout.NewSpacer(),
	)

	repeater := newRepeaterTab(st, w)
	loot := &lootTab{st: st, win: w, repeater: repeater, selectedIdx: -1}
	historyTab := newHistoryTab(st, w, repeater, loot)
	interceptTab := newInterceptTab(st)
	lootContent := loot.build()

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("History", AppIcon("history"), historyTab),
		container.NewTabItemWithIcon("Intercept", AppIcon("intercept"), interceptTab),
		container.NewTabItemWithIcon("Repeater", AppIcon("repeater"), repeater.build()),
		container.NewTabItemWithIcon("Loot", AppIcon("loot"), lootContent),
		container.NewTabItemWithIcon("Settings", AppIcon("settings"), newSettingsTab(w, st, p, ps)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(container.NewBorder(functionBar, nil, nil, nil, tabs))
	w.Show()
	launchWin.Close()
}
