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

const (
	prefKeyDarkTheme    = "dark_theme"
	prefKeyProxyHost    = "proxy_host"
	prefKeyProxyPort    = "proxy_port"
	prefKeyProxyEnabled = "proxy_enabled"

	defaultDarkTheme    = true
	defaultProxyHost    = "127.0.0.1"
	defaultProxyPort    = 9090
	defaultProxyEnabled = true
)

var proxyStatusBinding = binding.NewString()
var proxyRunningBinding = binding.NewBool()

func ShowMainWindow(fyneApp fyne.App, projectStore *store.Store, proxyServer *proxy.Proxy, launchWin fyne.Window) {
	prefs := fyneApp.Preferences()

	mainWin := fyneApp.NewWindow("Shiv")
	mainWin.Resize(fyne.NewSize(1280, 800))
	mainWin.SetMaster()

	isDark := prefs.BoolWithFallback(prefKeyDarkTheme, defaultDarkTheme)
	fyneApp.Settings().SetTheme(NewVagueTheme(isDark))

	toggleThemeBtn := widget.NewButtonWithIcon("", theme.ColorChromaticIcon(), nil)
	toggleThemeBtn.OnTapped = func() {
		isDark = !isDark
		fyneApp.Settings().SetTheme(NewVagueTheme(isDark))
		prefs.SetBool(prefKeyDarkTheme, isDark)
	}

	settingsBtn := widget.NewButtonWithIcon("", AppIcon("settings"), nil)

	proxyToggleBtn := widget.NewButtonWithIcon("", AppIcon("off-button"), nil)

	proxyRunningBinding.AddListener(binding.NewDataListener(func() {
		running, _ := proxyRunningBinding.Get()
		if running {
			proxyToggleBtn.SetIcon(AppIcon("on-button"))
		} else {
			proxyToggleBtn.SetIcon(AppIcon("off-button"))
		}
		proxyToggleBtn.Refresh()
	}))

	proxyToggleBtn.OnTapped = func() {
		isRunning, _ := proxyRunningBinding.Get()
		isRunning = !isRunning
		if isRunning {
			proxyHost := prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost)
			proxyPort := prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)
			addr := fmt.Sprintf("%s:%d", proxyHost, proxyPort)
			if err := proxyServer.Restart(addr); err != nil {
				isRunning = false
			}
		} else {
			proxyServer.Stop()
		}
		prefs.SetBool(prefKeyProxyEnabled, isRunning)
		proxyRunningBinding.Set(isRunning)
		proxyHost := prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost)
		proxyPort := prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)
		if isRunning {
			proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", proxyHost, proxyPort))
		} else {
			proxyStatusBinding.Set("Proxy: stopped")
		}
	}

	proxyHost := prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost)
	proxyPort := prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)
	proxyEnabled := prefs.BoolWithFallback(prefKeyProxyEnabled, defaultProxyEnabled)

	if proxyEnabled {
		proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", proxyHost, proxyPort))
	} else {
		proxyStatusBinding.Set("Proxy: stopped")
	}
	proxyRunningBinding.Set(proxyEnabled)

	proxyLabel := widget.NewLabelWithData(proxyStatusBinding)
	proxyLabel.Importance = widget.LowImportance

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

	keybinds := newKeybinds(mainWin, tabs, history, repeater, prefs)
	settingsBtn.OnTapped = func() {
		showSettingsDialog(fyneApp, mainWin, proxyServer, keybinds)
	}

	mainWin.SetContent(container.NewBorder(functionBar, nil, nil, nil, tabs))
	mainWin.Show()
	launchWin.Close()
}
