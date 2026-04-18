package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/assets"
	"github.com/shiv/internal/events"
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

func ShowMainWindow(fyneApp fyne.App, projectStore *store.Store, bus *events.Bus, launchWin fyne.Window, onSwitchProject func(string)) {
	prefs := fyneApp.Preferences()

	mainWin := fyneApp.NewWindow("Shiv")
	mainWin.Resize(fyne.NewSize(1280, 800))
	mainWin.SetIcon(fyne.NewStaticResource("logo.png", assets.Logo))

	mainWin.SetMaster()
	applyTheme(fyneApp)

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
			bus.EmitProxyRestart(events.ProxyRestartEvent{Addr: addr})
		} else {
			bus.EmitProxyStop(events.ProxyStopEvent{})
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

	repeater := newRepeaterTab(projectStore, bus, mainWin)
	loot := newLootTab(projectStore, mainWin, repeater)
	repeater.loot = loot
	intruder := newIntruderTab(mainWin, projectStore, repeater, loot)
	history := newHistoryTab(projectStore, mainWin, repeater, loot, intruder)
	intercept := newInterceptTab(projectStore)
	plugins := newPluginsTab(projectStore, bus, mainWin)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("History", AppIcon("history"), history.build()),
		container.NewTabItemWithIcon("Intercept", AppIcon("intercept"), intercept.build()),
		container.NewTabItemWithIcon("Repeater", AppIcon("repeater"), repeater.build()),
		container.NewTabItemWithIcon("Intruder", AppIcon("intruder"), intruder.build()),
		container.NewTabItemWithIcon("Loot", AppIcon("loot"), loot.build()),
		container.NewTabItemWithIcon("Plugins", AppIcon("plugin"), plugins.build()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	keybinds := newKeybinds(mainWin, tabs, history, repeater, prefs)

	settingsMenuItem := fyne.NewMenuItem("Settings", func() {
		showSettingsDialog(fyneApp, mainWin, bus, keybinds)
	})
	settingsMenuItem.Icon = AppIcon("toolbox")

	toggleThemeMenuItem := fyne.NewMenuItem("Toggle Theme", func() {
		isDark := !prefs.BoolWithFallback(prefKeyDarkTheme, defaultDarkTheme)
		prefs.SetBool(prefKeyDarkTheme, isDark)
		applyTheme(fyneApp)
	})
	toggleThemeMenuItem.Icon = theme.ColorChromaticIcon()
	launchBrowserMenuItem := fyne.NewMenuItem("Launch Browser", func() {
		launchDefaultBrowser(fyneApp, mainWin)
	})

	newProjectItem := fyne.NewMenuItem("New Project", func() {
		d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
			if err != nil || wc == nil {
				return
			}
			wc.Close()
			path := wc.URI().Path()
			saveRecent(path)
			mainWin.Hide()
			onSwitchProject(path)
		}, mainWin)
		d.SetFileName("untitled.shiv")
		d.SetFilter(storage.NewExtensionFileFilter([]string{".shiv"}))
		d.Show()
	})
	newProjectItem.Icon = theme.DocumentCreateIcon()

	openProjectItem := fyne.NewMenuItem("Open Project", func() {
		d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
			if err != nil || rc == nil {
				return
			}
			rc.Close()
			path := rc.URI().Path()
			saveRecent(path)
			mainWin.Hide()
			onSwitchProject(path)
		}, mainWin)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".shiv"}))
		d.Show()
	})
	openProjectItem.Icon = theme.FolderOpenIcon()

	quitItem := fyne.NewMenuItem("Quit", func() {
		fyneApp.Quit()
	})

	menuItems := []*fyne.MenuItem{
		newProjectItem,
		openProjectItem,
		fyne.NewMenuItemSeparator(),
		launchBrowserMenuItem,
		toggleThemeMenuItem,
		settingsMenuItem,
		fyne.NewMenuItemSeparator(),
		quitItem,
	}
	if lt := loadActiveTheme(fyneApp); lt != nil && !lt.HasBoth {
		toggleThemeMenuItem.Disabled = true
	}

	menu := fyne.NewMenu("", menuItems...)
	var menuBtn *widget.Button

	menuBtn = widget.NewButtonWithIcon("", theme.MenuIcon(), func() {
		widget.ShowPopUpMenuAtRelativePosition(menu, mainWin.Canvas(), fyne.NewPos(0, menuBtn.Size().Height), menuBtn)
	})

	optionsBar := container.NewBorder(
		container.NewBorder(nil, nil, nil,
			container.NewHBox(proxyLabel, proxyToggleBtn, browserBtn, menuBtn),
		),
		nil, nil, nil,
	)

	mainWin.SetContent(container.NewStack(
		tabs,
		optionsBar,
	))
	mainWin.Show()
	if launchWin != nil {
		launchWin.Close()
	}
}
