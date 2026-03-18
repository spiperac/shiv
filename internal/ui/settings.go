package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/cert"
	"github.com/shiv/internal/logger"
)

func newSettingsTab(win fyne.Window) fyne.CanvasObject {
	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapBreak

	installBtn := widget.NewButton("Install CA Certificate", func() {
		ca, err := cert.Load()
		if err != nil {
			statusLabel.SetText("Error loading CA: " + err.Error())
			logger.Error("settings: load CA: %v", err)
			return
		}
		msg, err := ca.InstallCA()
		if err != nil {
			statusLabel.SetText("Installation failed:\n" + err.Error())
			logger.Error("settings: install CA: %v", err)
			return
		}
		statusLabel.SetText(msg)
	})
	installBtn.Importance = widget.HighImportance

	return container.NewVBox(
		installBtn,
		statusLabel,
	)
}
