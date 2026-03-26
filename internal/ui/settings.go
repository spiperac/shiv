package ui

import (
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/proxy"
)

var settingsWin fyne.Window

func showSettingsDialog(fyneApp fyne.App, parentWin fyne.Window, proxyServer *proxy.Proxy, keybinds *Keybinds) {
	if settingsWin != nil {
		settingsWin.RequestFocus()
		return
	}

	tabs := container.NewAppTabs(
		container.NewTabItem("Certificate", buildCertificateTab(parentWin)),
		container.NewTabItem("Proxy", buildProxyTab(fyneApp, proxyServer)),
		container.NewTabItem("Browser", buildBrowserTab(fyneApp, parentWin)),
		container.NewTabItem("Appearance & Shortcuts", buildAppearanceTab(fyneApp, keybinds)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	settingsWin = fyneApp.NewWindow("Settings")
	settingsWin.SetContent(container.NewPadded(tabs))
	settingsWin.Resize(fyne.NewSize(480, 380))
	settingsWin.SetOnClosed(func() {
		settingsWin = nil
	})
	closeOnEscape(settingsWin, settingsWin.Close)
	settingsWin.Show()
}

func buildCertificateTab(parentWin fyne.Window) fyne.CanvasObject {
	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapBreak

	installBtn := widget.NewButton("Install CA Certificate", func() {
		certAuthority, err := cert.Load()
		if err != nil {
			statusLabel.SetText("Error loading CA: " + err.Error())
			logger.Error("settings: load CA: %v", err)
			return
		}
		msg, err := certAuthority.InstallCA()
		if err != nil {
			statusLabel.SetText("Installation failed:\n" + err.Error())
			logger.Error("settings: install CA: %v", err)
			return
		}
		statusLabel.SetText(msg)
	})
	installBtn.Importance = widget.HighImportance

	exportBtn := widget.NewButton("Export Certificate (PEM)", func() {
		certAuthority, err := cert.Load()
		if err != nil {
			statusLabel.SetText("Error loading CA: " + err.Error())
			logger.Error("settings: load CA for export: %v", err)
			return
		}
		pemBytes, err := certAuthority.CertPEM()
		if err != nil {
			statusLabel.SetText("Error reading certificate: " + err.Error())
			logger.Error("settings: cert PEM: %v", err)
			return
		}
		saveDialog := dialog.NewFileSave(func(writeCloser fyne.URIWriteCloser, saveErr error) {
			if saveErr != nil || writeCloser == nil {
				return
			}
			defer writeCloser.Close()
			if _, writeErr := writeCloser.Write(pemBytes); writeErr != nil {
				statusLabel.SetText("Export failed: " + writeErr.Error())
				logger.Error("settings: export cert: %v", writeErr)
				return
			}
			statusLabel.SetText("Certificate exported successfully.")
		}, parentWin)
		saveDialog.SetFileName("shiv-ca.pem")
		saveDialog.Show()
	})

	return container.NewVBox(
		newBoldLabel("CA Certificate"),
		widget.NewLabel("Install the Shiv CA into your system trust store so browsers accept intercepted traffic."),
		installBtn,
		widget.NewSeparator(),
		widget.NewLabel("Export the CA certificate to import manually into a browser or device."),
		exportBtn,
		statusLabel,
	)
}

func buildProxyTab(fyneApp fyne.App, proxyServer *proxy.Proxy) fyne.CanvasObject {
	prefs := fyneApp.Preferences()

	hostEntry := widget.NewEntry()
	hostEntry.SetText(prefs.StringWithFallback(prefKeyProxyHost, defaultProxyHost))
	hostEntry.SetPlaceHolder("127.0.0.1")

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(prefs.IntWithFallback(prefKeyProxyPort, defaultProxyPort)))
	portEntry.SetPlaceHolder("9090")

	enabledCheck := widget.NewCheck("Proxy enabled", nil)
	enabledCheck.Checked = prefs.BoolWithFallback(prefKeyProxyEnabled, defaultProxyEnabled)

	proxyStatus := widget.NewLabel("")
	proxyStatus.Wrapping = fyne.TextWrapBreak

	saveBtn := widget.NewButton("Save & Restart Proxy", func() {
		proxyHost := hostEntry.Text
		proxyPort, err := strconv.Atoi(portEntry.Text)
		if err != nil || proxyPort < 1 || proxyPort > 65535 {
			proxyStatus.SetText("Invalid port number.")
			return
		}

		prefs.SetString(prefKeyProxyHost, proxyHost)
		prefs.SetInt(prefKeyProxyPort, proxyPort)
		prefs.SetBool(prefKeyProxyEnabled, enabledCheck.Checked)

		if enabledCheck.Checked {
			addr := fmt.Sprintf("%s:%d", proxyHost, proxyPort)
			if err := proxyServer.Restart(addr); err != nil {
				proxyStatus.SetText("Failed to restart proxy: " + err.Error())
				logger.Error("settings: restart proxy: %v", err)
				return
			}
			proxyStatus.SetText(fmt.Sprintf("Proxy restarted on %s:%d", proxyHost, proxyPort))
			proxyRunningBinding.Set(true)
			proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", proxyHost, proxyPort))
		} else {
			proxyServer.Stop()
			proxyStatus.SetText("Proxy stopped.")
			proxyRunningBinding.Set(false)
			proxyStatusBinding.Set("Proxy: stopped")
		}
	})
	saveBtn.Importance = widget.HighImportance

	return container.NewVBox(
		newBoldLabel("Proxy"),
		widget.NewForm(
			widget.NewFormItem("Host", hostEntry),
			widget.NewFormItem("Port", portEntry),
		),
		enabledCheck,
		saveBtn,
		proxyStatus,
	)
}

func buildAppearanceTab(fyneApp fyne.App, keybinds *Keybinds) fyne.CanvasObject {
	prefs := fyneApp.Preferences()

	themeSelect := widget.NewSelect([]string{"Dark", "Light"}, func(selected string) {
		isDark := selected == "Dark"
		prefs.SetBool(prefKeyDarkTheme, isDark)
		fyneApp.Settings().SetTheme(NewVagueTheme(isDark))
	})
	if prefs.BoolWithFallback(prefKeyDarkTheme, defaultDarkTheme) {
		themeSelect.SetSelected("Dark")
	} else {
		themeSelect.SetSelected("Light")
	}

	sendKeyEntry := widget.NewEntry()
	sendKeyEntry.SetText(prefs.StringWithFallback(prefKeySendRequest, string(defaultKeySendRequest)))

	closeTabKeyEntry := widget.NewEntry()
	closeTabKeyEntry.SetText(prefs.StringWithFallback(prefKeyCloseTab, string(defaultKeyCloseTab)))

	toRepeaterKeyEntry := widget.NewEntry()
	toRepeaterKeyEntry.SetText(prefs.StringWithFallback(prefKeyToRepeater, string(defaultKeyToRepeater)))

	sendKeyEntry.OnChanged = func(text string) {
		if len(text) > 0 {
			sendKeyEntry.SetText(strings.ToUpper(string([]rune(text)[0])))
		}
	}

	closeTabKeyEntry.OnChanged = func(text string) {
		if len(text) > 0 {
			closeTabKeyEntry.SetText(strings.ToUpper(string([]rune(text)[0])))
		}
	}

	toRepeaterKeyEntry.OnChanged = func(text string) {
		if len(text) > 0 {
			toRepeaterKeyEntry.SetText(strings.ToUpper(string([]rune(text)[0])))
		}
	}

	shortcutStatus := widget.NewLabel("")

	saveShortcutsBtn := widget.NewButton("Save Shortcuts", func() {
		keybinds.KeySendRequest = fyne.KeyName(strings.ToUpper(sendKeyEntry.Text))
		keybinds.KeyCloseTab = fyne.KeyName(strings.ToUpper(closeTabKeyEntry.Text))
		keybinds.KeyToRepeater = fyne.KeyName(strings.ToUpper(toRepeaterKeyEntry.Text))
		prefs.SetString(prefKeySendRequest, strings.ToUpper(sendKeyEntry.Text))
		prefs.SetString(prefKeyCloseTab, strings.ToUpper(closeTabKeyEntry.Text))
		prefs.SetString(prefKeyToRepeater, strings.ToUpper(toRepeaterKeyEntry.Text))
		keybinds.Update()
		shortcutStatus.SetText("Shortcuts updated.")
	})

	return container.NewVBox(
		newBoldLabel("Appearance"),
		widget.NewForm(
			widget.NewFormItem("Theme", themeSelect),
		),
		widget.NewSeparator(),
		newBoldLabel("Shortcuts"),
		widget.NewLabel("Single letter keys always, Ctrl modifier always applied (eg. Send is Ctrl + s)."),
		widget.NewForm(
			widget.NewFormItem("Send Request", sendKeyEntry),
			widget.NewFormItem("Close Tab", closeTabKeyEntry),
			widget.NewFormItem("Send to Repeater", toRepeaterKeyEntry),
		),
		saveShortcutsBtn,
		shortcutStatus,
	)
}

func buildBrowserTab(fyneApp fyne.App, win fyne.Window) fyne.CanvasObject {
	prefs := fyneApp.Preferences()
	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapBreak

	browsers := DetectBrowsers()

	names := make([]string, len(browsers))
	for i, b := range browsers {
		names[i] = b.Name
	}

	var browserSelect *widget.Select
	if len(browsers) == 0 {
		browserSelect = widget.NewSelect([]string{"No browsers detected"}, nil)
		browserSelect.SetSelected("No browsers detected")
		browserSelect.Disable()
	} else {
		browserSelect = widget.NewSelect(names, nil)
		// Pre-select saved default if it matches a detected browser.
		savedPath := prefs.String(prefKeyDefaultBrowser)
		for _, b := range browsers {
			if b.Path == savedPath {
				browserSelect.SetSelected(b.Name)
				break
			}
		}
		if browserSelect.Selected == "" {
			browserSelect.SetSelected(names[0])
		}
	}

	rescanBtn := widget.NewButton("Rescan", func() {
		fresh := DetectBrowsers()
		freshNames := make([]string, len(fresh))
		for i, b := range fresh {
			freshNames[i] = b.Name
		}
		browsers = fresh
		names = freshNames
		if len(freshNames) == 0 {
			browserSelect.Options = []string{"No browsers detected"}
			browserSelect.SetSelected("No browsers detected")
			browserSelect.Disable()
			statusLabel.SetText("No supported browsers found.")
		} else {
			browserSelect.Enable()
			browserSelect.Options = freshNames
			browserSelect.SetSelected(freshNames[0])
			statusLabel.SetText(fmt.Sprintf("Found %d browser(s).", len(freshNames)))
		}
		browserSelect.Refresh()
	})

	saveBtn := widget.NewButton("Save Default", func() {
		if len(browsers) == 0 {
			return
		}
		sel := browserSelect.Selected
		for _, b := range browsers {
			if b.Name == sel {
				prefs.SetString(prefKeyDefaultBrowser, b.Path)
				statusLabel.SetText("Default browser saved: " + b.Name)
				return
			}
		}
	})
	saveBtn.Importance = widget.HighImportance

	clearProfileBtn := widget.NewButton("Clear Browser Profile", func() {
		sel := browserSelect.Selected
		for _, b := range browsers {
			if b.Name == sel {
				if err := ClearBrowserProfile(fyneApp, b.Path); err != nil {
					statusLabel.SetText("Error: " + err.Error())
				} else {
					statusLabel.SetText("Profile cleared for: " + b.Name)
				}
				return
			}
		}
	})

	return container.NewVBox(
		newBoldLabel("Browser"),
		widget.NewLabel("Launch a browser pre-configured to route traffic through Shiv's proxy with an isolated profile."),
		widget.NewForm(
			widget.NewFormItem("Default browser", container.NewBorder(nil, nil, nil, rescanBtn, browserSelect)),
		),
		container.NewHBox(saveBtn, clearProfileBtn),
		statusLabel,
	)
}

// newBrowserPickerContent returns a simple select widget for the one-time picker dialog.
func newBrowserPickerContent(names []string, onChanged func(string)) fyne.CanvasObject {
	sel := widget.NewSelect(names, onChanged)
	if len(names) > 0 {
		sel.SetSelected(names[0])
	}
	return container.NewVBox(
		widget.NewLabel("Multiple browsers detected. Choose one to set as default:"),
		sel,
	)
}
