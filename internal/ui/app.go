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
	prefKeyUserTheme    = "user_theme"

	defaultDarkTheme    = true
	defaultProxyHost    = "127.0.0.1"
	defaultProxyPort    = 9090
	defaultProxyEnabled = true
)

// Package-level bindings shared between app.go and settings.go.
// Both files are in the same package so this is safe — there is only
// ever one app instance in a desktop process.
var proxyStatusBinding = binding.NewString()
var proxyRunningBinding = binding.NewBool()

// loadActiveTheme reads the user theme from prefs and loads it.
// Returns nil if no user theme is set or loading fails (falls back to default).
func loadActiveTheme(fyneApp fyne.App) *LoadedTheme {
	name := fyneApp.Preferences().String(prefKeyUserTheme)
	if name == "" {
		return nil
	}
	dir := ThemesDir(fyneApp.Storage().RootURI().Path())
	lt, err := loadThemeByName(name, dir)
	if err != nil {
		return nil
	}
	return lt
}

// applyTheme reads current dark/light and user theme prefs and applies the theme.
// Call this from anywhere — toggle, settings, startup. No shared mutable state.
func applyTheme(fyneApp fyne.App) {
	isDark := fyneApp.Preferences().BoolWithFallback(prefKeyDarkTheme, defaultDarkTheme)
	fyneApp.Settings().SetTheme(NewVagueThemeWithLoaded(isDark, loadActiveTheme(fyneApp)))
}

// setProxyStatus updates both package-level bindings and persists the running
// state to prefs. Must be called on the Fyne main goroutine.
func setProxyStatus(prefs fyne.Preferences, running bool) {
	proxyHost := prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost)
	proxyPort := prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)
	prefs.SetBool(prefKeyProxyEnabled, running)
	_ = proxyRunningBinding.Set(running)
	if running {
		_ = proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", proxyHost, proxyPort))
	} else {
		_ = proxyStatusBinding.Set("Proxy: stopped")
	}
}

func ShowMainWindow(fyneApp fyne.App, projectStore *store.Store, proxyServer *proxy.Proxy, launchWin fyne.Window) {
	prefs := fyneApp.Preferences()

	mainWin := fyneApp.NewWindow("Shiv")
	mainWin.Resize(fyne.NewSize(1280, 800))
	mainWin.SetMaster()

	applyTheme(fyneApp)

	toggleThemeBtn := widget.NewButtonWithIcon("", theme.ColorChromaticIcon(), func() {
		isDark := !prefs.BoolWithFallback(prefKeyDarkTheme, defaultDarkTheme)
		prefs.SetBool(prefKeyDarkTheme, isDark)
		applyTheme(fyneApp)
	})

	// Hide toggle if active user theme only has one variant.
	if lt := loadActiveTheme(fyneApp); lt != nil && !lt.HasBoth {
		toggleThemeBtn.Hide()
	}

	settingsBtn := widget.NewButtonWithIcon("", AppIcon("toolbox"), nil)

	browserBtn := widget.NewButtonWithIcon("", AppIcon("web"), func() {
		launchDefaultBrowser(fyneApp, mainWin)
	})

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
				// Restart failed synchronously — don't mark as running.
				isRunning = false
			}
		} else {
			proxyServer.Stop()
		}

		setProxyStatus(prefs, isRunning)
	}

	// Initialise status from persisted prefs.
	setProxyStatus(prefs, prefs.BoolWithFallback(prefKeyProxyEnabled, defaultProxyEnabled))

	proxyLabel := widget.NewLabelWithData(proxyStatusBinding)
	proxyLabel.Importance = widget.LowImportance

	logo := canvas.NewImageFromResource(fyne.NewStaticResource("logo.png", assets.Logo))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(24, 24))

	appName := widget.NewLabel("Shiv")
	appName.TextStyle = fyne.TextStyle{Bold: true}

	functionBar := container.NewBorder(nil, nil,
		container.NewHBox(logo, appName),
		container.NewHBox(proxyLabel, proxyToggleBtn, settingsBtn, browserBtn, toggleThemeBtn),
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
