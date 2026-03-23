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

func ShowMainWindow(app fyne.App, projectStore *store.Store, proxyServer *proxy.Proxy, settings store.Settings, launchWin fyne.Window) {
	mainWin := app.NewWindow("Shiv")
	mainWin.Resize(fyne.NewSize(1280, 800))
	mainWin.SetMaster()

	toggleBtn := widget.NewButtonWithIcon("", theme.ColorChromaticIcon(), nil)
	isDark := settings.DarkTheme
	toggleBtn.OnTapped = func() {
		isDark = !isDark
		app.Settings().SetTheme(NewVagueTheme(isDark))
		current := store.LoadSettings()
		current.DarkTheme = isDark
		store.SaveSettings(current)
	}
	settingsBtn := widget.NewButtonWithIcon("", AppIcon("settings"), func() {
		showSettingsDialog(app, mainWin, proxyServer)
	})
	logo := canvas.NewImageFromResource(fyne.NewStaticResource("logo.png", assets.Logo))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(24, 24))

	appName := widget.NewLabel("Shiv")
	appName.TextStyle = fyne.TextStyle{Bold: true}

	functionBar := container.NewBorder(nil, nil,
		container.NewHBox(logo, appName),
		container.NewHBox(settingsBtn, toggleBtn),
		layout.NewSpacer(),
	)

	repeater := newRepeaterTab(projectStore, mainWin)
	loot := &lootTab{projectStore: projectStore, win: mainWin, repeater: repeater, selectedIdx: -1}
	historyTab := newHistoryTab(projectStore, mainWin, repeater, loot)
	interceptTab := newInterceptTab(projectStore)
	lootContent := loot.build()

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("History", AppIcon("history"), historyTab),
		container.NewTabItemWithIcon("Intercept", AppIcon("intercept"), interceptTab),
		container.NewTabItemWithIcon("Repeater", AppIcon("repeater"), repeater.build()),
		container.NewTabItemWithIcon("Loot", AppIcon("loot"), lootContent),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	mainWin.SetContent(container.NewBorder(functionBar, nil, nil, nil, tabs))
	mainWin.Show()
	launchWin.Close()
}
