package ui

// DataTable — a virtualised, row-centric table for Fyne.
//
// Architecture
// ────────────
// Fyne renderers may only contain canvas.Object items, not child widgets.
// Interactive hit-testing (Tapped, TappedSecondary, Dragged) requires
// fyne.Widget.  We therefore split the widget into two layers:
//
//   visual layer  – DataTable itself, rendered with canvas primitives
//                   (rectangles for row backgrounds, canvas.Text for cells,
//                    canvas.Line for column dividers).
//
//   input layer   – a transparent fyne.Widget (dtInputLayer) that sits on
//                   top of the visual layer inside a container.NewStack().
//                   It handles Tapped / TappedSecondary by converting the
//                   click Y-position to a data-row index and routing the
//                   event.  It also owns child *colDivider widgets for
//                   column-resize drag.
//
// Both layers are wrapped in container.NewStack() by Build().
// Callers use the object returned from Build() in their layouts.
//
// Scrolling
// ─────────
// DataTable implements fyne.Scrollable so a container.NewScroll() parent
// forwards wheel events to it.  Internally we maintain scrollOffset
// (the first visible data-row index) and re-render on every change.
// ScrollToRow() scrolls programmatically.
//
// Selection
// ─────────
// Selection is tracked by the caller-supplied RowID int64, not by index.
// This means inserting rows at the top (as History does when new requests
// arrive) never loses the highlight.  selectedRowIndex() re-resolves the
// ID to its current position on every render pass.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ─────────────────────────────────────────────────────────────
// Public configuration types
// ─────────────────────────────────────────────────────────────

// DataTableColumn describes one column.
type DataTableColumn struct {
	Header string
	Width  float32
}

// ─────────────────────────────────────────────────────────────
// Layout constants
// ─────────────────────────────────────────────────────────────

const (
	dtRowH    float32 = 32 // height of every data row
	dtHdrH    float32 = 34 // height of the header row
	dtPadX    float32 = 8  // horizontal text padding inside a cell
	dtDivW    float32 = 6  // width of the draggable divider hit area
	dtMinColW float32 = 40 // minimum column width after a resize drag
)

// ─────────────────────────────────────────────────────────────
// DataTable widget (visual layer)
// ─────────────────────────────────────────────────────────────

// DataTable is the public widget.
// Create it with NewDataTable(), set all callbacks, call SetWindow(),
// then use the fyne.CanvasObject returned by Build() in your layout.
type DataTable struct {
	widget.BaseWidget

	// ── column definitions ────────────────────────────────────
	Columns []DataTableColumn

	// ── data callbacks ────────────────────────────────────────
	// These may be updated at runtime; call Refresh() after any change.

	// RowCount returns the current number of data rows (header excluded).
	RowCount func() int
	// CellValue returns the display text for a zero-based (row, col) pair.
	CellValue func(row, col int) string
	// CellStyle returns the colour importance for a cell.
	// Nil means widget.MediumImportance (normal foreground colour) everywhere.
	CellStyle func(row, col int) widget.Importance
	// RowID returns a stable unique int64 for each row index.
	// Used to re-find the selected row after the data changes.
	RowID func(row int) int64

	// ── interaction callbacks ─────────────────────────────────

	// OnSelect is called with the zero-based row index on left-click.
	OnSelect func(row int)
	// MenuItems returns the right-click context menu entries for a row.
	// Return nil or an empty slice to suppress the menu for that row.
	// If the field itself is nil no menu is ever shown.
	MenuItems func(row int) []ContextMenuItem

	// ── internal state ────────────────────────────────────────

	colWidths    []float32 // live widths, initialised from Columns
	selectedID   int64     // RowID of the selected row; 0 = none
	hasSelected  bool
	scrollOffset int     // index of the first visible data row
	scrollFrac   float32 // fractional row accumulator for smooth trackpad scrolling

	win  fyne.Window // needed to anchor pop-up menus
	rend *dtRenderer // set by CreateRenderer
}

// NewDataTable allocates and extends a DataTable.
func NewDataTable() *DataTable {
	t := &DataTable{}
	t.ExtendBaseWidget(t)
	return t
}

// SetWindow gives the DataTable a window reference so it can show
// right-click pop-up menus.  Must be called before the widget is shown.
func (t *DataTable) SetWindow(w fyne.Window) { t.win = w }

// Build constructs the complete canvas object (visual + input overlay in
// a Stack) that callers place in their layouts.  Call once after all
// callbacks are set.
func (t *DataTable) Build() fyne.CanvasObject {
	t.initColWidths()
	return container.NewStack(t, newDTInputLayer(t))
}

// SetSelected marks the row whose RowID equals id as selected and repaints.
// Pass 0 to clear the selection.
func (t *DataTable) SetSelected(id int64) {
	t.selectedID = id
	t.hasSelected = id != 0
	t.Refresh()
}

// ClearSelection removes any row highlight and repaints.
func (t *DataTable) ClearSelection() {
	t.selectedID = 0
	t.hasSelected = false
	t.Refresh()
}

// ScrollToRow scrolls the viewport so that the given data row is visible.
func (t *DataTable) ScrollToRow(row int) {
	vis := t.visibleSlots()
	if row < t.scrollOffset {
		t.scrollOffset = row
	} else if row >= t.scrollOffset+vis {
		t.scrollOffset = row - vis + 1
	}
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.Refresh()
}

// scrollFrac accumulates sub-row scroll deltas so slow trackpad scrolling
// still eventually moves the table.
// ── internal state field — added alongside scrollOffset ──────────────────
// (kept here as a method-level note; the field is on DataTable below)

// Scrolled implements fyne.Scrollable.
func (t *DataTable) Scrolled(ev *fyne.ScrollEvent) {
	nData := t.rowCount()
	vis := t.visibleSlots()
	max := nData - vis
	if max < 0 {
		max = 0
	}

	// Accumulate fractional rows so trackpad slow-swipe still scrolls.
	// DY is in pixels (negative = scroll down = higher row indices).
	t.scrollFrac -= ev.Scrolled.DY / dtRowH
	rows := int(t.scrollFrac)
	t.scrollFrac -= float32(rows)

	// Guarantee at least 1 row movement per discrete wheel click.
	// On a wheel click DY is typically ±3..5 px; the fraction alone
	// may round to 0 for several clicks before accumulating.
	if rows == 0 {
		if ev.Scrolled.DY < 0 {
			rows = 1
		} else if ev.Scrolled.DY > 0 {
			rows = -1
		}
	}

	t.scrollOffset += rows
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	if t.scrollOffset > max {
		t.scrollOffset = max
	}
	t.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (t *DataTable) CreateRenderer() fyne.WidgetRenderer {
	t.initColWidths()
	r := newDTRenderer(t)
	t.rend = r
	return r
}

// ── private helpers ──────────────────────────────────────────

func (t *DataTable) initColWidths() {
	if len(t.colWidths) == len(t.Columns) {
		return
	}
	t.colWidths = make([]float32, len(t.Columns))
	for i, c := range t.Columns {
		t.colWidths[i] = c.Width
	}
}

func (t *DataTable) rowCount() int {
	if t.RowCount == nil {
		return 0
	}
	return t.RowCount()
}

// visibleSlots returns how many row-slot objects fit in the current height.
func (t *DataTable) visibleSlots() int {
	if t.rend == nil {
		return 0
	}
	h := t.rend.size.Height - dtHdrH
	if h < 0 {
		return 0
	}
	return int(h/dtRowH) + 2
}

// selectedRowIndex maps selectedID back to its current row index, or -1.
func (t *DataTable) selectedRowIndex() int {
	if !t.hasSelected || t.RowID == nil {
		return -1
	}
	n := t.rowCount()
	for i := 0; i < n; i++ {
		if t.RowID(i) == t.selectedID {
			return i
		}
	}
	return -1
}

func (t *DataTable) totalWidth() float32 {
	var w float32
	for _, cw := range t.colWidths {
		w += cw
	}
	return w
}

// cellColor resolves a (row, col) pair to a concrete colour via CellStyle.
func (t *DataTable) cellColor(row, col int) color.Color {
	imp := widget.MediumImportance
	if t.CellStyle != nil {
		imp = t.CellStyle(row, col)
	}
	switch imp {
	case widget.DangerImportance:
		return theme.Color(theme.ColorNameError)
	case widget.WarningImportance:
		return theme.Color(theme.ColorNameWarning)
	case widget.SuccessImportance:
		return theme.Color(theme.ColorNameSuccess)
	case widget.LowImportance:
		return theme.Color(theme.ColorNamePlaceHolder)
	case widget.HighImportance:
		return theme.Color(theme.ColorNamePrimary)
	default:
		return theme.Color(theme.ColorNameForeground)
	}
}

// tappedRow handles a confirmed left-click on data row index row.
func (t *DataTable) tappedRow(row int) {
	if t.RowID != nil {
		t.selectedID = t.RowID(row)
		t.hasSelected = true
	}
	t.Refresh()
	if t.OnSelect != nil {
		t.OnSelect(row)
	}
}

// secondaryTappedRow handles a confirmed right-click on data row index row.
// absPos is the absolute screen position used to anchor the pop-up menu.
func (t *DataTable) secondaryTappedRow(row int, absPos fyne.Position) {
	if t.MenuItems == nil || t.win == nil {
		return
	}
	items := t.MenuItems(row)
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
		absPos,
	)
}

// ─────────────────────────────────────────────────────────────
// dtRenderer — canvas-object visual layer
// ─────────────────────────────────────────────────────────────

const (
	dtScrollW float32 = 8 // scrollbar width
)

type dtRenderer struct {
	t    *DataTable
	size fyne.Size

	// header
	hdrBg    *canvas.Rectangle
	hdrTexts []*canvas.Text
	divLines []*canvas.Line // visual column separators in the header

	// virtualised body rows
	slots     int                 // number of row-slots currently allocated
	rowBgs    []*canvas.Rectangle // one per slot
	cellTexts [][]*canvas.Text    // [slot][col]

	// scrollbar
	scrollTrack *canvas.Rectangle
	scrollThumb *canvas.Rectangle
}

func newDTRenderer(t *DataTable) *dtRenderer {
	nCols := len(t.Columns)

	r := &dtRenderer{t: t}
	r.hdrBg = canvas.NewRectangle(color.Transparent)

	r.hdrTexts = make([]*canvas.Text, nCols)
	for i, col := range t.Columns {
		tx := canvas.NewText(col.Header, color.White)
		tx.TextStyle = fyne.TextStyle{Bold: true}
		tx.TextSize = theme.TextSize()
		r.hdrTexts[i] = tx
	}

	// nCols-1 dividers
	if nCols > 1 {
		r.divLines = make([]*canvas.Line, nCols-1)
		for i := range r.divLines {
			ln := canvas.NewLine(color.Transparent)
			ln.StrokeWidth = 1
			r.divLines[i] = ln
		}
	}

	r.scrollTrack = canvas.NewRectangle(color.Transparent)
	r.scrollThumb = canvas.NewRectangle(color.Transparent)

	return r
}

func (r *dtRenderer) Destroy() {}

func (r *dtRenderer) Objects() []fyne.CanvasObject {
	cap := 1 + len(r.hdrTexts) + len(r.divLines) + len(r.rowBgs) + 2
	for _, row := range r.cellTexts {
		cap += len(row)
	}
	objs := make([]fyne.CanvasObject, 0, cap)
	objs = append(objs, r.hdrBg)
	for _, tx := range r.hdrTexts {
		objs = append(objs, tx)
	}
	for _, ln := range r.divLines {
		objs = append(objs, ln)
	}
	for _, bg := range r.rowBgs {
		objs = append(objs, bg)
	}
	for _, row := range r.cellTexts {
		for _, ct := range row {
			objs = append(objs, ct)
		}
	}
	objs = append(objs, r.scrollTrack, r.scrollThumb)
	return objs
}

func (r *dtRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.t.totalWidth(), dtHdrH+dtRowH*3)
}

func (r *dtRenderer) Layout(size fyne.Size) {
	r.size = size
	r.growSlots()
	r.layoutHeader()
	r.layoutBody()
	r.layoutScrollbar()
}

func (r *dtRenderer) Refresh() {
	r.t.initColWidths()
	r.growSlots()
	r.layoutHeader()
	r.layoutBody()
	r.updateCells()
	r.layoutScrollbar()
	canvas.Refresh(r.t)
}

// growSlots adds row-slot objects as the widget grows taller.
// Slots are never removed (only hidden) to avoid object churn.
func (r *dtRenderer) growSlots() {
	bodyH := r.size.Height - dtHdrH
	if bodyH < 0 {
		bodyH = 0
	}
	needed := int(bodyH/dtRowH) + 2
	nCols := len(r.t.Columns)

	for r.slots < needed {
		bg := canvas.NewRectangle(color.Transparent)
		r.rowBgs = append(r.rowBgs, bg)

		row := make([]*canvas.Text, nCols)
		for c := 0; c < nCols; c++ {
			tx := canvas.NewText("", color.White)
			tx.TextSize = theme.TextSize()
			row[c] = tx
		}
		r.cellTexts = append(r.cellTexts, row)
		r.slots++
	}

	// hide any slots that are now beyond the visible area
	for i := needed; i < r.slots; i++ {
		r.rowBgs[i].Hide()
		for _, ct := range r.cellTexts[i] {
			ct.Hide()
		}
	}
}

func (r *dtRenderer) layoutHeader() {
	r.hdrBg.Move(fyne.NewPos(0, 0))
	r.hdrBg.Resize(fyne.NewSize(r.size.Width, dtHdrH))
	r.hdrBg.FillColor = theme.Color(theme.ColorNameHeaderBackground)
	r.hdrBg.Refresh()

	x := float32(0)
	for i, tx := range r.hdrTexts {
		if i >= len(r.t.colWidths) {
			tx.Hide()
			continue
		}
		cw := r.t.colWidths[i]
		tx.Move(fyne.NewPos(x+dtPadX, (dtHdrH-theme.TextSize())/2))
		tx.Color = theme.Color(theme.ColorNameForeground)
		tx.Text = r.t.Columns[i].Header
		tx.Refresh()
		tx.Show()

		if i < len(r.divLines) {
			divX := x + cw - 0.5
			r.divLines[i].Position1 = fyne.NewPos(divX, 4)
			r.divLines[i].Position2 = fyne.NewPos(divX, dtHdrH-4)
			r.divLines[i].StrokeColor = theme.Color(theme.ColorNameSeparator)
			r.divLines[i].Refresh()
			r.divLines[i].Show()
		}
		x += cw
	}
}

func (r *dtRenderer) layoutBody() {
	nData := r.t.rowCount()
	selRow := r.t.selectedRowIndex()
	needed := int((r.size.Height-dtHdrH)/dtRowH) + 2

	y := dtHdrH
	for slot := 0; slot < needed && slot < r.slots; slot++ {
		dataIdx := r.t.scrollOffset + slot
		bg := r.rowBgs[slot]
		bg.Move(fyne.NewPos(0, y))
		bg.Resize(fyne.NewSize(r.size.Width, dtRowH))

		if dataIdx >= nData {
			// empty slot — transparent background, hide cells
			bg.FillColor = color.Transparent
			bg.Refresh()
			bg.Show()
			for _, ct := range r.cellTexts[slot] {
				ct.Hide()
			}
			y += dtRowH
			continue
		}

		switch {
		case dataIdx == selRow:
			bg.FillColor = theme.Color(theme.ColorNameSelection)
		case slot%2 == 0:
			bg.FillColor = theme.Color(theme.ColorNameBackground)
		default:
			bg.FillColor = theme.Color(theme.ColorNameInputBackground)
		}
		bg.Refresh()
		bg.Show()

		// position cell texts
		x := float32(0)
		for col, cw := range r.t.colWidths {
			if col >= len(r.cellTexts[slot]) {
				break
			}
			ct := r.cellTexts[slot][col]
			ct.Move(fyne.NewPos(x+dtPadX, y+(dtRowH-theme.TextSize())/2))
			ct.Show()
			x += cw
		}
		y += dtRowH
	}
}

func (r *dtRenderer) layoutScrollbar() {
	nData := r.t.rowCount()
	bodyH := r.size.Height - dtHdrH
	trackX := r.size.Width - dtScrollW

	// track always visible along the right edge below the header
	r.scrollTrack.Move(fyne.NewPos(trackX, dtHdrH))
	r.scrollTrack.Resize(fyne.NewSize(dtScrollW, bodyH))
	r.scrollTrack.FillColor = theme.Color(theme.ColorNameScrollBar)
	r.scrollTrack.Refresh()

	visRows := int(bodyH / dtRowH)
	if nData <= visRows {
		// all rows fit — hide both track and thumb
		r.scrollTrack.FillColor = color.Transparent
		r.scrollTrack.Refresh()
		r.scrollThumb.FillColor = color.Transparent
		r.scrollThumb.Refresh()
		return
	}

	// thumb height proportional to visible fraction
	ratio := float32(visRows) / float32(nData)
	thumbH := bodyH * ratio
	if thumbH < 20 {
		thumbH = 20
	}

	// thumb position proportional to scroll offset
	scrollable := float32(nData - visRows)
	thumbY := dtHdrH + (float32(r.t.scrollOffset)/scrollable)*(bodyH-thumbH)

	r.scrollThumb.Move(fyne.NewPos(trackX+1, thumbY))
	r.scrollThumb.Resize(fyne.NewSize(dtScrollW-2, thumbH))
	r.scrollThumb.FillColor = theme.Color(theme.ColorNameForeground)
	r.scrollThumb.CornerRadius = (dtScrollW - 2) / 2
	r.scrollThumb.Refresh()
}

func (r *dtRenderer) updateCells() {
	nData := r.t.rowCount()
	needed := int((r.size.Height-dtHdrH)/dtRowH) + 2

	for slot := 0; slot < needed && slot < r.slots; slot++ {
		dataIdx := r.t.scrollOffset + slot
		if dataIdx >= nData {
			for _, ct := range r.cellTexts[slot] {
				ct.Text = ""
				ct.Refresh()
			}
			continue
		}
		for col := range r.cellTexts[slot] {
			ct := r.cellTexts[slot][col]
			if r.t.CellValue != nil {
				raw := r.t.CellValue(dataIdx, col)
				maxW := r.t.colWidths[col] - dtPadX*2
				ct.Text = truncateText(raw, ct.TextSize, ct.TextStyle, maxW)
			}
			ct.Color = r.t.cellColor(dataIdx, col)
			ct.Refresh()
		}
	}
}

// truncateText shortens s with an ellipsis so it fits within maxWidth pixels.
func truncateText(s string, size float32, style fyne.TextStyle, maxWidth float32) string {
	if maxWidth <= 0 {
		return ""
	}
	if fyne.MeasureText(s, size, style).Width <= maxWidth {
		return s
	}
	ellipsis := "..."
	ew := fyne.MeasureText(ellipsis, size, style).Width
	budget := maxWidth - ew
	if budget <= 0 {
		return ellipsis
	}
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fyne.MeasureText(string(runes[:mid]), size, style).Width <= budget {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo]) + ellipsis
}

// ─────────────────────────────────────────────────────────────
// dtInputLayer — transparent interactive overlay
// ─────────────────────────────────────────────────────────────

// dtInputLayer is laid on top of DataTable in a container.NewStack().
// It intercepts Tapped / TappedSecondary, converts the Y coordinate to
// a row index, and delegates to DataTable.  It also owns the colDivider
// child widgets so they participate in Fyne's widget event routing.
type dtInputLayer struct {
	widget.BaseWidget
	t        *DataTable
	dividers []*colDivider
}

func newDTInputLayer(t *DataTable) *dtInputLayer {
	l := &dtInputLayer{t: t}
	l.ExtendBaseWidget(l)
	for i := 0; i < len(t.Columns)-1; i++ {
		l.dividers = append(l.dividers, newColDivider(t, i))
	}
	return l
}

func (l *dtInputLayer) CreateRenderer() fyne.WidgetRenderer {
	objs := make([]fyne.CanvasObject, len(l.dividers))
	for i, d := range l.dividers {
		objs[i] = d
	}
	return &dtInputRenderer{layer: l, objs: objs}
}

// rowAtY converts a Y coordinate (in the layer's local coordinate space)
// to a data-row index.  Returns -1 when Y is in the header or past data.
func (l *dtInputLayer) rowAtY(y float32) int {
	if y < dtHdrH {
		return -1
	}
	slot := int((y - dtHdrH) / dtRowH)
	dataIdx := l.t.scrollOffset + slot
	if dataIdx < 0 || dataIdx >= l.t.rowCount() {
		return -1
	}
	return dataIdx
}

// Scrolled forwards mouse-wheel events to the DataTable so scrolling works
// even though dtInputLayer sits on top in the Stack.
func (l *dtInputLayer) Scrolled(ev *fyne.ScrollEvent) {
	l.t.Scrolled(ev)
}

func (l *dtInputLayer) Tapped(ev *fyne.PointEvent) {
	row := l.rowAtY(ev.Position.Y)
	if row < 0 {
		return
	}
	l.t.tappedRow(row)
}

func (l *dtInputLayer) TappedSecondary(ev *fyne.PointEvent) {
	row := l.rowAtY(ev.Position.Y)
	if row < 0 {
		return
	}
	l.t.secondaryTappedRow(row, ev.AbsolutePosition)
}

// dtInputRenderer lays out the colDivider handles and otherwise does nothing.
type dtInputRenderer struct {
	layer *dtInputLayer
	objs  []fyne.CanvasObject
}

func (r *dtInputRenderer) Destroy()                     {}
func (r *dtInputRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *dtInputRenderer) Objects() []fyne.CanvasObject { return r.objs }

func (r *dtInputRenderer) Layout(size fyne.Size) {
	t := r.layer.t
	x := float32(0)
	for i, d := range r.layer.dividers {
		if i >= len(t.colWidths) {
			break
		}
		x += t.colWidths[i]
		d.Move(fyne.NewPos(x-dtDivW/2, 0))
		d.Resize(fyne.NewSize(dtDivW, size.Height))
	}
}

func (r *dtInputRenderer) Refresh() {
	r.Layout(r.layer.Size())
	canvas.Refresh(r.layer)
}

// ─────────────────────────────────────────────────────────────
// colDivider — draggable column-resize handle
// ─────────────────────────────────────────────────────────────

type colDivider struct {
	widget.BaseWidget
	t   *DataTable
	col int // index of the column to the left of this divider
}

func newColDivider(t *DataTable, col int) *colDivider {
	d := &colDivider{t: t, col: col}
	d.ExtendBaseWidget(d)
	return d
}

func (d *colDivider) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// Cursor changes the mouse pointer to a horizontal-resize arrow when
// hovering over this handle.
func (d *colDivider) Cursor() desktop.Cursor { return desktop.HResizeCursor }

func (d *colDivider) MouseIn(_ *desktop.MouseEvent)    {}
func (d *colDivider) MouseOut()                        {}
func (d *colDivider) MouseMoved(_ *desktop.MouseEvent) {}

func (d *colDivider) Dragged(ev *fyne.DragEvent) {
	if d.col >= len(d.t.colWidths) {
		return
	}
	newW := d.t.colWidths[d.col] + ev.Dragged.DX
	if newW < dtMinColW {
		newW = dtMinColW
	}
	d.t.colWidths[d.col] = newW
	d.t.Refresh()
}

func (d *colDivider) DragEnd() {}
