package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/proxy"
	"github.com/shiv/internal/store"
)

var settingsWin fyne.Window

func showSettingsDialog(app fyne.App, _ fyne.Window, proxyServer *proxy.Proxy) {
	if settingsWin != nil {
		settingsWin.RequestFocus()
		return
	}

	settings := store.LoadSettings()

	caStatus := widget.NewLabel("")
	caStatus.Wrapping = fyne.TextWrapBreak

	installBtn := widget.NewButton("Install CA Certificate", func() {
		certAuth, err := cert.Load()
		if err != nil {
			caStatus.SetText("Error loading CA: " + err.Error())
			logger.Error("settings: load CA: %v", err)
			return
		}
		msg, err := certAuth.InstallCA()
		if err != nil {
			caStatus.SetText("Installation failed:\n" + err.Error())
			logger.Error("settings: install CA: %v", err)
			return
		}
		caStatus.SetText(msg)
	})
	installBtn.Importance = widget.HighImportance

	hostEntry := widget.NewEntry()
	hostEntry.SetText(settings.ProxyHost)
	hostEntry.SetPlaceHolder("127.0.0.1")

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(settings.ProxyPort))
	portEntry.SetPlaceHolder("9090")

	enabledCheck := widget.NewCheck("Proxy enabled", nil)
	enabledCheck.Checked = settings.ProxyEnabled

	proxyStatus := widget.NewLabel("")
	proxyStatus.Wrapping = fyne.TextWrapBreak

	saveBtn := widget.NewButton("Save & Restart Proxy", func() {
		host := hostEntry.Text
		port, err := strconv.Atoi(portEntry.Text)
		if err != nil || port < 1 || port > 65535 {
			proxyStatus.SetText("Invalid port number.")
			return
		}

		updatedSettings := store.LoadSettings()
		updatedSettings.ProxyHost = host
		updatedSettings.ProxyPort = port
		updatedSettings.ProxyEnabled = enabledCheck.Checked

		if err := store.SaveSettings(updatedSettings); err != nil {
			proxyStatus.SetText("Failed to save settings: " + err.Error())
			logger.Error("settings: save: %v", err)
			return
		}

		if enabledCheck.Checked {
			addr := fmt.Sprintf("%s:%d", host, port)
			if err := proxyServer.Restart(addr); err != nil {
				proxyStatus.SetText("Failed to restart proxy: " + err.Error())
				logger.Error("settings: restart proxy: %v", err)
				return
			}
			proxyStatus.SetText(fmt.Sprintf("Proxy restarted on %s:%d", host, port))
			proxyRunningBinding.Set(true)
			proxyStatusBinding.Set(fmt.Sprintf("Proxy: running on %s:%d", host, port))
		} else {
			proxyServer.Stop()
			proxyStatus.SetText("Proxy stopped.")
			proxyRunningBinding.Set(false)
			proxyStatusBinding.Set("Proxy: stopped")
		}
	})
	saveBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		newBoldLabel("CA Certificate"),
		installBtn,
		caStatus,
		widget.NewSeparator(),
		newBoldLabel("Proxy"),
		widget.NewForm(
			widget.NewFormItem("Host", hostEntry),
			widget.NewFormItem("Port", portEntry),
		),
		enabledCheck,
		saveBtn,
		proxyStatus,
	)

	settingsWin = app.NewWindow("Settings")
	settingsWin.SetContent(container.NewPadded(content))
	settingsWin.Resize(fyne.NewSize(400, 350))
	settingsWin.SetOnClosed(func() {
		settingsWin = nil
	})
	settingsWin.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyEscape {
			settingsWin.Close()
		}
	})
	closeOnEscape(settingsWin, settingsWin.Close)
	settingsWin.Show()
}
