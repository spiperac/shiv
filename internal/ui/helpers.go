package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func closeOnEscape(win fyne.Window, closeFn func()) {
	previous := win.Canvas().OnTypedKey()
	win.Canvas().SetOnTypedKey(func(keyEvent *fyne.KeyEvent) {
		if keyEvent.Name == fyne.KeyEscape {
			closeFn()
			win.Canvas().SetOnTypedKey(previous)
		}
	})
}

func newBoldLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.TextStyle = fyne.TextStyle{Bold: true}
	return label
}
