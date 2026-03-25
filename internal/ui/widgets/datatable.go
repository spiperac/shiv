package widgets

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// DataTableColumn describes a single column header and its initial width.
type DataTableColumn struct {
	Header string
	Width  float32
}

// ContextMenuItem describes a single item in a right-click context menu.
type ContextMenuItem struct {
	Label  string
	Action func()
}

// Layout constants for DataTable. All sizes are in logical pixels.
const (
	dtRowH    float32 = 32 // height of a data row
	dtHdrH    float32 = 34 // height of the header row
	dtPadX    float32 = 8  // horizontal cell padding
	dtDivW    float32 = 6  // width of a column-divider hit area
	dtScrollW float32 = 8  // width of the scrollbar track
)

// DataTable is a virtualised, row-selectable table widget with resizable
// columns, right-click context menus, and a draggable scrollbar thumb.
//
// Create with NewDataTable, set all callback fields, call SetWindow, then
// use the object returned by Build in your layout.
type DataTable struct {
	widget.BaseWidget

	// Columns defines the header labels and initial widths.
	Columns []DataTableColumn

	// Data callbacks — may be updated at runtime; call Refresh after changes.
	RowCount  func() int
	CellValue func(row, col int) string
	CellStyle func(row, col int) widget.Importance // nil → MediumImportance
	RowID     func(row int) int64                  // stable unique ID per row

	// Interaction callbacks.
	OnSelect  func(row int)
	MenuItems func(row int) []ContextMenuItem // nil → no context menu

	colWidths    []float32
	selectedID   int64
	hasSelected  bool
	scrollOffset int
	scrollFrac   float32 // sub-row accumulator for smooth trackpad scrolling

	win        fyne.Window
	rend       *dtRenderer
	inputLayer *dtInputLayer
}

// NewDataTable allocates and extends a DataTable.
func NewDataTable() *DataTable {
	t := &DataTable{}
	t.ExtendBaseWidget(t)
	return t
}

// SetWindow provides the window reference required for context menu anchoring.
// Must be called before the widget is shown.
func (t *DataTable) SetWindow(w fyne.Window) { t.win = w }

// Build constructs the complete canvas object to place in a layout.
// Call once after all callbacks are configured.
func (t *DataTable) Build() fyne.CanvasObject {
	t.initColWidths()
	layer := newDTInputLayer(t)
	t.inputLayer = layer
	return container.NewStack(t, layer)
}

// SetSelected highlights the row identified by id. Pass 0 to clear.
func (t *DataTable) SetSelected(id int64) {
	t.selectedID = id
	t.hasSelected = id != 0
	t.Refresh()
}

// ClearSelection removes the current row highlight.
func (t *DataTable) ClearSelection() {
	t.selectedID = 0
	t.hasSelected = false
	t.Refresh()
}

// ScrollToRow scrolls the viewport so that row is visible.
func (t *DataTable) ScrollToRow(row int) {
	visible := t.visibleSlots()
	if row < t.scrollOffset {
		t.scrollOffset = row
	} else if row >= t.scrollOffset+visible {
		t.scrollOffset = row - visible + 1
	}
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.Refresh()
}

// Scrolled implements fyne.Scrollable, forwarding wheel events from the
// input layer overlay.
func (t *DataTable) Scrolled(ev *fyne.ScrollEvent) {
	totalRows := t.rowCount()
	visible := t.visibleSlots()
	maxOffset := totalRows - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	t.scrollFrac -= ev.Scrolled.DY / dtRowH
	rows := int(t.scrollFrac)
	t.scrollFrac -= float32(rows)
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
	if t.scrollOffset > maxOffset {
		t.scrollOffset = maxOffset
	}
	t.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (t *DataTable) CreateRenderer() fyne.WidgetRenderer {
	t.initColWidths()
	renderer := newDTRenderer(t)
	t.rend = renderer
	return renderer
}

func (t *DataTable) initColWidths() {
	if len(t.colWidths) == len(t.Columns) {
		return
	}
	t.colWidths = make([]float32, len(t.Columns))
	for i, col := range t.Columns {
		t.colWidths[i] = col.Width
	}
}

func (t *DataTable) rowCount() int {
	if t.RowCount == nil {
		return 0
	}
	return t.RowCount()
}

func (t *DataTable) visibleSlots() int {
	if t.rend == nil {
		return 1
	}
	availHeight := t.rend.size.Height - dtHdrH
	if availHeight < 0 {
		return 0
	}
	return int(availHeight/dtRowH) + 2
}

// cellColor maps a widget.Importance value to a theme colour.
func (t *DataTable) cellColor(row, col int) color.Color {
	importance := widget.MediumImportance
	if t.CellStyle != nil {
		importance = t.CellStyle(row, col)
	}
	switch importance {
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

// minColWidth returns the minimum display width for col, measured from its
// header text so the header is always fully readable.
func (t *DataTable) minColWidth(col int) float32 {
	if col >= len(t.Columns) {
		return dtPadX * 2
	}
	textWidth := fyne.MeasureText(t.Columns[col].Header, theme.TextSize(), fyne.TextStyle{Bold: true}).Width
	return textWidth + dtPadX*2
}

// thumbBounds returns the scrollbar thumb position and height in widget-local
// coordinates. ok is false when all rows fit in the viewport.
func (t *DataTable) thumbBounds() (thumbY, thumbH float32, ok bool) {
	if t.rend == nil {
		return 0, 0, false
	}
	totalRows := t.rowCount()
	bodyHeight := t.rend.size.Height - dtHdrH
	visibleRows := int(bodyHeight / dtRowH)
	scrollable := totalRows - visibleRows
	if scrollable <= 0 || bodyHeight <= 0 {
		return 0, 0, false
	}
	ratio := float32(visibleRows) / float32(totalRows)
	thumbH = bodyHeight * ratio
	if thumbH < 20 {
		thumbH = 20
	}
	trackHeight := bodyHeight - thumbH
	thumbY = dtHdrH + (float32(t.scrollOffset)/float32(scrollable))*trackHeight
	return thumbY, thumbH, true
}

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
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", menuItems...), t.win.Canvas(), absPos)
}

// dtRenderer draws the table using canvas primitives. Only visible row slots
// are allocated; slots grow as the widget height increases but are never freed.
type dtRenderer struct {
	table *DataTable
	size  fyne.Size

	headerBg     *canvas.Rectangle
	headerTexts  []*canvas.Text
	dividerLines []*canvas.Line

	slots     int
	rowBgs    []*canvas.Rectangle
	cellTexts [][]*canvas.Text

	scrollTrack *canvas.Rectangle
	scrollThumb *canvas.Rectangle

	objs []fyne.CanvasObject // cached; rebuilt only when slots grow
}

func newDTRenderer(table *DataTable) *dtRenderer {
	nCols := len(table.Columns)
	r := &dtRenderer{table: table}
	r.headerBg = canvas.NewRectangle(color.Transparent)

	r.headerTexts = make([]*canvas.Text, nCols)
	for i, col := range table.Columns {
		headerText := canvas.NewText(col.Header, color.Transparent)
		headerText.TextStyle = fyne.TextStyle{Bold: true}
		headerText.TextSize = theme.TextSize()
		r.headerTexts[i] = headerText
	}

	if nCols > 1 {
		r.dividerLines = make([]*canvas.Line, nCols-1)
		for i := range r.dividerLines {
			dividerLine := canvas.NewLine(color.Transparent)
			dividerLine.StrokeWidth = 1
			r.dividerLines[i] = dividerLine
		}
	}

	r.scrollTrack = canvas.NewRectangle(color.Transparent)
	r.scrollThumb = canvas.NewRectangle(color.Transparent)
	r.rebuildObjects()
	return r
}

func (r *dtRenderer) Destroy() {}

func (r *dtRenderer) Objects() []fyne.CanvasObject { return r.objs }

func (r *dtRenderer) MinSize() fyne.Size {
	return fyne.NewSize(100, dtHdrH+dtRowH*3)
}

func (r *dtRenderer) Layout(size fyne.Size) {
	r.size = size
	r.growSlots()
	r.layoutHeader()
	r.layoutBody()
	r.layoutScrollbar()
}

func (r *dtRenderer) Refresh() {
	r.table.initColWidths()
	r.growSlots()
	r.layoutHeader()
	r.layoutBody()
	r.updateCells()
	r.layoutScrollbar()
	canvas.Refresh(r.table)
}

// rebuildObjects reconstructs the cached object slice after slots grow.
func (r *dtRenderer) rebuildObjects() {
	nFixed := 1 + len(r.headerTexts) + len(r.dividerLines) + 2
	total := nFixed + len(r.rowBgs)
	for _, row := range r.cellTexts {
		total += len(row)
	}
	objs := make([]fyne.CanvasObject, 0, total)
	objs = append(objs, r.headerBg)
	for _, headerText := range r.headerTexts {
		objs = append(objs, headerText)
	}
	for _, dividerLine := range r.dividerLines {
		objs = append(objs, dividerLine)
	}
	for _, rowBg := range r.rowBgs {
		objs = append(objs, rowBg)
	}
	for _, row := range r.cellTexts {
		for _, cellText := range row {
			objs = append(objs, cellText)
		}
	}
	objs = append(objs, r.scrollTrack, r.scrollThumb)
	r.objs = objs
}

func (r *dtRenderer) growSlots() {
	bodyHeight := r.size.Height - dtHdrH
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	needed := int(bodyHeight/dtRowH) + 2
	nCols := len(r.table.Columns)

	grew := false
	for r.slots < needed {
		rowBg := canvas.NewRectangle(color.Transparent)
		r.rowBgs = append(r.rowBgs, rowBg)
		cellRow := make([]*canvas.Text, nCols)
		for col := 0; col < nCols; col++ {
			cellText := canvas.NewText("", color.Transparent)
			cellText.TextSize = theme.TextSize()
			cellRow[col] = cellText
		}
		r.cellTexts = append(r.cellTexts, cellRow)
		r.slots++
		grew = true
	}
	if grew {
		r.rebuildObjects()
	}

	for slot := needed; slot < r.slots; slot++ {
		r.rowBgs[slot].Hide()
		for _, cellText := range r.cellTexts[slot] {
			cellText.Hide()
		}
	}
}

func (r *dtRenderer) layoutHeader() {
	r.headerBg.Move(fyne.NewPos(0, 0))
	r.headerBg.Resize(fyne.NewSize(r.size.Width, dtHdrH))
	r.headerBg.FillColor = theme.Color(theme.ColorNameHeaderBackground)
	r.headerBg.Refresh()

	xPos := float32(0)
	for i, headerText := range r.headerTexts {
		if i >= len(r.table.colWidths) {
			headerText.Hide()
			continue
		}
		colWidth := r.table.colWidths[i]
		headerText.Move(fyne.NewPos(xPos+dtPadX, (dtHdrH-theme.TextSize())/2))
		headerText.Color = theme.Color(theme.ColorNameForeground)
		headerText.Text = r.table.Columns[i].Header
		headerText.Refresh()
		headerText.Show()

		if i < len(r.dividerLines) {
			divX := xPos + colWidth - 0.5
			r.dividerLines[i].Position1 = fyne.NewPos(divX, 4)
			r.dividerLines[i].Position2 = fyne.NewPos(divX, dtHdrH-4)
			r.dividerLines[i].StrokeColor = theme.Color(theme.ColorNameSeparator)
			r.dividerLines[i].Refresh()
			r.dividerLines[i].Show()
		}
		xPos += colWidth
	}
}

func (r *dtRenderer) layoutBody() {
	totalRows := r.table.rowCount()
	needed := int((r.size.Height-dtHdrH)/dtRowH) + 2

	yPos := dtHdrH
	for slot := 0; slot < needed && slot < r.slots; slot++ {
		dataIdx := r.table.scrollOffset + slot
		rowBg := r.rowBgs[slot]
		rowBg.Move(fyne.NewPos(0, yPos))
		rowBg.Resize(fyne.NewSize(r.size.Width, dtRowH))

		if dataIdx >= totalRows {
			rowBg.FillColor = color.Transparent
			rowBg.Refresh()
			rowBg.Show()
			for _, cellText := range r.cellTexts[slot] {
				cellText.Hide()
			}
			yPos += dtRowH
			continue
		}

		switch {
		case r.table.hasSelected && r.table.RowID != nil && r.table.RowID(dataIdx) == r.table.selectedID:
			rowBg.FillColor = theme.Color(theme.ColorNameSelection)
		case slot%2 == 0:
			rowBg.FillColor = theme.Color(theme.ColorNameBackground)
		default:
			rowBg.FillColor = theme.Color(theme.ColorNameInputBackground)
		}
		rowBg.Refresh()
		rowBg.Show()

		xPos := float32(0)
		for col, colWidth := range r.table.colWidths {
			if col >= len(r.cellTexts[slot]) {
				break
			}
			cellText := r.cellTexts[slot][col]
			cellText.Move(fyne.NewPos(xPos+dtPadX, yPos+(dtRowH-theme.TextSize())/2))
			cellText.Show()
			xPos += colWidth
		}
		yPos += dtRowH
	}
}

func (r *dtRenderer) layoutScrollbar() {
	totalRows := r.table.rowCount()
	bodyHeight := r.size.Height - dtHdrH
	trackX := r.size.Width - dtScrollW

	r.scrollTrack.Move(fyne.NewPos(trackX, dtHdrH))
	r.scrollTrack.Resize(fyne.NewSize(dtScrollW, bodyHeight))

	visibleRows := int(bodyHeight / dtRowH)
	if totalRows <= visibleRows {
		r.scrollTrack.FillColor = color.Transparent
		r.scrollTrack.Refresh()
		r.scrollThumb.FillColor = color.Transparent
		r.scrollThumb.Refresh()
		return
	}

	r.scrollTrack.FillColor = theme.Color(theme.ColorNameScrollBar)
	r.scrollTrack.Refresh()

	thumbY, thumbH, ok := r.table.thumbBounds()
	if !ok {
		r.scrollThumb.FillColor = color.Transparent
		r.scrollThumb.Refresh()
		return
	}

	r.scrollThumb.Move(fyne.NewPos(trackX+1, thumbY))
	r.scrollThumb.Resize(fyne.NewSize(dtScrollW-2, thumbH))
	r.scrollThumb.FillColor = theme.Color(theme.ColorNameForeground)
	r.scrollThumb.CornerRadius = (dtScrollW - 2) / 2
	r.scrollThumb.Refresh()
}

func (r *dtRenderer) updateCells() {
	totalRows := r.table.rowCount()
	needed := int((r.size.Height-dtHdrH)/dtRowH) + 2

	for slot := 0; slot < needed && slot < r.slots; slot++ {
		dataIdx := r.table.scrollOffset + slot
		if dataIdx >= totalRows {
			for _, cellText := range r.cellTexts[slot] {
				cellText.Text = ""
				cellText.Refresh()
			}
			continue
		}
		for col, cellText := range r.cellTexts[slot] {
			if col >= len(r.table.colWidths) {
				cellText.Text = ""
				cellText.Refresh()
				continue
			}
			if r.table.CellValue != nil {
				cellText.Text = truncateText(r.table.CellValue(dataIdx, col), cellText.TextSize, cellText.TextStyle, r.table.colWidths[col]-dtPadX*2)
			}
			cellText.Color = r.table.cellColor(dataIdx, col)
			cellText.Refresh()
		}
	}
}

// truncateText shortens s to fit within maxWidth pixels, appending an ellipsis.
func truncateText(s string, size float32, style fyne.TextStyle, maxWidth float32) string {
	if maxWidth <= 0 {
		return ""
	}
	if fyne.MeasureText(s, size, style).Width <= maxWidth {
		return s
	}
	const ellipsis = "..."
	budget := maxWidth - fyne.MeasureText(ellipsis, size, style).Width
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

// dtInputLayer is a transparent widget overlaid on DataTable via
// container.NewStack. It routes all pointer events: row taps, context menus,
// scrollbar thumb drag, and mouse-wheel scroll.
type dtInputLayer struct {
	widget.BaseWidget
	table         *DataTable
	dividers      []*colDivider
	draggingThumb bool
}

func newDTInputLayer(table *DataTable) *dtInputLayer {
	layer := &dtInputLayer{table: table}
	layer.ExtendBaseWidget(layer)
	for i := 0; i < len(table.Columns)-1; i++ {
		layer.dividers = append(layer.dividers, newColDivider(table, i))
	}
	return layer
}

func (layer *dtInputLayer) CreateRenderer() fyne.WidgetRenderer {
	objs := make([]fyne.CanvasObject, len(layer.dividers))
	for i, divider := range layer.dividers {
		objs[i] = divider
	}
	return &dtInputRenderer{layer: layer, objs: objs}
}

func (layer *dtInputLayer) inScrollbarArea(xPos float32) bool {
	if layer.table.rend == nil {
		return false
	}
	return xPos >= layer.table.rend.size.Width-dtScrollW
}

func (layer *dtInputLayer) inThumb(xPos, yPos float32) bool {
	if !layer.inScrollbarArea(xPos) {
		return false
	}
	thumbY, thumbH, ok := layer.table.thumbBounds()
	if !ok {
		return false
	}
	return yPos >= thumbY && yPos <= thumbY+thumbH
}

func (layer *dtInputLayer) rowAtY(yPos float32) int {
	if yPos < dtHdrH {
		return -1
	}
	dataIdx := layer.table.scrollOffset + int((yPos-dtHdrH)/dtRowH)
	if dataIdx < 0 || dataIdx >= layer.table.rowCount() {
		return -1
	}
	return dataIdx
}

func (layer *dtInputLayer) Scrolled(ev *fyne.ScrollEvent) { layer.table.Scrolled(ev) }

func (layer *dtInputLayer) Tapped(ev *fyne.PointEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	if row := layer.rowAtY(ev.Position.Y); row >= 0 {
		layer.table.tappedRow(row)
	}
}

func (layer *dtInputLayer) TappedSecondary(ev *fyne.PointEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	if row := layer.rowAtY(ev.Position.Y); row >= 0 {
		layer.table.secondaryTappedRow(row, ev.AbsolutePosition)
	}
}

func (layer *dtInputLayer) MouseDown(ev *desktop.MouseEvent) {
	layer.draggingThumb = layer.inThumb(ev.Position.X, ev.Position.Y)
}

func (layer *dtInputLayer) MouseUp(_ *desktop.MouseEvent) { layer.draggingThumb = false }

func (layer *dtInputLayer) Dragged(ev *fyne.DragEvent) {
	if !layer.draggingThumb {
		return
	}
	table := layer.table
	if table.rend == nil {
		return
	}
	widgetPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(table)
	if ev.AbsolutePosition.Y < widgetPos.Y || ev.AbsolutePosition.Y > widgetPos.Y+table.Size().Height {
		return
	}
	_, thumbH, ok := table.thumbBounds()
	if !ok {
		return
	}
	bodyHeight := table.rend.size.Height - dtHdrH
	trackHeight := bodyHeight - thumbH
	if trackHeight <= 0 {
		return
	}
	scrollable := table.rowCount() - int(bodyHeight/dtRowH)
	if scrollable <= 0 {
		return
	}
	thumbTop := ev.Position.Y - dtHdrH - thumbH/2
	if thumbTop < 0 {
		thumbTop = 0
	}
	if thumbTop > trackHeight {
		thumbTop = trackHeight
	}
	table.scrollOffset = int((thumbTop / trackHeight) * float32(scrollable))
	table.Refresh()
}

func (layer *dtInputLayer) DragEnd()                         { layer.draggingThumb = false }
func (layer *dtInputLayer) MouseIn(_ *desktop.MouseEvent)    {}
func (layer *dtInputLayer) MouseOut()                        {}
func (layer *dtInputLayer) MouseMoved(_ *desktop.MouseEvent) {}

// dtInputRenderer positions column-divider widgets within the input layer.
type dtInputRenderer struct {
	layer *dtInputLayer
	objs  []fyne.CanvasObject
}

func (r *dtInputRenderer) Destroy()                     {}
func (r *dtInputRenderer) MinSize() fyne.Size           { return fyne.NewSize(0, 0) }
func (r *dtInputRenderer) Objects() []fyne.CanvasObject { return r.objs }

func (r *dtInputRenderer) Layout(size fyne.Size) {
	table := r.layer.table
	xPos := float32(0)
	for i, divider := range r.layer.dividers {
		if i >= len(table.colWidths) {
			break
		}
		xPos += table.colWidths[i]
		divider.Move(fyne.NewPos(xPos-dtDivW/2, 0))
		divider.Resize(fyne.NewSize(dtDivW, size.Height))
	}
}

func (r *dtInputRenderer) Refresh() {
	r.Layout(r.layer.Size())
	canvas.Refresh(r.layer)
}

// colDivider is a transparent widget positioned at each column boundary.
// It presents an H-resize cursor and handles column-resize drag events.
type colDivider struct {
	widget.BaseWidget
	table *DataTable
	col   int // index of the column to the left of this divider
}

func newColDivider(table *DataTable, col int) *colDivider {
	divider := &colDivider{table: table, col: col}
	divider.ExtendBaseWidget(divider)
	return divider
}

func (d *colDivider) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (d *colDivider) Cursor() desktop.Cursor           { return desktop.HResizeCursor }
func (d *colDivider) MouseIn(_ *desktop.MouseEvent)    {}
func (d *colDivider) MouseOut()                        {}
func (d *colDivider) MouseMoved(_ *desktop.MouseEvent) {}

// Dragged resizes the column to the left of this divider. Dragging right
// first consumes spare space to the right of all columns, then shrinks the
// right neighbour. Dragging left shrinks this column and widens the right
// neighbour. Neither column can go below its header-text minimum width.
func (d *colDivider) Dragged(ev *fyne.DragEvent) {
	if d.col >= len(d.table.colWidths) {
		return
	}

	delta := ev.Dragged.DX
	leftWidth := d.table.colWidths[d.col]
	leftMin := d.table.minColWidth(d.col)

	if leftWidth+delta < leftMin {
		delta = leftMin - leftWidth
	}
	if delta == 0 {
		return
	}

	if d.col+1 < len(d.table.colWidths) {
		rightWidth := d.table.colWidths[d.col+1]
		rightMin := d.table.minColWidth(d.col + 1)

		if delta > 0 {
			spareSpace := float32(0)
			if d.table.rend != nil {
				totalWidth := float32(0)
				for _, colWidth := range d.table.colWidths {
					totalWidth += colWidth
				}
				spareSpace = d.table.rend.size.Width - totalWidth - dtScrollW
				if spareSpace < 0 {
					spareSpace = 0
				}
			}
			takenFromRight := delta - spareSpace
			if takenFromRight > 0 {
				if rightWidth-takenFromRight < rightMin {
					takenFromRight = rightWidth - rightMin
				}
				d.table.colWidths[d.col+1] -= takenFromRight
				delta = spareSpace + takenFromRight
			}
		} else {
			d.table.colWidths[d.col+1] -= delta
		}
	}

	d.table.colWidths[d.col] = leftWidth + delta
	d.table.Refresh()
	if d.table.inputLayer != nil {
		d.table.inputLayer.Refresh()
	}
}

func (d *colDivider) DragEnd() {}
