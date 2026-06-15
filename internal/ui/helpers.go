package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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

// saveToFile opens a file-save dialog and writes content to the chosen path.
func saveToFile(content []byte, defaultName string, win fyne.Window) {
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil || wc == nil {
			return
		}
		defer wc.Close()
		if _, err := wc.Write(content); err != nil {
			dialog.ShowError(err, win)
		}
	}, win)
	d.SetFileName(defaultName)
	d.Show()
}

// copyButton returns a button labelled "Copy" that copies text to clipboard.
func copyButton(label string, text func() string) *widget.Button {
	return widget.NewButton(label, func() {
		fyne.CurrentApp().Clipboard().SetContent(text())
	})
}

// paneHeader returns a border container with a bold label on the left and
// optional extra widgets on the right, used consistently across all detail panes.
func paneHeader(title string, right ...fyne.CanvasObject) fyne.CanvasObject {
	var rightObj fyne.CanvasObject
	if len(right) == 1 {
		rightObj = right[0]
	} else if len(right) > 1 {
		rightObj = container.NewHBox(right...)
	}
	return container.NewBorder(nil, nil, newBoldLabel(title), rightObj)
}

func formatSize(bytes int) string {
	switch {
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
