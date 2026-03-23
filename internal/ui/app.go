package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/assets"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
)

var proxyStatusBinding = binding.NewString()
var proxyRunningBinding = binding.NewBool()

func ShowMainWindow(app fyne.App, projectStore *store.Store, proxyServer *proxy.Proxy, settings store.Settings, launchWin fyne.Window) {
	mainWin := app.NewWindow("Shiv")
	mainWin.Resize(fyne.NewSize(1280, 800))
	mainWin.SetMaster()

	isDark := settings.DarkTheme

	toggleThemeBtn := widget.NewButtonWithIcon("", theme.ColorChromaticIcon(), nil)
	toggleThemeBtn.OnTapped = func() {
		isDark = !isDark
		app.Settings().SetTheme(NewVagueTheme(isDark))
		currentSettings := store.LoadSettings()
		currentSettings.DarkTheme = isDark
		store.SaveSettings(currentSettings)
	}

	settingsBtn := widget.NewButtonWithIcon("", AppIcon("settings"), func() {
		showSettingsDialog(app, mainWin, proxyServer)
	})

	proxyToggleBtn := widget.NewButtonWithIcon("", theme.MediaRecordIcon(), nil)

	proxyRunningBinding.AddListener(binding.NewDataListener(func() {
		running, _ := proxyRunningBinding.Get()
		if running {
			proxyToggleBtn.Importance = widget.SuccessImportance
		} else {
			proxyToggleBtn.Importance = widget.DangerImportance
		}
		proxyToggleBtn.Refresh()
	}))

	proxyToggleBtn.OnTapped = func() {
		isRunning, _ := proxyRunningBinding.Get()
		isRunning = !isRunning
		if isRunning {
			currentSettings := store.LoadSettings()
			addr := fmt.Sprintf("%s:%d", currentSettings.ProxyHost, currentSettings.ProxyPort)
			if err := proxyServer.Restart(addr); err != nil {
				isRunning = false
			}
		} else {
			proxyServer.Stop()
		}
		currentSettings := store.LoadSettings()
		currentSettings.ProxyEnabled = isRunning
		store.SaveSettings(currentSettings)
		proxyRunningBinding.Set(isRunning)
		if isRunning {
			proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", currentSettings.ProxyHost, currentSettings.ProxyPort))
		} else {
			proxyStatusBinding.Set("Proxy: stopped")
		}
	}

	if settings.ProxyEnabled {
		proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", settings.ProxyHost, settings.ProxyPort))
	} else {
		proxyStatusBinding.Set("Proxy: stopped")
	}
	proxyLabel := widget.NewLabelWithData(proxyStatusBinding)
	proxyLabel.Importance = widget.LowImportance
	proxyRunningBinding.Set(settings.ProxyEnabled)

	logo := canvas.NewImageFromResource(fyne.NewStaticResource("logo.png", assets.Logo))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(24, 24))

	appName := widget.NewLabel("Shiv")
	appName.TextStyle = fyne.TextStyle{Bold: true}

	functionBar := container.NewBorder(nil, nil,
		container.NewHBox(logo, appName),
		container.NewHBox(proxyLabel, proxyToggleBtn, settingsBtn, toggleThemeBtn),
		layout.NewSpacer(),
	)

	// All tabs follow the same pattern:
	// 1. Create struct via newXTab(...)
	// 2. Wire cross-tab dependencies
	// 3. Call .build() when passing to AppTabs
	repeater := newRepeaterTab(projectStore, mainWin)
	loot := newLootTab(projectStore, mainWin, repeater)
	repeater.loot = loot
	intruder := newIntruderTab(mainWin, projectStore, repeater, loot)
	history := newHistoryTab(projectStore, mainWin, repeater, loot, intruder)
	intercept := newInterceptTab(projectStore)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("History", AppIcon("history"), history.build()),
		container.NewTabItemWithIcon("Intercept", AppIcon("intercept"), intercept.build()),
		container.NewTabItemWithIcon("Repeater", AppIcon("repeater"), repeater.build()),
		container.NewTabItemWithIcon("Intruder", AppIcon("intruder"), intruder.build()),
		container.NewTabItemWithIcon("Loot", AppIcon("loot"), loot.build()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	mainWin.SetContent(container.NewBorder(functionBar, nil, nil, nil, tabs))
	mainWin.Show()
	launchWin.Close()
}
