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

// tappableTable extends widget.Table with right-click context menu support
// and full-row highlight on selection. It is the standard table used throughout
// Shiv wherever rows need a context menu.
//
// selectedRow is a func so the table can always read the latest selection from
// the caller's state without storing a separate copy.
type tappableTable struct {
	widget.Table
	win         fyne.Window
	selectedRow func() int
	menuItems   func(row int) []ContextMenuItem
}

func newTappableTable(
	win fyne.Window,
	length func() (int, int),
	createCell func() fyne.CanvasObject,
	updateCell func(widget.TableCellID, fyne.CanvasObject),
	selectedRow func() int,
	menuItems func(row int) []ContextMenuItem,
) *tappableTable {
	t := &tappableTable{
		win:         win,
		selectedRow: selectedRow,
		menuItems:   menuItems,
	}
	t.Length = length
	t.CreateCell = createCell
	t.UpdateCell = updateCell
	t.ExtendBaseWidget(t)
	return t
}

// TappedSecondary fires on right-click and shows a context menu for the
// currently selected data row.
func (t *tappableTable) TappedSecondary(ev *fyne.PointEvent) {
	row := t.selectedRow()
	if row < 0 || t.menuItems == nil {
		return
	}
	items := t.menuItems(row)
	if len(items) == 0 {
		return
	}
	menuItems := make([]*fyne.MenuItem, len(items))
	for i, item := range items {
		it := item
		menuItems[i] = fyne.NewMenuItem(it.Label, it.Action)
	}
	widget.ShowPopUpMenuAtPosition(
		fyne.NewMenu("", menuItems...),
		t.win.Canvas(),
		ev.AbsolutePosition,
	)
}

// highlightTableRow applies importance styling to a cell label based on
// whether its row is currently selected, achieving a full-row highlight effect.
func highlightTableRow(label *widget.Label, rowIsSelected bool, defaultImportance widget.Importance) {
	if rowIsSelected {
		label.Importance = widget.HighImportance
	} else {
		label.Importance = defaultImportance
	}
}
