package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// readOnlyEntry is a multiline monospace text display that allows selection
// and copying but does not accept user input.
type readOnlyEntry struct {
	widget.Entry
}

func newReadOnlyEntry() *readOnlyEntry {
	e := &readOnlyEntry{}
	e.ExtendBaseWidget(e)
	e.MultiLine = true
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.Wrapping = fyne.TextWrapBreak
	return e
}

func (e *readOnlyEntry) TypedRune(_ rune) {}

func (e *readOnlyEntry) TypedKey(keyEvent *fyne.KeyEvent) {
	switch keyEvent.Name {
	case fyne.KeyUp, fyne.KeyDown, fyne.KeyLeft, fyne.KeyRight,
		fyne.KeyHome, fyne.KeyEnd, fyne.KeyPageUp, fyne.KeyPageDown:
		e.Entry.TypedKey(keyEvent)
	}
}

func (e *readOnlyEntry) TypedShortcut(shortcut fyne.Shortcut) {
	if _, ok := shortcut.(*fyne.ShortcutCopy); ok {
		e.Entry.TypedShortcut(shortcut)
	}
}

// repeaterEntry is a multiline monospace editor that intercepts Ctrl+S
// to trigger a send action.
type repeaterEntry struct {
	widget.Entry
	onCtrlS func()
}

func newRepeaterEntry() *repeaterEntry {
	e := &repeaterEntry{}
	e.ExtendBaseWidget(e)
	e.MultiLine = true
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.Wrapping = fyne.TextWrapBreak
	return e
}

func (e *repeaterEntry) TypedShortcut(shortcut fyne.Shortcut) {
	if cs, ok := shortcut.(*desktop.CustomShortcut); ok {
		if cs.KeyName == fyne.KeyS && cs.Modifier == fyne.KeyModifierControl {
			if e.onCtrlS != nil {
				e.onCtrlS()
			}
			return
		}
	}
	e.Entry.TypedShortcut(shortcut)
}

// newBoldLabel returns a label with bold text style.
func newBoldLabel(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.TextStyle = fyne.TextStyle{Bold: true}
	return label
}

// ContextMenuItem describes a single item in a right-click context menu.
type ContextMenuItem struct {
	Label  string
	Action func()
}
