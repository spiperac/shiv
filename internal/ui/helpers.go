package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/shiv/internal/logger"
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

// newBoldLabel returns a label with bold text style.
func newBoldLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.TextStyle = fyne.TextStyle{Bold: true}
	return label
}

// recoverPanic logs a panic without crashing the process.
// Use as: defer recoverPanic("context description")
func recoverPanic(context string) {
	if r := recover(); r != nil {
		logger.Error("panic in %s: %v", context, r)
	}
}

func hasSuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}
