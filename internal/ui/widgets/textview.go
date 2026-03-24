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

// tvTokenKind identifies the syntax role of a token for colour mapping.
type tvTokenKind uint8

const (
	tvKindPlain     tvTokenKind = iota
	tvKindMethod                // HTTP request method
	tvKindPath                  // HTTP request path
	tvKindVersion               // HTTP version string
	tvKindStatus2xx             // 2xx response status code
	tvKindStatus3xx             // 3xx response status code
	tvKindStatus4xx             // 4xx response status code
	tvKindStatus5xx             // 5xx response status code
	tvKindHdrName               // header field name
	tvKindHdrColon              // colon separator after header name
	tvKindHdrValue              // header field value
	tvKindJSONKey               // JSON object key
	tvKindJSONStr               // JSON string value
	tvKindJSONNum               // JSON number
	tvKindJSONBool              // JSON true, false, null
	tvKindJSONPunct             // JSON structural punctuation: { } [ ] , :
	tvKindLow                   // dimmed / de-emphasised text
)

// tvColor returns the theme colour for a given token kind.
func tvColor(kind tvTokenKind) color.Color {
	switch kind {
	case tvKindMethod:
		return theme.Color(theme.ColorNameError)
	case tvKindPath:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindVersion, tvKindLow:
		return theme.Color(theme.ColorNamePlaceHolder)
	case tvKindStatus2xx:
		return theme.Color(theme.ColorNameSuccess)
	case tvKindStatus3xx:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindStatus4xx, tvKindStatus5xx:
		return theme.Color(theme.ColorNameError)
	case tvKindHdrName:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindHdrColon, tvKindJSONPunct:
		return theme.Color(theme.ColorNamePlaceHolder)
	case tvKindHdrValue:
		return theme.Color(theme.ColorNameForeground)
	case tvKindJSONKey:
		return theme.Color(theme.ColorNamePrimary)
	case tvKindJSONStr:
		return theme.Color(theme.ColorNameSuccess)
	case tvKindJSONNum:
		return theme.Color(theme.ColorNameWarning)
	case tvKindJSONBool:
		return theme.Color(theme.ColorNameError)
	default:
		return theme.Color(theme.ColorNameForeground)
	}
}

// tvToken is a run of text sharing a single syntax colour.
type tvToken struct {
	Text string
	Kind tvTokenKind
}

// tvLine is one visual line (after word-wrap) composed of tokens.
// Raw holds the plain text for selection and clipboard operations.
type tvLine struct {
	Tokens []tvToken
	Raw    string
}

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
	view.processing = true
	view.mu.Unlock()

	go func() {
		parsedLines := parseAndWrap(s, wrapWidth)
		fyne.Do(func() {
			view.mu.Lock()
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
	}()
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
	return int(availHeight/tvLineH) + 1
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
			view.win.Clipboard().SetContent(view.selectedText())
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
		view.selLine = totalLines - 1
		if view.selLine < 0 {
			view.selLine = 0
		}
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

	newThumbTop := thumb.Position().Y - tvPadY + ev.Dragged.DY
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

// parseAndWrap tokenises raw HTTP text and wraps each logical line to fit
// within wrapWidth pixels. Returns the complete slice of visual lines.
func parseAndWrap(s string, wrapWidth float32) []tvLine {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	logicalLines := strings.Split(s, "\n")

	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}
	charsPerLine := int(wrapWidth / charWidth)
	if charsPerLine < 10 {
		charsPerLine = 10
	}

	bodyContentType := detectContentType(logicalLines)

	var result []tvLine
	inBody := false

	for i, rawLine := range logicalLines {
		if !inBody && rawLine == "" && i > 0 {
			inBody = true
			result = append(result, tvLine{Tokens: []tvToken{{Text: "", Kind: tvKindPlain}}, Raw: ""})
			continue
		}

		var tokens []tvToken
		if !inBody {
			tokens = tokeniseHTTPMeta(rawLine, i == 0)
		} else {
			tokens = tokeniseBody(rawLine, bodyContentType)
		}

		result = append(result, wrapTokens(tokens, rawLine, charsPerLine)...)
	}
	return result
}

// detectContentType scans the header section to determine the body content type.
func detectContentType(lines []string) string {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-type:") {
			switch {
			case strings.Contains(lower, "json"):
				return "json"
			case strings.Contains(lower, "html"):
				return "html"
			default:
				return "text"
			}
		}
		if line == "" {
			break
		}
	}
	return "text"
}

// tokeniseHTTPMeta tokenises one line from the HTTP header section.
// isFirstLine indicates the request or response line.
func tokeniseHTTPMeta(line string, isFirstLine bool) []tvToken {
	if line == "" {
		return []tvToken{{Text: "", Kind: tvKindPlain}}
	}
	if isFirstLine {
		return tokeniseFirstLine(line)
	}
	colonIdx := strings.Index(line, ":")
	if colonIdx < 0 {
		return []tvToken{{Text: line, Kind: tvKindPlain}}
	}
	return []tvToken{
		{Text: line[:colonIdx], Kind: tvKindHdrName},
		{Text: ":", Kind: tvKindHdrColon},
		{Text: line[colonIdx+1:], Kind: tvKindHdrValue},
	}
}

// tokeniseFirstLine tokenises the HTTP request or response line.
func tokeniseFirstLine(line string) []tvToken {
	parts := strings.SplitN(line, " ", 3)
	if strings.HasPrefix(parts[0], "HTTP/") {
		if len(parts) < 2 {
			return []tvToken{{Text: line, Kind: tvKindPlain}}
		}
		statusKind := tvKindStatus2xx
		if len(parts[1]) > 0 {
			switch parts[1][0] {
			case '3':
				statusKind = tvKindStatus3xx
			case '4':
				statusKind = tvKindStatus4xx
			case '5':
				statusKind = tvKindStatus5xx
			}
		}
		tokens := []tvToken{
			{Text: parts[0] + " ", Kind: tvKindVersion},
			{Text: parts[1], Kind: statusKind},
		}
		if len(parts) == 3 {
			tokens = append(tokens, tvToken{Text: " " + parts[2], Kind: tvKindLow})
		}
		return tokens
	}
	if len(parts) == 3 {
		return []tvToken{
			{Text: parts[0] + " ", Kind: tvKindMethod},
			{Text: parts[1], Kind: tvKindPath},
			{Text: " " + parts[2], Kind: tvKindVersion},
		}
	}
	return []tvToken{{Text: line, Kind: tvKindPlain}}
}

// tokeniseBody tokenises a body line according to its content type.
func tokeniseBody(line string, contentType string) []tvToken {
	if contentType == "json" {
		return tokeniseJSON(line)
	}
	return []tvToken{{Text: line, Kind: tvKindPlain}}
}

// tokeniseJSON performs a single-pass tokenisation of a JSON line.
func tokeniseJSON(line string) []tvToken {
	if line == "" {
		return []tvToken{{Text: "", Kind: tvKindPlain}}
	}
	var tokens []tvToken
	runes := []rune(line)
	pos := 0
	for pos < len(runes) {
		ch := runes[pos]
		switch {
		case ch == '"':
			end := pos + 1
			for end < len(runes) && !(runes[end] == '"' && runes[end-1] != '\\') {
				end++
			}
			if end < len(runes) {
				end++
			}
			str := string(runes[pos:end])
			peek := end
			for peek < len(runes) && runes[peek] == ' ' {
				peek++
			}
			if peek < len(runes) && runes[peek] == ':' {
				tokens = append(tokens, tvToken{Text: str, Kind: tvKindJSONKey})
			} else {
				tokens = append(tokens, tvToken{Text: str, Kind: tvKindJSONStr})
			}
			pos = end
		case ch == '{' || ch == '}' || ch == '[' || ch == ']' || ch == ',' || ch == ':':
			tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindJSONPunct})
			pos++
		case ch == '-' || (ch >= '0' && ch <= '9'):
			end := pos + 1
			for end < len(runes) && (runes[end] >= '0' && runes[end] <= '9' || runes[end] == '.' || runes[end] == 'e' || runes[end] == 'E' || runes[end] == '+' || runes[end] == '-') {
				end++
			}
			tokens = append(tokens, tvToken{Text: string(runes[pos:end]), Kind: tvKindJSONNum})
			pos = end
		case ch == 't' || ch == 'f' || ch == 'n':
			matched := false
			for _, keyword := range []string{"true", "false", "null"} {
				if strings.HasPrefix(string(runes[pos:]), keyword) {
					tokens = append(tokens, tvToken{Text: keyword, Kind: tvKindJSONBool})
					pos += len([]rune(keyword))
					matched = true
					break
				}
			}
			if !matched {
				tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindPlain})
				pos++
			}
		case ch == ' ' || ch == '\t':
			end := pos + 1
			for end < len(runes) && (runes[end] == ' ' || runes[end] == '\t') {
				end++
			}
			tokens = append(tokens, tvToken{Text: string(runes[pos:end]), Kind: tvKindPlain})
			pos = end
		default:
			tokens = append(tokens, tvToken{Text: string(ch), Kind: tvKindPlain})
			pos++
		}
	}
	return tokens
}

// wrapTokens splits a logical line into visual lines of at most charsPerLine runes.
func wrapTokens(tokens []tvToken, rawLine string, charsPerLine int) []tvLine {
	if charsPerLine <= 0 {
		charsPerLine = 80
	}
	runes := []rune(rawLine)
	if len(runes) <= charsPerLine {
		return []tvLine{{Tokens: tokens, Raw: rawLine}}
	}

	var result []tvLine
	for start := 0; start < len(runes); start += charsPerLine {
		end := start + charsPerLine
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[start:end])

		var chunkTokens []tvToken
		tokenStart := 0
		for _, token := range tokens {
			tokenRunes := []rune(token.Text)
			tokenEnd := tokenStart + len(tokenRunes)
			overlapStart := max(tokenStart, start) - tokenStart
			overlapEnd := min(tokenEnd, end) - tokenStart
			if overlapStart < overlapEnd && overlapStart >= 0 && overlapEnd <= len(tokenRunes) {
				chunkTokens = append(chunkTokens, tvToken{
					Text: string(tokenRunes[overlapStart:overlapEnd]),
					Kind: token.Kind,
				})
			}
			tokenStart = tokenEnd
		}
		if len(chunkTokens) == 0 {
			chunkTokens = []tvToken{{Text: chunk, Kind: tvKindPlain}}
		}
		result = append(result, tvLine{Tokens: chunkTokens, Raw: chunk})
	}
	return result
}
