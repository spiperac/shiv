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

func newSettingsTab(win fyne.Window, st *store.Store, p *proxy.Proxy, ps store.ProxySettings) fyne.CanvasObject {
	// CA section.
	caStatus := widget.NewLabel("")
	caStatus.Wrapping = fyne.TextWrapBreak

	installBtn := widget.NewButton("Install CA Certificate", func() {
		ca, err := cert.Load()
		if err != nil {
			caStatus.SetText("Error loading CA: " + err.Error())
			logger.Error("settings: load CA: %v", err)
			return
		}
		msg, err := ca.InstallCA()
		if err != nil {
			caStatus.SetText("Installation failed:\n" + err.Error())
			logger.Error("settings: install CA: %v", err)
			return
		}
		caStatus.SetText(msg)
	})
	installBtn.Importance = widget.HighImportance

	hostEntry := widget.NewEntry()
	hostEntry.SetText(ps.Host)
	hostEntry.SetPlaceHolder("127.0.0.1")

	portEntry := widget.NewEntry()
	portEntry.SetText(strconv.Itoa(ps.Port))
	portEntry.SetPlaceHolder("9090")

	enabledCheck := widget.NewCheck("Proxy enabled", nil)
	enabledCheck.Checked = ps.Enabled

	proxyStatus := widget.NewLabel("")
	proxyStatus.Wrapping = fyne.TextWrapBreak

	saveBtn := widget.NewButton("Save & Restart Proxy", func() {
		host := hostEntry.Text
		portStr := portEntry.Text
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			proxyStatus.SetText("Invalid port number.")
			return
		}

		newPS := store.ProxySettings{
			Host:    host,
			Port:    port,
			Enabled: enabledCheck.Checked,
		}

		if err := st.SaveProxySettings(newPS); err != nil {
			proxyStatus.SetText("Failed to save settings: " + err.Error())
			logger.Error("settings: save proxy: %v", err)
			return
		}

		if enabledCheck.Checked {
			addr := fmt.Sprintf("%s:%d", host, port)
			if err := p.Restart(addr); err != nil {
				proxyStatus.SetText("Failed to restart proxy: " + err.Error())
				logger.Error("settings: restart proxy: %v", err)
				return
			}
			proxyStatus.SetText(fmt.Sprintf("Proxy restarted on %s:%d", host, port))
		} else {
			p.Stop()
			proxyStatus.SetText("Proxy stopped.")
		}
	})
	saveBtn.Importance = widget.HighImportance

	return container.NewVBox(
		widget.NewSeparator(),
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
}
