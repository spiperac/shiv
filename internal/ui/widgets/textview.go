package widgets

// TextView is a virtualised, syntax-highlighted, selectable read-only text
// widget for Fyne.
//
// Architecture
//
// Fyne's widget.Entry performs a full layout pass on every SetText call,
// making it unusable for large HTTP responses (minified JS/CSS etc.).
// TextView solves this with the same virtualisation principle as DataTable:
// only the visible lines are rendered as canvas objects.
//
// Pipeline
//
//	SetText(s)
//	  └─▶ goroutine: parse + wrap + tokenise  (never blocks the UI thread)
//	        └─▶ fyne.Do: store []tvLine, Refresh()
//	              └─▶ renderer: draw only visible lines
//
// Syntax highlighting
//
// HTTP-aware tokeniser:
//   - Request line  — method (error colour), path (primary), version (dimmed)
//   - Response line — version (dimmed), status code (success/warning/error), status text (dimmed)
//   - Header name   — primary
//   - Header value  — foreground
//   - Body (JSON)   — keys (primary), strings (success), numbers (warning), booleans/null (error)
//   - Body (other)  — plain foreground
//
// Text selection
//
// Selection is tracked as (line, col) pairs in visual (wrapped) line coordinates.
// Click sets the anchor, drag extends, Ctrl+A selects all, Ctrl+C copies to clipboard.
//
// Scrolling
//
// Vertical only. Mouse wheel and scrollbar thumb drag both work.

import (
	"image/color"
	"math"
	"strings"
	"sync"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Layout constants for TextView. All sizes are in logical pixels.
const (
	tvLineH            float32 = 20 // height of one text line
	tvPadX             float32 = 8  // left and right margin
	tvPadY             float32 = 4  // top and bottom margin
	tvScrollW          float32 = 8  // scrollbar track width
	tvScrollMinThumb   float32 = 20 // minimum scrollbar thumb height
	tvMaxTokensPerLine         = 64 // canvas text objects allocated per visible line
)

// TextView is the public widget. Use newTextView to create it.
// Call SetText to load content; parsing never blocks the UI thread.
// Use Build to obtain the canvas object to place in a layout.
type TextView struct {
	widget.BaseWidget

	mu      sync.RWMutex
	lines   []tvLine // processed visual lines, populated by background parser
	rawFull string   // original unprocessed text, used for select-all copy

	scrollOffset int     // index of the first visible line
	scrollFrac   float32 // sub-line accumulator for smooth trackpad scrolling

	// Selection is tracked as anchor + cursor (line, col) pairs in visual-line
	// coordinates. Anchor is set on mouse down; cursor moves during drag.
	selAnchorLine int
	selAnchorCol  int
	selLine       int
	selCol        int
	hasSelection  bool

	win  fyne.Window
	rend *tvRenderer

	pendingThumb  *tvScrollThumb // created in Build before rend exists; wired in CreateRenderer
	lastWrapWidth float32        // wrap width used for the last parse pass
	pendingText   string         // text waiting to be parsed once width is known
	processing    bool           // true while the background parse goroutine is running
	gen           int64
}

func NewTextView() *TextView {
	view := &TextView{}
	view.ExtendBaseWidget(view)
	return view
}

// SetWindow provides the window reference required for clipboard access.
// Must be called before the widget is shown.
func (view *TextView) SetWindow(w fyne.Window) { view.win = w }

// SetText replaces the displayed content. Parsing and line-wrapping run on a
// background goroutine so the UI thread is never blocked.
func (view *TextView) SetText(s string) {
	view.mu.Lock()
	view.pendingText = s
	view.rawFull = s
	view.scrollOffset = 0
	view.scrollFrac = 0
	view.hasSelection = false
	alreadyProcessing := view.processing
	currentWidth := view.lastWrapWidth
	view.mu.Unlock()

	if alreadyProcessing || currentWidth < 1 {
		return
	}
	view.startProcessing(s, currentWidth)
}

// SetPlaceHolder is a no-op retained for API compatibility.
func (view *TextView) SetPlaceHolder(_ string) {}

// GetText returns the current raw text content.
func (view *TextView) GetText() string {
	view.mu.RLock()
	defer view.mu.RUnlock()
	return view.rawFull
}

func (view *TextView) startProcessing(s string, wrapWidth float32) {
	view.mu.Lock()
	view.gen++
	currentGen := view.gen
	view.processing = true
	view.mu.Unlock()

	go func(gen int64) {
		parsedLines := parseAndWrap(s, wrapWidth)
		fyne.Do(func() {
			view.mu.Lock()
			if gen != view.gen {
				view.mu.Unlock()
				return
			}
			view.lines = parsedLines
			view.processing = false
			pending := ""
			if view.pendingText != s {
				pending = view.pendingText
			}
			latestWidth := view.lastWrapWidth
			view.mu.Unlock()

			if pending != "" && latestWidth > 0 {
				view.startProcessing(pending, latestWidth)
			}
			view.Refresh()
		})
	}(currentGen)
}

// CreateRenderer implements fyne.Widget.
func (view *TextView) CreateRenderer() fyne.WidgetRenderer {
	renderer := newTVRenderer(view)
	view.rend = renderer
	if view.pendingThumb != nil {
		renderer.thumbWidget = view.pendingThumb
		view.pendingThumb = nil
	}
	return renderer
}

// Scrolled implements fyne.Scrollable.
func (view *TextView) Scrolled(ev *fyne.ScrollEvent) {
	view.mu.RLock()
	totalLines := len(view.lines)
	view.mu.RUnlock()

	visibleLines := view.visibleLineCount()
	maxOffset := totalLines - visibleLines
	if maxOffset < 0 {
		maxOffset = 0
	}

	view.scrollFrac -= ev.Scrolled.DY / tvLineH
	rows := int(view.scrollFrac)
	view.scrollFrac -= float32(rows)
	if rows == 0 {
		if ev.Scrolled.DY < 0 {
			rows = 1
		} else if ev.Scrolled.DY > 0 {
			rows = -1
		}
	}
	view.scrollOffset += rows
	if view.scrollOffset < 0 {
		view.scrollOffset = 0
	}
	if view.scrollOffset > maxOffset {
		view.scrollOffset = maxOffset
	}
	view.Refresh()
}

func (view *TextView) visibleLineCount() int {
	if view.rend == nil {
		return 0
	}
	availHeight := view.rend.size.Height - tvPadY*2
	if availHeight < 0 {
		return 0
	}
	return int(availHeight / tvLineH)
}

// posFromPoint converts a canvas-local position to a (line, col) pair in
// visual-line coordinates.
func (view *TextView) posFromPoint(point fyne.Position) (line, col int) {
	view.mu.RLock()
	totalLines := len(view.lines)
	view.mu.RUnlock()

	lineIdx := view.scrollOffset + int((point.Y-tvPadY)/tvLineH)
	if lineIdx < 0 {
		lineIdx = 0
	}
	if lineIdx >= totalLines {
		lineIdx = totalLines - 1
	}
	if lineIdx < 0 {
		return 0, 0
	}

	view.mu.RLock()
	rawLine := ""
	if lineIdx < len(view.lines) {
		rawLine = view.lines[lineIdx].Raw
	}
	view.mu.RUnlock()

	runes := []rune(rawLine)
	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}
	col = int((point.X - tvPadX) / charWidth)
	if col < 0 {
		col = 0
	}
	if col > len(runes) {
		col = len(runes)
	}
	return lineIdx, col
}

// selectedText returns the currently selected plain text.
func (view *TextView) selectedText() string {
	if !view.hasSelection {
		return ""
	}
	view.mu.RLock()
	defer view.mu.RUnlock()

	startLine, startCol, endLine, endCol := view.normaliseSelection()
	if startLine == endLine {
		runes := []rune(view.lines[startLine].Raw)
		if startCol > len(runes) {
			startCol = len(runes)
		}
		if endCol > len(runes) {
			endCol = len(runes)
		}
		return string(runes[startCol:endCol])
	}

	var builder strings.Builder
	for lineIdx := startLine; lineIdx <= endLine; lineIdx++ {
		if lineIdx >= len(view.lines) {
			break
		}
		runes := []rune(view.lines[lineIdx].Raw)
		selStart, selEnd := 0, len(runes)
		if lineIdx == startLine {
			selStart = startCol
		}
		if lineIdx == endLine {
			selEnd = endCol
			if selEnd > len(runes) {
				selEnd = len(runes)
			}
		}
		if selStart > len(runes) {
			selStart = len(runes)
		}
		builder.WriteString(string(runes[selStart:selEnd]))
		if lineIdx < endLine {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

// normaliseSelection returns (startLine, startCol, endLine, endCol) in reading order.
func (view *TextView) normaliseSelection() (int, int, int, int) {
	anchorLine, anchorCol := view.selAnchorLine, view.selAnchorCol
	cursorLine, cursorCol := view.selLine, view.selCol
	if anchorLine > cursorLine || (anchorLine == cursorLine && anchorCol > cursorCol) {
		return cursorLine, cursorCol, anchorLine, anchorCol
	}
	return anchorLine, anchorCol, cursorLine, cursorCol
}

func (view *TextView) TypedShortcut(s fyne.Shortcut) {
	switch s.(type) {
	case *fyne.ShortcutCopy:
		if view.win != nil && view.hasSelection {
			fyne.CurrentApp().Clipboard().SetContent(view.selectedText())
		}
	case *fyne.ShortcutSelectAll:
		view.mu.RLock()
		totalLines := len(view.lines)
		lastRaw := ""
		if totalLines > 0 {
			lastRaw = view.lines[totalLines-1].Raw
		}
		view.mu.RUnlock()
		view.selAnchorLine, view.selAnchorCol = 0, 0
		view.selLine = max(totalLines-1, 0)
		view.selCol = len([]rune(lastRaw))
		view.hasSelection = true
		view.Refresh()
	}
}

func (view *TextView) TypedKey(_ *fyne.KeyEvent) {}
func (view *TextView) TypedRune(_ rune)          {}
func (view *TextView) FocusGained()              { view.Refresh() }
func (view *TextView) FocusLost() {
	view.hasSelection = false
	view.Refresh()
}

// Build constructs the complete canvas object to place in a layout.
// Call once after all callbacks are configured.
func (view *TextView) Build() fyne.CanvasObject {
	thumb := newTVScrollThumb(view)
	view.pendingThumb = thumb
	return container.NewStack(view, newTVInputLayer(view), thumb)
}

// tvInputLayer is a transparent widget overlaid on TextView via
// container.NewStack. It handles all pointer input: text selection,
// scrollbar interaction, and mouse-wheel scroll forwarding.
type tvInputLayer struct {
	widget.BaseWidget
	view *TextView
}

func newTVInputLayer(view *TextView) *tvInputLayer {
	layer := &tvInputLayer{view: view}
	layer.ExtendBaseWidget(layer)
	return layer
}

func (layer *tvInputLayer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (layer *tvInputLayer) Scrolled(ev *fyne.ScrollEvent) { layer.view.Scrolled(ev) }

func (layer *tvInputLayer) inScrollbarArea(xPos float32) bool {
	if layer.view.rend == nil {
		return false
	}
	return xPos >= layer.view.rend.size.Width-tvScrollW
}

// MouseDown sets the selection anchor on button press so drag always starts
// from the correct position.
func (layer *tvInputLayer) MouseDown(ev *desktop.MouseEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	view := layer.view
	line, col := view.posFromPoint(ev.Position)
	view.selAnchorLine, view.selAnchorCol = line, col
	view.selLine, view.selCol = line, col
	view.hasSelection = false
	view.Refresh()
	if view.win != nil {
		view.win.Canvas().Focus(view)
	}
}

func (layer *tvInputLayer) MouseUp(_ *desktop.MouseEvent) {}

// Tapped satisfies fyne.Tappable; selection state is managed in MouseDown.
func (layer *tvInputLayer) Tapped(_ *fyne.PointEvent) {}

func (layer *tvInputLayer) DoubleTapped(ev *fyne.PointEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	view := layer.view
	line, col := view.posFromPoint(ev.Position)
	view.mu.RLock()
	rawLine := ""
	if line < len(view.lines) {
		rawLine = view.lines[line].Raw
	}
	view.mu.RUnlock()

	runes := []rune(rawLine)
	if len(runes) == 0 {
		return
	}
	if col >= len(runes) {
		col = len(runes) - 1
	}

	wordStart := col
	for wordStart > 0 && isWordRune(runes[wordStart-1]) {
		wordStart--
	}
	wordEnd := col
	for wordEnd < len(runes) && isWordRune(runes[wordEnd]) {
		wordEnd++
	}
	view.selAnchorLine, view.selAnchorCol = line, wordStart
	view.selLine, view.selCol = line, wordEnd
	view.hasSelection = wordStart != wordEnd
	view.Refresh()
}

func (layer *tvInputLayer) Dragged(ev *fyne.DragEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	view := layer.view
	line, col := view.posFromPoint(ev.Position)
	view.selLine, view.selCol = line, col
	view.hasSelection = !(view.selAnchorLine == view.selLine && view.selAnchorCol == view.selCol)
	view.Refresh()
}

func (layer *tvInputLayer) DragEnd()                         {}
func (layer *tvInputLayer) MouseIn(_ *desktop.MouseEvent)    {}
func (layer *tvInputLayer) MouseOut()                        {}
func (layer *tvInputLayer) MouseMoved(_ *desktop.MouseEvent) {}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}

// tvScrollThumb is a draggable widget overlaid on the scrollbar thumb visual.
// It receives drag events and translates them to scroll offset changes.
type tvScrollThumb struct {
	widget.BaseWidget
	view *TextView
	rect *canvas.Rectangle
}

func newTVScrollThumb(view *TextView) *tvScrollThumb {
	thumb := &tvScrollThumb{view: view}
	thumb.rect = canvas.NewRectangle(color.Transparent)
	thumb.ExtendBaseWidget(thumb)
	return thumb
}

func (thumb *tvScrollThumb) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(thumb.rect)
}

func (thumb *tvScrollThumb) Cursor() desktop.Cursor           { return desktop.DefaultCursor }
func (thumb *tvScrollThumb) MouseIn(_ *desktop.MouseEvent)    {}
func (thumb *tvScrollThumb) MouseOut()                        {}
func (thumb *tvScrollThumb) MouseMoved(_ *desktop.MouseEvent) {}

func (thumb *tvScrollThumb) Dragged(ev *fyne.DragEvent) {
	view := thumb.view
	view.mu.RLock()
	totalLines := len(view.lines)
	view.mu.RUnlock()

	if view.rend == nil || totalLines == 0 {
		return
	}

	widgetPos := fyne.CurrentApp().Driver().AbsolutePositionForObject(view)
	widgetBottom := widgetPos.Y + view.Size().Height
	if ev.AbsolutePosition.Y < widgetPos.Y || ev.AbsolutePosition.Y > widgetBottom {
		return
	}

	bodyHeight := view.rend.size.Height - tvPadY*2
	visibleLines := int(bodyHeight / tvLineH)
	scrollable := totalLines - visibleLines
	if scrollable <= 0 {
		return
	}

	ratio := float32(visibleLines) / float32(totalLines)
	thumbHeight := bodyHeight * ratio
	if thumbHeight < tvScrollMinThumb {
		thumbHeight = tvScrollMinThumb
	}
	trackHeight := bodyHeight - thumbHeight
	if trackHeight <= 0 {
		return
	}

	newThumbTop := ev.AbsolutePosition.Y - widgetPos.Y - tvPadY - thumbHeight/2
	if newThumbTop < 0 {
		newThumbTop = 0
	}
	if newThumbTop > trackHeight {
		newThumbTop = trackHeight
	}

	view.scrollOffset = int((newThumbTop / trackHeight) * float32(scrollable))
	view.Refresh()
}

func (thumb *tvScrollThumb) DragEnd() {}

// tvRenderer draws the text content using canvas primitives.
// Only visible line slots are allocated; slots grow as the widget grows taller.
type tvRenderer struct {
	view *TextView
	size fyne.Size

	selBgs     []*canvas.Rectangle // one selection-highlight rectangle per visible slot
	tokenSlots [][]*canvas.Text    // [slot][tokenIndex] — pre-allocated token text objects
	slots      int

	scrollTrack *canvas.Rectangle
	scrollThumb *canvas.Rectangle // visual canvas rect for the scrollbar thumb
	thumbWidget *tvScrollThumb    // draggable overlay widget, positioned to match scrollThumb
}

func newTVRenderer(view *TextView) *tvRenderer {
	renderer := &tvRenderer{view: view}
	renderer.scrollTrack = canvas.NewRectangle(color.Transparent)
	renderer.scrollThumb = canvas.NewRectangle(color.Transparent)
	return renderer
}

func (renderer *tvRenderer) Destroy() {}

func (renderer *tvRenderer) Objects() []fyne.CanvasObject {
	total := 2 + len(renderer.selBgs) + len(renderer.tokenSlots)*tvMaxTokensPerLine
	objs := make([]fyne.CanvasObject, 0, total)
	objs = append(objs, renderer.scrollTrack, renderer.scrollThumb)
	for _, selBg := range renderer.selBgs {
		objs = append(objs, selBg)
	}
	for _, slot := range renderer.tokenSlots {
		for _, tokenText := range slot {
			objs = append(objs, tokenText)
		}
	}
	return objs
}

func (renderer *tvRenderer) MinSize() fyne.Size {
	return fyne.NewSize(200, tvLineH*3+tvPadY*2)
}

func (renderer *tvRenderer) Layout(size fyne.Size) {
	renderer.size = size
	renderer.checkWrapWidth()
	renderer.growSlots()
	renderer.layoutContent()
}

func (renderer *tvRenderer) Refresh() {
	renderer.checkWrapWidth()
	renderer.growSlots()
	renderer.layoutContent()
	canvas.Refresh(renderer.view)
}

// checkWrapWidth triggers a re-parse when the available text width changes.
func (renderer *tvRenderer) checkWrapWidth() {
	availWidth := renderer.size.Width - tvPadX*2 - tvScrollW
	if availWidth < 1 {
		return
	}
	renderer.view.mu.Lock()
	prevWidth := renderer.view.lastWrapWidth
	pendingText := renderer.view.pendingText
	renderer.view.lastWrapWidth = availWidth
	renderer.view.mu.Unlock()

	if pendingText == "" {
		return
	}
	if math.Abs(float64(prevWidth-availWidth)) > 4 || prevWidth < 1 {
		renderer.view.startProcessing(pendingText, availWidth)
	}
}

func (renderer *tvRenderer) growSlots() {
	bodyHeight := renderer.size.Height - tvPadY*2
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	needed := int(bodyHeight/tvLineH) + 2

	for renderer.slots < needed {
		selBg := canvas.NewRectangle(color.Transparent)
		renderer.selBgs = append(renderer.selBgs, selBg)

		tokenSlot := make([]*canvas.Text, tvMaxTokensPerLine)
		for i := range tokenSlot {
			tokenText := canvas.NewText("", color.White)
			tokenText.TextStyle = fyne.TextStyle{Monospace: true}
			tokenText.TextSize = theme.TextSize()
			tokenSlot[i] = tokenText
		}
		renderer.tokenSlots = append(renderer.tokenSlots, tokenSlot)
		renderer.slots++
	}

	for slot := needed; slot < renderer.slots; slot++ {
		renderer.selBgs[slot].Hide()
		for _, tokenText := range renderer.tokenSlots[slot] {
			tokenText.Hide()
		}
	}
}

func (renderer *tvRenderer) layoutContent() {
	renderer.view.mu.RLock()
	lines := renderer.view.lines
	scrollOffset := renderer.view.scrollOffset
	hasSelection := renderer.view.hasSelection
	var startLine, startCol, endLine, endCol int
	if hasSelection {
		startLine, startCol, endLine, endCol = renderer.view.normaliseSelection()
	}
	renderer.view.mu.RUnlock()

	totalLines := len(lines)
	needed := int((renderer.size.Height-tvPadY*2)/tvLineH) + 2
	contentWidth := renderer.size.Width - tvScrollW
	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width

	yPos := tvPadY
	for slot := 0; slot < needed && slot < renderer.slots; slot++ {
		lineIdx := scrollOffset + slot
		selBg := renderer.selBgs[slot]

		if lineIdx >= totalLines {
			selBg.FillColor = color.Transparent
			selBg.Resize(fyne.NewSize(0, 0))
			selBg.Refresh()
			selBg.Hide()
			for _, tokenText := range renderer.tokenSlots[slot] {
				tokenText.Hide()
			}
			yPos += tvLineH
			continue
		}

		// selection highlight background
		if hasSelection && lineIdx >= startLine && lineIdx <= endLine {
			line := lines[lineIdx]
			runes := []rune(line.Raw)
			lineLen := len(runes)

			selStart := 0
			selEnd := lineLen
			if lineIdx == startLine {
				selStart = startCol
			}
			if lineIdx == endLine {
				selEnd = endCol
			}
			if selStart > lineLen {
				selStart = lineLen
			}
			if selEnd > lineLen {
				selEnd = lineLen
			}

			selX := tvPadX + float32(selStart)*charWidth
			selWidth := float32(selEnd-selStart) * charWidth
			if lineIdx < endLine && selEnd == lineLen {
				selWidth += charWidth
			}
			selBg.Move(fyne.NewPos(selX, yPos))
			selBg.Resize(fyne.NewSize(selWidth, tvLineH))
			selBg.FillColor = selectionColor()
			selBg.Refresh()
			selBg.Show()
		} else {
			selBg.FillColor = color.Transparent
			selBg.Resize(fyne.NewSize(0, 0))
			selBg.Refresh()
			selBg.Hide()
		}

		// token text objects
		line := lines[lineIdx]
		xPos := tvPadX
		tokenIdx := 0
		for _, token := range line.Tokens {
			if tokenIdx >= tvMaxTokensPerLine {
				break
			}
			if token.Text == "" {
				continue
			}
			tokenText := renderer.tokenSlots[slot][tokenIdx]
			maxWidth := contentWidth - xPos
			displayText := truncateText(token.Text, theme.TextSize(), fyne.TextStyle{Monospace: true}, maxWidth)
			if displayText == "" {
				tokenText.Hide()
				tokenIdx++
				continue
			}
			tokenText.Text = displayText
			tokenText.Color = tvColor(token.Kind)
			tokenText.TextStyle = fyne.TextStyle{Monospace: true}
			tokenText.TextSize = theme.TextSize()
			tokenText.Move(fyne.NewPos(xPos, yPos))
			tokenText.Refresh()
			tokenText.Show()
			xPos += fyne.MeasureText(token.Text, theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
			tokenIdx++
		}
		for ; tokenIdx < tvMaxTokensPerLine; tokenIdx++ {
			renderer.tokenSlots[slot][tokenIdx].Hide()
		}

		yPos += tvLineH
	}

	renderer.layoutScrollbar(totalLines)
}

func (renderer *tvRenderer) layoutScrollbar(totalLines int) {
	bodyHeight := renderer.size.Height - tvPadY*2
	trackX := renderer.size.Width - tvScrollW

	renderer.scrollTrack.Move(fyne.NewPos(trackX, tvPadY))
	renderer.scrollTrack.Resize(fyne.NewSize(tvScrollW, bodyHeight))

	visibleLines := int(bodyHeight / tvLineH)
	if totalLines <= visibleLines {
		renderer.scrollTrack.FillColor = color.Transparent
		renderer.scrollTrack.Refresh()
		renderer.scrollThumb.FillColor = color.Transparent
		renderer.scrollThumb.Refresh()
		if renderer.thumbWidget != nil {
			renderer.thumbWidget.Hide()
		}
		return
	}

	renderer.scrollTrack.FillColor = theme.Color(theme.ColorNameScrollBar)
	renderer.scrollTrack.Refresh()

	ratio := float32(visibleLines) / float32(totalLines)
	thumbHeight := bodyHeight * ratio
	if thumbHeight < tvScrollMinThumb {
		thumbHeight = tvScrollMinThumb
	}
	scrollable := float32(totalLines - visibleLines)
	thumbY := tvPadY + (float32(renderer.view.scrollOffset)/scrollable)*(bodyHeight-thumbHeight)

	renderer.scrollThumb.Move(fyne.NewPos(trackX+1, thumbY))
	renderer.scrollThumb.Resize(fyne.NewSize(tvScrollW-2, thumbHeight))
	renderer.scrollThumb.FillColor = theme.Color(theme.ColorNameForeground)
	renderer.scrollThumb.CornerRadius = (tvScrollW - 2) / 2
	renderer.scrollThumb.Refresh()

	if renderer.thumbWidget != nil {
		renderer.thumbWidget.rect.FillColor = color.Transparent
		renderer.thumbWidget.Move(fyne.NewPos(trackX+1, thumbY))
		renderer.thumbWidget.Resize(fyne.NewSize(tvScrollW-2, thumbHeight))
		renderer.thumbWidget.Show()
	}
}

func selectionColor() color.Color {
	themeColor := theme.Color(theme.ColorNameSelection)
	if nrgba, ok := themeColor.(color.NRGBA); ok {
		nrgba.A = 180
		return nrgba
	}
	return themeColor
}
