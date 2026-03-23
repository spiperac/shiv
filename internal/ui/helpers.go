package ui

import (
	"fyne.io/fyne/v2"
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
