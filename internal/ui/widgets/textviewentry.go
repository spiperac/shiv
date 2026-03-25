package widgets

import (
	"image/color"
	"strings"
	"sync"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const tveTabWidth = 4 // spaces per tab

// TextViewEntry is a syntax-highlighted, virtualised, editable text widget.
// It never calls widget.Entry.SetText so large requests never block the UI.
//
// Thread-safety: all mutable state is guarded by mu. All methods that mutate
// state are called from the Fyne UI goroutine (via TypedKey, TypedRune, etc.)
// so in practice mu only protects against the blink goroutine reading
// cursorVisible concurrently with the renderer. Visual cache reads/writes are
// also coordinated through mu.
type TextViewEntry struct {
	widget.BaseWidget

	// OnSend is called when the user presses Ctrl+S.
	OnSend func()

	mu      sync.Mutex
	rawFull string

	scrollOffset int
	scrollFrac   float32

	selAnchorLine int
	selAnchorCol  int
	selLine       int
	selCol        int
	hasSelection  bool

	cursorLine int
	cursorCol  int

	undoStack []tveUndoEntry

	shiftSelecting bool
	shiftHeld      bool

	cursorVisible bool
	blinkReset    chan struct{} // send to reset blink timer; close to stop goroutine
	blinkRunning  bool

	dragging         bool // true while mouse drag is in progress — guards FocusLost
	scrollbarHovered bool // true while mouse is over scrollbar — widens thumb

	// Visual line cache. Invalidated by setting visualCacheRaw = "".
	// Protected by mu — both raw snapshot and built result stored together so
	// there is no TOCTOU window.
	visualCache      []tveVisualLine
	visualCacheRaw   string
	visualCacheWidth int

	win  fyne.Window
	rend *tveRenderer
}

type tveUndoEntry struct {
	raw        string
	cursorLine int
	cursorCol  int
}

type tveVisualLine struct {
	tokens     []tvToken
	raw        string
	logicalIdx int
	colOffset  int
}

func NewTextViewEntry() *TextViewEntry {
	e := &TextViewEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *TextViewEntry) SetWindow(w fyne.Window) { e.win = w }
func (e *TextViewEntry) SetPlaceHolder(_ string) {}
func (e *TextViewEntry) AcceptsTab() bool        { return true }

func (e *TextViewEntry) GetText() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rawFull
}

func (e *TextViewEntry) SetText(s string) {
	e.mu.Lock()
	e.rawFull = s
	e.scrollOffset = 0
	e.scrollFrac = 0
	e.hasSelection = false
	e.shiftSelecting = false
	e.shiftHeld = false
	e.cursorLine = 0
	e.cursorCol = 0
	e.undoStack = nil
	e.visualCacheRaw = "" // invalidate
	e.mu.Unlock()
	e.Refresh()
}

// splitRaw normalises line endings and splits into logical lines.
func splitRaw(raw string) []string {
	if raw == "" {
		return []string{""}
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return strings.Split(raw, "\n")
}

// logicalLines returns the current logical lines. Caller must not hold mu.
func (e *TextViewEntry) logicalLines() []string {
	e.mu.Lock()
	raw := e.rawFull
	e.mu.Unlock()
	return splitRaw(raw)
}

// visual returns cached visual lines, rebuilding under mu if stale.
// This eliminates the TOCTOU race: raw snapshot and cache write happen in
// the same critical section.
func (e *TextViewEntry) visual() []tveVisualLine {
	cpl := e.charsPerLine()

	e.mu.Lock()
	raw := e.rawFull
	if e.visualCache != nil && e.visualCacheRaw == raw && e.visualCacheWidth == cpl {
		cached := e.visualCache
		e.mu.Unlock()
		return cached
	}
	// Build while holding the lock so raw cannot change under us.
	built := buildVisualLines(splitRaw(raw), cpl)
	e.visualCache = built
	e.visualCacheRaw = raw
	e.visualCacheWidth = cpl
	e.mu.Unlock()
	return built
}

// commitLines writes mutated lines back and invalidates the visual cache.
// Must be called from the UI goroutine (same as all edit methods).
func (e *TextViewEntry) commitLines(lines []string) {
	e.mu.Lock()
	e.rawFull = strings.Join(lines, "\n")
	e.visualCacheRaw = "" // invalidate
	e.mu.Unlock()
}

// charsPerLine computes the number of monospace characters that fit in the
// available width. Safe to call without holding mu.
func (e *TextViewEntry) charsPerLine() int {
	if e.rend == nil {
		return 80
	}
	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}
	availWidth := e.rend.size.Width - tvPadX*2 - tvScrollW
	if availWidth < 1 {
		return 80
	}
	n := int(availWidth / charWidth)
	if n < 10 {
		n = 10
	}
	return n
}

// buildVisualLines converts logical lines into wrapped visual lines.
// Each logical line is tokenised once for correct colours on continuations.
func buildVisualLines(logicalLines []string, charsPerLine int) []tveVisualLine {
	if charsPerLine < 10 {
		charsPerLine = 10
	}
	bodyStartLine := -1
	for i, l := range logicalLines {
		if l == "" && i > 0 {
			bodyStartLine = i
			break
		}
	}
	contentType := "text"
	if bodyStartLine >= 0 {
		contentType = detectContentType(logicalLines)
	}

	var result []tveVisualLine

	for logIdx, rawLine := range logicalLines {
		inBody := bodyStartLine >= 0 && logIdx > bodyStartLine
		runes := []rune(rawLine)

		if len(runes) == 0 {
			var tokens []tvToken
			if !inBody {
				tokens = tokeniseHTTPMeta(rawLine, logIdx == 0)
			} else {
				tokens = tokeniseBody(rawLine, contentType)
			}
			result = append(result, tveVisualLine{
				tokens: tokens, raw: "", logicalIdx: logIdx, colOffset: 0,
			})
			continue
		}

		colOffset := 0
		for colOffset < len(runes) {
			end := colOffset + charsPerLine
			if end > len(runes) {
				end = len(runes)
			}
			chunk := string(runes[colOffset:end])
			var chunkTokens []tvToken
			if !inBody {
				// Only the very first chunk of the first line is the HTTP start line.
				chunkTokens = tokeniseHTTPMeta(chunk, logIdx == 0 && colOffset == 0)
			} else {
				chunkTokens = tokeniseBody(chunk, contentType)
			}
			result = append(result, tveVisualLine{
				tokens:     chunkTokens,
				raw:        chunk,
				logicalIdx: logIdx,
				colOffset:  colOffset,
			})
			colOffset = end
		}
	}
	return result
}

// logicalToVisual maps a logical (line, col) to the visual line index and
// column within that visual line. Handles end-of-line and wrapped lines correctly.
func logicalToVisual(visual []tveVisualLine, logicalLine, logicalCol int) (int, int) {
	lastMatchV := -1
	for v, vl := range visual {
		if vl.logicalIdx != logicalLine {
			continue
		}
		chunkLen := len([]rune(vl.raw))
		chunkEnd := vl.colOffset + chunkLen
		isLastChunk := v+1 >= len(visual) || visual[v+1].logicalIdx != logicalLine

		if logicalCol >= vl.colOffset && (logicalCol < chunkEnd || isLastChunk) {
			col := logicalCol - vl.colOffset
			if col < 0 {
				col = 0
			}
			if col > chunkLen {
				col = chunkLen
			}
			return v, col
		}
		lastMatchV = v
	}
	if lastMatchV >= 0 {
		vl := visual[lastMatchV]
		return lastMatchV, len([]rune(vl.raw))
	}
	return 0, 0
}

func (e *TextViewEntry) pushUndo(prevRaw string, prevCursorLine, prevCursorCol int) {
	if len(e.undoStack) >= 100 {
		e.undoStack = e.undoStack[1:]
	}
	e.undoStack = append(e.undoStack, tveUndoEntry{
		raw: prevRaw, cursorLine: prevCursorLine, cursorCol: prevCursorCol,
	})
}

func (e *TextViewEntry) deleteSelection(lines []string) []string {
	if !e.hasSelection {
		return lines
	}
	startLine, startCol, endLine, endCol := e.normaliseSelection()
	if startLine == endLine {
		runes := []rune(lines[startLine])
		if startCol > len(runes) {
			startCol = len(runes)
		}
		if endCol > len(runes) {
			endCol = len(runes)
		}
		newRunes := append([]rune{}, runes[:startCol]...)
		newRunes = append(newRunes, runes[endCol:]...)
		lines[startLine] = string(newRunes)
	} else {
		startRunes := []rune(lines[startLine])
		endRunes := []rune(lines[endLine])
		if startCol > len(startRunes) {
			startCol = len(startRunes)
		}
		if endCol > len(endRunes) {
			endCol = len(endRunes)
		}
		merged := string(startRunes[:startCol]) + string(endRunes[endCol:])
		newLines := make([]string, 0, len(lines)-(endLine-startLine))
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, merged)
		newLines = append(newLines, lines[endLine+1:]...)
		lines = newLines
	}
	e.cursorLine = startLine
	e.cursorCol = startCol
	e.hasSelection = false
	e.shiftSelecting = false
	return lines
}

func (e *TextViewEntry) ensureCursorVisible() {
	visible := e.visibleLineCount()
	if visible <= 0 {
		return
	}
	vLine, _ := logicalToVisual(e.visual(), e.cursorLine, e.cursorCol)
	if vLine < e.scrollOffset {
		e.scrollOffset = vLine
	} else if vLine >= e.scrollOffset+visible {
		e.scrollOffset = vLine - visible + 1
	}
}

func (e *TextViewEntry) thumbBounds() (thumbY, thumbH float32, ok bool) {
	if e.rend == nil {
		return 0, 0, false
	}
	totalLines := len(e.visual())
	bodyHeight := e.rend.size.Height - tvPadY*2
	visibleLines := int(bodyHeight / tvLineH)
	scrollable := totalLines - visibleLines
	if scrollable <= 0 || bodyHeight <= 0 {
		return 0, 0, false
	}
	ratio := float32(visibleLines) / float32(totalLines)
	thumbH = bodyHeight * ratio
	if thumbH < tvScrollMinThumb {
		thumbH = tvScrollMinThumb
	}
	trackHeight := bodyHeight - thumbH
	thumbY = tvPadY + (float32(e.scrollOffset)/float32(scrollable))*trackHeight
	return thumbY, thumbH, true
}

// --------------------------------------------------------------------------
// Blink — single long-lived goroutine, reset via channel on each keystroke/click.
// --------------------------------------------------------------------------

func (e *TextViewEntry) startBlink() {
	e.stopBlink()
	resetCh := make(chan struct{}, 1)
	e.blinkReset = resetCh
	e.blinkRunning = true
	e.cursorVisible = true
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case _, ok := <-resetCh:
				if !ok {
					// Channel closed — widget destroyed or focus lost.
					return
				}
				// Reset: make cursor visible and restart the ticker.
				fyne.Do(func() { e.cursorVisible = true })
				ticker.Reset(500 * time.Millisecond)
			case <-ticker.C:
				fyne.Do(func() {
					e.cursorVisible = !e.cursorVisible
					e.Refresh()
				})
			}
		}
	}()
}

func (e *TextViewEntry) stopBlink() {
	if e.blinkReset != nil {
		close(e.blinkReset)
		e.blinkReset = nil
	}
	e.blinkRunning = false
	e.cursorVisible = false
}

// resetBlink makes the cursor immediately visible and resets the blink timer.
// Non-blocking — uses a buffered channel so rapid keystrokes never block.
func (e *TextViewEntry) resetBlink() {
	if !e.blinkRunning {
		return
	}
	select {
	case e.blinkReset <- struct{}{}:
	default:
		// Already a reset pending — fine, cursor will be shown.
	}
}

// --------------------------------------------------------------------------
// Widget lifecycle
// --------------------------------------------------------------------------

func (e *TextViewEntry) CreateRenderer() fyne.WidgetRenderer {
	r := newTVERenderer(e)
	e.rend = r
	return r
}

func (e *TextViewEntry) Build() fyne.CanvasObject {
	return container.NewStack(e, newTVEInputLayer(e))
}

// --------------------------------------------------------------------------
// Input handling
// --------------------------------------------------------------------------

func (e *TextViewEntry) Scrolled(ev *fyne.ScrollEvent) {
	totalLines := len(e.visual())
	visible := e.visibleLineCount()
	maxOffset := totalLines - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	e.scrollFrac -= ev.Scrolled.DY / tvLineH
	rows := int(e.scrollFrac)
	e.scrollFrac -= float32(rows)
	if rows == 0 {
		if ev.Scrolled.DY < 0 {
			rows = 1
		} else if ev.Scrolled.DY > 0 {
			rows = -1
		}
	}
	e.scrollOffset += rows
	if e.scrollOffset < 0 {
		e.scrollOffset = 0
	}
	if e.scrollOffset > maxOffset {
		e.scrollOffset = maxOffset
	}
	e.Refresh()
}

func (e *TextViewEntry) visibleLineCount() int {
	if e.rend == nil {
		return 0
	}
	availHeight := e.rend.size.Height - tvPadY*2
	if availHeight < 0 {
		return 0
	}
	return int(availHeight/tvLineH) + 1
}

func (e *TextViewEntry) posFromPoint(point fyne.Position) (line, col int) {
	visual := e.visual()
	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}
	vLineIdx := e.scrollOffset + int((point.Y-tvPadY)/tvLineH)
	if vLineIdx < 0 {
		vLineIdx = 0
	}
	if vLineIdx >= len(visual) {
		vLineIdx = len(visual) - 1
	}
	if vLineIdx < 0 {
		return 0, 0
	}
	vl := visual[vLineIdx]
	runes := []rune(vl.raw)
	vCol := int((point.X - tvPadX) / charWidth)
	if vCol < 0 {
		vCol = 0
	}
	if vCol > len(runes) {
		vCol = len(runes)
	}
	return vl.logicalIdx, vl.colOffset + vCol
}

func (e *TextViewEntry) normaliseSelection() (int, int, int, int) {
	al, ac := e.selAnchorLine, e.selAnchorCol
	cl, cc := e.selLine, e.selCol
	if al > cl || (al == cl && ac > cc) {
		return cl, cc, al, ac
	}
	return al, ac, cl, cc
}

func (e *TextViewEntry) selectedText() string {
	if !e.hasSelection {
		return ""
	}
	lines := e.logicalLines()
	startLine, startCol, endLine, endCol := e.normaliseSelection()
	if startLine == endLine {
		runes := []rune(lines[startLine])
		if startCol > len(runes) {
			startCol = len(runes)
		}
		if endCol > len(runes) {
			endCol = len(runes)
		}
		return string(runes[startCol:endCol])
	}
	var b strings.Builder
	for i := startLine; i <= endLine; i++ {
		if i >= len(lines) {
			break
		}
		runes := []rune(lines[i])
		lo, hi := 0, len(runes)
		if i == startLine {
			lo = startCol
		}
		if i == endLine {
			hi = endCol
			if hi > len(runes) {
				hi = len(runes)
			}
		}
		if lo > len(runes) {
			lo = len(runes)
		}
		b.WriteString(string(runes[lo:hi]))
		if i < endLine {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Dragged is implemented directly on TextViewEntry so Fyne's hit-test finds it
// before any parent split container — same pattern as widget.Entry.
func (e *TextViewEntry) Dragged(ev *fyne.DragEvent) {
	if e.rend != nil && ev.Position.X >= e.rend.size.Width-tvScrollW {
		return
	}
	line, col := e.posFromPoint(ev.Position)
	e.selLine, e.selCol = line, col
	e.cursorLine, e.cursorCol = line, col
	e.hasSelection = !(e.selAnchorLine == line && e.selAnchorCol == col)
	e.Refresh()
}

func (e *TextViewEntry) DragEnd() {}

func (e *TextViewEntry) TypedRune(r rune) {
	lines := e.logicalLines()
	prevRaw := strings.Join(lines, "\n")
	prevCL, prevCC := e.cursorLine, e.cursorCol
	if e.hasSelection {
		lines = e.deleteSelection(lines)
	}
	cl := e.cursorLine
	cc := e.cursorCol
	if cl >= len(lines) {
		cl = len(lines) - 1
	}
	runes := []rune(lines[cl])
	if cc > len(runes) {
		cc = len(runes)
	}
	newRunes := make([]rune, 0, len(runes)+1)
	newRunes = append(newRunes, runes[:cc]...)
	newRunes = append(newRunes, r)
	newRunes = append(newRunes, runes[cc:]...)
	lines[cl] = string(newRunes)
	e.pushUndo(prevRaw, prevCL, prevCC)
	e.commitLines(lines)
	e.mu.Lock()
	e.cursorLine = cl
	e.cursorCol = cc + 1
	e.mu.Unlock()
	e.ensureCursorVisible()
	e.resetBlink()
	e.Refresh()
}

func (e *TextViewEntry) TypedKey(ev *fyne.KeyEvent) {
	lines := e.logicalLines()
	cl := e.cursorLine
	cc := e.cursorCol
	if cl >= len(lines) {
		cl = len(lines) - 1
	}
	if cl < 0 {
		cl = 0
	}
	runes := []rune(lines[cl])
	if cc > len(runes) {
		cc = len(runes)
	}

	isShift := e.shiftHeld

	beginShift := func() {
		if !e.shiftSelecting {
			e.shiftSelecting = true
			e.selAnchorLine = cl
			e.selAnchorCol = cc
		}
	}
	updateShift := func() {
		e.selLine = e.cursorLine
		e.selCol = e.cursorCol
		e.hasSelection = !(e.selAnchorLine == e.selLine && e.selAnchorCol == e.selCol)
	}
	cancelShift := func() {
		e.shiftSelecting = false
		e.shiftHeld = false
		e.hasSelection = false
	}

	switch ev.Name {
	case fyne.KeyBackspace:
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		if e.hasSelection {
			lines = e.deleteSelection(lines)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
		} else if cc > 0 {
			newRunes := append([]rune{}, runes[:cc-1]...)
			newRunes = append(newRunes, runes[cc:]...)
			lines[cl] = string(newRunes)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
			e.mu.Lock()
			e.cursorCol = cc - 1
			e.mu.Unlock()
		} else if cl > 0 {
			prevRunes := []rune(lines[cl-1])
			merged := string(prevRunes) + string(runes)
			newLines := append([]string{}, lines[:cl-1]...)
			newLines = append(newLines, merged)
			newLines = append(newLines, lines[cl+1:]...)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(newLines)
			e.mu.Lock()
			e.cursorLine = cl - 1
			e.cursorCol = len(prevRunes)
			e.mu.Unlock()
		}
		cancelShift()

	case fyne.KeyDelete:
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		if e.hasSelection {
			lines = e.deleteSelection(lines)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
		} else if cc < len(runes) {
			newRunes := append([]rune{}, runes[:cc]...)
			newRunes = append(newRunes, runes[cc+1:]...)
			lines[cl] = string(newRunes)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
		} else if cl < len(lines)-1 {
			merged := string(runes) + lines[cl+1]
			newLines := append([]string{}, lines[:cl]...)
			newLines = append(newLines, merged)
			newLines = append(newLines, lines[cl+2:]...)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(newLines)
		}
		cancelShift()

	case fyne.KeyReturn, fyne.KeyEnter:
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		if e.hasSelection {
			lines = e.deleteSelection(lines)
			cl = e.cursorLine
			cc = e.cursorCol
			runes = []rune(lines[cl])
		}
		before := string(runes[:cc])
		after := string(runes[cc:])
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:cl]...)
		newLines = append(newLines, before, after)
		newLines = append(newLines, lines[cl+1:]...)
		e.pushUndo(prevRaw, prevCL, prevCC)
		e.commitLines(newLines)
		e.mu.Lock()
		e.cursorLine = cl + 1
		e.cursorCol = 0
		e.mu.Unlock()
		cancelShift()

	case fyne.KeyTab:
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		if e.hasSelection {
			startLine, selStartCol, endLine, selEndCol := e.normaliseSelection()
			indent := strings.Repeat(" ", tveTabWidth)
			for i := startLine; i <= endLine; i++ {
				lines[i] = indent + lines[i]
			}
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
			e.selAnchorLine = startLine
			if selStartCol > 0 {
				e.selAnchorCol = selStartCol + tveTabWidth
			} else {
				e.selAnchorCol = 0
			}
			e.selLine = endLine
			e.selCol = selEndCol + tveTabWidth
			// Preserve cursor at whichever end it was before.
			if prevCL == startLine {
				e.cursorLine = startLine
				e.cursorCol = selStartCol + tveTabWidth
			} else {
				e.cursorLine = endLine
				e.cursorCol = selEndCol + tveTabWidth
			}
		} else {
			indent := strings.Repeat(" ", tveTabWidth)
			newRunes := make([]rune, 0, len(runes)+tveTabWidth)
			newRunes = append(newRunes, runes[:cc]...)
			newRunes = append(newRunes, []rune(indent)...)
			newRunes = append(newRunes, runes[cc:]...)
			lines[cl] = string(newRunes)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
			e.mu.Lock()
			e.cursorCol = cc + tveTabWidth
			e.mu.Unlock()
		}

	case fyne.KeyLeft:
		if isShift {
			beginShift()
		} else if e.hasSelection {
			startLine, startCol, _, _ := e.normaliseSelection()
			e.mu.Lock()
			e.cursorLine = startLine
			e.cursorCol = startCol
			e.mu.Unlock()
			cancelShift()
			e.ensureCursorVisible()
			e.resetBlink()
			e.Refresh()
			return
		}
		if cc > 0 {
			e.mu.Lock()
			e.cursorCol--
			e.mu.Unlock()
		} else if cl > 0 {
			e.mu.Lock()
			e.cursorLine--
			e.cursorCol = len([]rune(lines[e.cursorLine]))
			e.mu.Unlock()
		}
		if isShift {
			updateShift()
		}

	case fyne.KeyRight:
		if isShift {
			beginShift()
		} else if e.hasSelection {
			_, _, endLine, endCol := e.normaliseSelection()
			e.mu.Lock()
			e.cursorLine = endLine
			e.cursorCol = endCol
			e.mu.Unlock()
			cancelShift()
			e.ensureCursorVisible()
			e.resetBlink()
			e.Refresh()
			return
		}
		if cc < len(runes) {
			e.mu.Lock()
			e.cursorCol++
			e.mu.Unlock()
		} else if cl < len(lines)-1 {
			e.mu.Lock()
			e.cursorLine++
			e.cursorCol = 0
			e.mu.Unlock()
		}
		if isShift {
			updateShift()
		}

	case fyne.KeyUp:
		if isShift {
			beginShift()
		} else {
			cancelShift()
		}
		if cl > 0 {
			e.mu.Lock()
			e.cursorLine--
			prevLen := len([]rune(lines[e.cursorLine]))
			if e.cursorCol > prevLen {
				e.cursorCol = prevLen
			}
			e.mu.Unlock()
		}
		if isShift {
			updateShift()
		}

	case fyne.KeyDown:
		if isShift {
			beginShift()
		} else {
			cancelShift()
		}
		if cl < len(lines)-1 {
			e.mu.Lock()
			e.cursorLine++
			nextLen := len([]rune(lines[e.cursorLine]))
			if e.cursorCol > nextLen {
				e.cursorCol = nextLen
			}
			e.mu.Unlock()
		}
		if isShift {
			updateShift()
		}

	case fyne.KeyHome:
		if isShift {
			beginShift()
		} else {
			cancelShift()
		}
		e.mu.Lock()
		e.cursorCol = 0
		e.mu.Unlock()
		if isShift {
			updateShift()
		}

	case fyne.KeyEnd:
		if isShift {
			beginShift()
		} else {
			cancelShift()
		}
		e.mu.Lock()
		e.cursorCol = len(runes)
		e.mu.Unlock()
		if isShift {
			updateShift()
		}

	case fyne.KeyPageUp:
		visible := e.visibleLineCount()
		visual := e.visual()
		curVLine, _ := logicalToVisual(visual, e.cursorLine, e.cursorCol)
		targetVLine := curVLine - visible
		if targetVLine < 0 {
			targetVLine = 0
		}
		vl := visual[targetVLine]
		e.mu.Lock()
		e.cursorLine = vl.logicalIdx
		e.cursorCol = vl.colOffset
		e.mu.Unlock()
		cancelShift()

	case fyne.KeyPageDown:
		visible := e.visibleLineCount()
		visual := e.visual()
		curVLine, _ := logicalToVisual(visual, e.cursorLine, e.cursorCol)
		targetVLine := curVLine + visible
		if targetVLine >= len(visual) {
			targetVLine = len(visual) - 1
		}
		vl := visual[targetVLine]
		e.mu.Lock()
		e.cursorLine = vl.logicalIdx
		e.cursorCol = vl.colOffset
		e.mu.Unlock()
		cancelShift()
	}

	e.ensureCursorVisible()
	e.resetBlink()
	e.Refresh()
}

func (e *TextViewEntry) TypedShortcut(s fyne.Shortcut) {
	if cs, ok := s.(*desktop.CustomShortcut); ok {
		if cs.KeyName == fyne.KeyS && cs.Modifier == fyne.KeyModifierControl {
			if e.OnSend != nil {
				e.OnSend()
			}
			return
		}
	}

	switch s.(type) {
	case *fyne.ShortcutUndo:
		e.mu.Lock()
		if len(e.undoStack) > 0 {
			undo := e.undoStack[len(e.undoStack)-1]
			e.undoStack = e.undoStack[:len(e.undoStack)-1]
			e.rawFull = undo.raw
			e.visualCacheRaw = ""
			e.cursorLine = undo.cursorLine
			e.cursorCol = undo.cursorCol
			e.hasSelection = false
			e.shiftSelecting = false
			e.shiftHeld = false
		}
		e.mu.Unlock()
		e.resetBlink()
		e.Refresh()

	case *fyne.ShortcutSelectAll:
		lines := e.logicalLines()
		totalLines := len(lines)
		e.selAnchorLine, e.selAnchorCol = 0, 0
		e.selLine = totalLines - 1
		if e.selLine < 0 {
			e.selLine = 0
		}
		e.selCol = len([]rune(lines[e.selLine]))
		e.hasSelection = true
		e.Refresh()

	case *fyne.ShortcutCopy:
		if e.win != nil && e.hasSelection {
			e.win.Clipboard().SetContent(e.selectedText())
		}

	case *fyne.ShortcutCut:
		if e.win == nil || !e.hasSelection {
			return
		}
		e.win.Clipboard().SetContent(e.selectedText())
		lines := e.logicalLines()
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		lines = e.deleteSelection(lines)
		e.pushUndo(prevRaw, prevCL, prevCC)
		e.commitLines(lines)
		e.ensureCursorVisible()
		e.resetBlink()
		e.Refresh()

	case *fyne.ShortcutPaste:
		if e.win == nil {
			return
		}
		text := e.win.Clipboard().Content()
		if text == "" {
			return
		}
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		lines := e.logicalLines()
		prevRaw := strings.Join(lines, "\n")
		prevCL, prevCC := e.cursorLine, e.cursorCol
		if e.hasSelection {
			lines = e.deleteSelection(lines)
		}
		cl := e.cursorLine
		cc := e.cursorCol
		if cl >= len(lines) {
			cl = len(lines) - 1
		}
		runes := []rune(lines[cl])
		if cc > len(runes) {
			cc = len(runes)
		}
		pasteLines := strings.Split(text, "\n")
		if len(pasteLines) == 1 {
			newRunes := make([]rune, 0, len(runes)+len([]rune(text)))
			newRunes = append(newRunes, runes[:cc]...)
			newRunes = append(newRunes, []rune(text)...)
			newRunes = append(newRunes, runes[cc:]...)
			lines[cl] = string(newRunes)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(lines)
			e.mu.Lock()
			e.cursorLine = cl
			e.cursorCol = cc + len([]rune(pasteLines[0]))
			e.mu.Unlock()
		} else {
			firstPart := string(runes[:cc]) + pasteLines[0]
			lastPart := pasteLines[len(pasteLines)-1] + string(runes[cc:])
			newLines := make([]string, 0, len(lines)+len(pasteLines)-1)
			newLines = append(newLines, lines[:cl]...)
			newLines = append(newLines, firstPart)
			newLines = append(newLines, pasteLines[1:len(pasteLines)-1]...)
			newLines = append(newLines, lastPart)
			newLines = append(newLines, lines[cl+1:]...)
			e.pushUndo(prevRaw, prevCL, prevCC)
			e.commitLines(newLines)
			e.mu.Lock()
			e.cursorLine = cl + len(pasteLines) - 1
			e.cursorCol = len([]rune(pasteLines[len(pasteLines)-1]))
			e.mu.Unlock()
		}
		e.ensureCursorVisible()
		e.resetBlink()
		e.Refresh()
	}
}

func (e *TextViewEntry) FocusGained() {
	e.startBlink()
	e.Refresh()
}

func (e *TextViewEntry) FocusLost() {
	e.stopBlink()
	e.shiftHeld = false
	// Only clear selection state if we are not mid-drag. When dragging outside
	// the window Fyne fires FocusLost but the user is still interacting.
	if !e.dragging {
		e.shiftSelecting = false
	}
	e.Refresh()
}

func (e *TextViewEntry) KeyDown(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		e.shiftHeld = true
	}
}

func (e *TextViewEntry) KeyUp(ev *fyne.KeyEvent) {
	if ev.Name == desktop.KeyShiftLeft || ev.Name == desktop.KeyShiftRight {
		e.shiftHeld = false
		e.shiftSelecting = false
	}
}

// --------------------------------------------------------------------------
// Renderer
// --------------------------------------------------------------------------

type tveRenderer struct {
	entry *TextViewEntry
	size  fyne.Size

	selBgs     []*canvas.Rectangle
	tokenSlots [][]*canvas.Text
	cursorRect *canvas.Rectangle
	slots      int

	scrollTrack *canvas.Rectangle
	scrollThumb *canvas.Rectangle

	lastWidth float32 // tracks width to invalidate visual cache on resize
}

func newTVERenderer(entry *TextViewEntry) *tveRenderer {
	r := &tveRenderer{entry: entry}
	r.scrollTrack = canvas.NewRectangle(color.Transparent)
	r.scrollThumb = canvas.NewRectangle(color.Transparent)
	r.cursorRect = canvas.NewRectangle(color.Transparent)
	return r
}

// Destroy is called when the widget is removed. Stop the blink goroutine.
func (r *tveRenderer) Destroy() {
	r.entry.stopBlink()
}

func (r *tveRenderer) Objects() []fyne.CanvasObject {
	total := 3 + len(r.selBgs) + len(r.tokenSlots)*tvMaxTokensPerLine
	objs := make([]fyne.CanvasObject, 0, total)
	objs = append(objs, r.scrollTrack, r.scrollThumb)
	for _, selBg := range r.selBgs {
		objs = append(objs, selBg)
	}
	for _, slot := range r.tokenSlots {
		for _, t := range slot {
			objs = append(objs, t)
		}
	}
	objs = append(objs, r.cursorRect)
	return objs
}

func (r *tveRenderer) MinSize() fyne.Size {
	return fyne.NewSize(200, tvLineH*3+tvPadY*2)
}

func (r *tveRenderer) Layout(size fyne.Size) {
	r.size = size
	r.checkWrapWidth()
	r.growSlots()
	r.layoutContent()
}

func (r *tveRenderer) Refresh() {
	r.checkWrapWidth()
	r.growSlots()
	r.layoutContent()
	canvas.Refresh(r.entry)
}

func (r *tveRenderer) checkWrapWidth() {
	availWidth := r.size.Width - tvPadX*2 - tvScrollW
	if availWidth < 1 {
		return
	}
	if r.lastWidth != availWidth {
		r.lastWidth = availWidth
		// Invalidate visual cache so wrapping is recomputed at new width.
		r.entry.mu.Lock()
		r.entry.visualCacheRaw = ""
		r.entry.mu.Unlock()
	}
}

func (r *tveRenderer) growSlots() {
	bodyHeight := r.size.Height - tvPadY*2
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	needed := int(bodyHeight/tvLineH) + 2
	for r.slots < needed {
		selBg := canvas.NewRectangle(color.Transparent)
		r.selBgs = append(r.selBgs, selBg)
		tokenSlot := make([]*canvas.Text, tvMaxTokensPerLine)
		for i := range tokenSlot {
			t := canvas.NewText("", color.White)
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = theme.TextSize()
			tokenSlot[i] = t
		}
		r.tokenSlots = append(r.tokenSlots, tokenSlot)
		r.slots++
	}
	for slot := needed; slot < r.slots; slot++ {
		r.selBgs[slot].Hide()
		for _, t := range r.tokenSlots[slot] {
			t.Hide()
		}
	}
}

func (r *tveRenderer) visibleLines() int {
	bodyHeight := r.size.Height - tvPadY*2
	if bodyHeight < 0 {
		return 0
	}
	return int(bodyHeight/tvLineH) + 1
}

func (r *tveRenderer) layoutContent() {
	entry := r.entry
	visual := entry.visual()
	totalLines := len(visual)

	charWidth := fyne.MeasureText("M", theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
	if charWidth <= 0 {
		charWidth = theme.TextSize() * 0.6
	}

	entry.mu.Lock()
	scrollOffset := entry.scrollOffset
	hasSelection := entry.hasSelection
	cursorLogLine := entry.cursorLine
	cursorLogCol := entry.cursorCol
	cursorVisible := entry.cursorVisible
	var selStartLog, selStartCol, selEndLog, selEndCol int
	if hasSelection {
		selStartLog, selStartCol, selEndLog, selEndCol = entry.normaliseSelection()
	}
	entry.mu.Unlock()

	visible := r.visibleLines()
	maxOffset := totalLines - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
		entry.mu.Lock()
		entry.scrollOffset = scrollOffset
		entry.mu.Unlock()
	}

	needed := int((r.size.Height-tvPadY*2)/tvLineH) + 2
	cursorVLine, cursorVCol := logicalToVisual(visual, cursorLogLine, cursorLogCol)
	cursorDrawn := false
	yPos := tvPadY

	for slot := 0; slot < needed && slot < r.slots; slot++ {
		lineIdx := scrollOffset + slot
		selBg := r.selBgs[slot]

		if lineIdx >= totalLines {
			selBg.FillColor = color.Transparent
			selBg.Resize(fyne.NewSize(0, 0))
			selBg.Refresh()
			selBg.Hide()
			for _, t := range r.tokenSlots[slot] {
				t.Hide()
			}
			yPos += tvLineH
			continue
		}

		vl := visual[lineIdx]

		// Selection highlight. Fix 5: use strict < chunkEnd for start boundary.
		if hasSelection {
			vSelStart, vSelEnd := -1, -1
			chunkStart := vl.colOffset
			chunkEnd := vl.colOffset + len([]rune(vl.raw))
			isLastChunk := lineIdx+1 >= len(visual) || visual[lineIdx+1].logicalIdx != vl.logicalIdx

			switch {
			case vl.logicalIdx > selStartLog && vl.logicalIdx < selEndLog:
				vSelStart = 0
				vSelEnd = len([]rune(vl.raw))
			case vl.logicalIdx == selStartLog && vl.logicalIdx == selEndLog:
				if selStartCol < chunkEnd && selEndCol > chunkStart {
					vSelStart = selStartCol - chunkStart
					vSelEnd = selEndCol - chunkStart
				}
			case vl.logicalIdx == selStartLog:
				if selStartCol < chunkEnd {
					vSelStart = selStartCol - chunkStart
					vSelEnd = len([]rune(vl.raw))
					// Extend one extra char width for visual continuity if not last chunk.
					if !isLastChunk {
						vSelEnd++
					}
				}
			case vl.logicalIdx == selEndLog:
				if selEndCol > chunkStart {
					vSelStart = 0
					vSelEnd = selEndCol - chunkStart
				}
			}

			if vSelStart < 0 {
				vSelStart = 0
			}
			lineLen := len([]rune(vl.raw))
			if vSelEnd > lineLen {
				vSelEnd = lineLen
			}

			if vSelStart >= 0 && vSelEnd > vSelStart {
				selBg.Move(fyne.NewPos(tvPadX+float32(vSelStart)*charWidth, yPos))
				selBg.Resize(fyne.NewSize(float32(vSelEnd-vSelStart)*charWidth, tvLineH))
				selBg.FillColor = selectionColor()
				selBg.Refresh()
				selBg.Show()
			} else {
				selBg.FillColor = color.Transparent
				selBg.Resize(fyne.NewSize(0, 0))
				selBg.Refresh()
				selBg.Hide()
			}
		} else {
			selBg.FillColor = color.Transparent
			selBg.Resize(fyne.NewSize(0, 0))
			selBg.Refresh()
			selBg.Hide()
		}

		// Draw tokens.
		xPos := tvPadX
		tokenIdx := 0
		for _, token := range vl.tokens {
			if tokenIdx >= tvMaxTokensPerLine {
				break
			}
			if token.Text == "" {
				continue
			}
			t := r.tokenSlots[slot][tokenIdx]
			t.Text = token.Text
			t.Color = tvColor(token.Kind)
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = theme.TextSize()
			t.Move(fyne.NewPos(xPos, yPos))
			t.Refresh()
			t.Show()
			xPos += fyne.MeasureText(token.Text, theme.TextSize(), fyne.TextStyle{Monospace: true}).Width
			tokenIdx++
		}
		for ; tokenIdx < tvMaxTokensPerLine; tokenIdx++ {
			r.tokenSlots[slot][tokenIdx].Hide()
		}

		// Draw cursor.
		if lineIdx == cursorVLine && cursorVisible {
			cursorX := tvPadX + float32(cursorVCol)*charWidth
			r.cursorRect.Move(fyne.NewPos(cursorX, yPos+2))
			r.cursorRect.Resize(fyne.NewSize(2, tvLineH-4))
			r.cursorRect.FillColor = theme.Color(theme.ColorNameForeground)
			r.cursorRect.Refresh()
			r.cursorRect.Show()
			cursorDrawn = true
		}

		yPos += tvLineH
	}

	if !cursorDrawn {
		r.cursorRect.FillColor = color.Transparent
		r.cursorRect.Resize(fyne.NewSize(0, 0))
		r.cursorRect.Refresh()
		r.cursorRect.Hide()
	}

	r.layoutScrollbar(totalLines)
}

func (r *tveRenderer) layoutScrollbar(totalLines int) {
	bodyHeight := r.size.Height - tvPadY*2
	trackW := float32(tvScrollW)
	trackX := r.size.Width - trackW
	if r.entry.scrollbarHovered {
		trackW = tvScrollW + 6
		trackX = r.size.Width - trackW
	}
	r.scrollTrack.Move(fyne.NewPos(trackX, tvPadY))
	r.scrollTrack.Resize(fyne.NewSize(trackW, bodyHeight))

	visibleLines := int(bodyHeight / tvLineH)
	if totalLines <= visibleLines {
		r.scrollTrack.FillColor = color.Transparent
		r.scrollTrack.Refresh()
		r.scrollThumb.FillColor = color.Transparent
		r.scrollThumb.Refresh()
		return
	}

	r.scrollTrack.FillColor = theme.Color(theme.ColorNameScrollBar)
	r.scrollTrack.Refresh()

	ratio := float32(visibleLines) / float32(totalLines)
	thumbHeight := bodyHeight * ratio
	if thumbHeight < tvScrollMinThumb {
		thumbHeight = tvScrollMinThumb
	}
	scrollable := float32(totalLines - visibleLines)
	thumbY := tvPadY + (float32(r.entry.scrollOffset)/scrollable)*(bodyHeight-thumbHeight)

	thumbW := float32(tvScrollW - 2)
	thumbX := trackX + 1
	if r.entry.scrollbarHovered {
		thumbW = trackW - 2
		thumbX = trackX + 1
	}
	r.scrollThumb.Move(fyne.NewPos(thumbX, thumbY))
	r.scrollThumb.Resize(fyne.NewSize(thumbW, thumbHeight))
	r.scrollThumb.FillColor = theme.Color(theme.ColorNameForeground)
	r.scrollThumb.CornerRadius = thumbW / 2
	r.scrollThumb.Refresh()
}

// --------------------------------------------------------------------------
// Input layer
// --------------------------------------------------------------------------

type tveInputLayer struct {
	widget.BaseWidget
	entry         *TextViewEntry
	draggingThumb bool
	draggingText  bool
}

func newTVEInputLayer(entry *TextViewEntry) *tveInputLayer {
	layer := &tveInputLayer{entry: entry}
	layer.ExtendBaseWidget(layer)
	return layer
}

func (layer *tveInputLayer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (layer *tveInputLayer) Scrolled(ev *fyne.ScrollEvent) { layer.entry.Scrolled(ev) }

func (layer *tveInputLayer) inScrollbarArea(xPos float32) bool {
	if layer.entry.rend == nil {
		return false
	}
	return xPos >= layer.entry.rend.size.Width-tvScrollW
}

func (layer *tveInputLayer) inThumb(xPos, yPos float32) bool {
	if !layer.inScrollbarArea(xPos) {
		return false
	}
	thumbY, thumbH, ok := layer.entry.thumbBounds()
	if !ok {
		return false
	}
	return yPos >= thumbY && yPos <= thumbY+thumbH
}

func (layer *tveInputLayer) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	layer.draggingThumb = layer.inThumb(ev.Position.X, ev.Position.Y)
	if layer.draggingThumb {
		return
	}
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	entry := layer.entry
	line, col := entry.posFromPoint(ev.Position)
	entry.selAnchorLine, entry.selAnchorCol = line, col
	entry.selLine, entry.selCol = line, col
	entry.cursorLine, entry.cursorCol = line, col
	entry.hasSelection = false
	entry.shiftSelecting = false
	entry.shiftHeld = false
	entry.dragging = false
	entry.resetBlink()
	entry.Refresh()
	if entry.win != nil {
		entry.win.Canvas().Focus(entry)
	}
}

func (layer *tveInputLayer) MouseUp(_ *desktop.MouseEvent) {
	layer.draggingThumb = false
	layer.draggingText = false
	layer.entry.dragging = false
}

func (layer *tveInputLayer) Tapped(_ *fyne.PointEvent) {}

func (layer *tveInputLayer) DoubleTapped(ev *fyne.PointEvent) {
	if layer.inScrollbarArea(ev.Position.X) {
		return
	}
	entry := layer.entry
	line, col := entry.posFromPoint(ev.Position)
	lines := entry.logicalLines()
	if line >= len(lines) {
		return
	}
	runes := []rune(lines[line])
	if len(runes) == 0 {
		return
	}
	if col >= len(runes) {
		col = len(runes) - 1
	}
	wordStart := col
	for wordStart > 0 && tveIsWordRune(runes[wordStart-1]) {
		wordStart--
	}
	wordEnd := col
	for wordEnd < len(runes) && tveIsWordRune(runes[wordEnd]) {
		wordEnd++
	}
	entry.selAnchorLine, entry.selAnchorCol = line, wordStart
	entry.selLine, entry.selCol = line, wordEnd
	entry.cursorLine, entry.cursorCol = line, wordEnd
	entry.hasSelection = wordStart != wordEnd
	entry.Refresh()
}

func (layer *tveInputLayer) dragScrollbar(ev *fyne.DragEvent) {
	entry := layer.entry
	if entry.rend == nil {
		return
	}
	totalLines := len(entry.visual())
	if totalLines == 0 {
		return
	}
	bodyHeight := entry.rend.size.Height - tvPadY*2
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
	newThumbTop := ev.Position.Y - tvPadY - thumbHeight/2
	if newThumbTop < 0 {
		newThumbTop = 0
	}
	if newThumbTop > trackHeight {
		newThumbTop = trackHeight
	}
	entry.scrollOffset = int((newThumbTop / trackHeight) * float32(scrollable))
	entry.Refresh()
}

func (layer *tveInputLayer) Dragged(ev *fyne.DragEvent) {
	if layer.draggingThumb {
		layer.dragScrollbar(ev)
		return
	}
	layer.draggingText = true
	layer.entry.dragging = true
	layer.entry.Dragged(ev)
}

func (layer *tveInputLayer) DragEnd() {
	wasDraggingText := layer.draggingText
	layer.draggingThumb = false
	layer.draggingText = false
	layer.entry.dragging = false
	if wasDraggingText && layer.entry.win != nil {
		layer.entry.win.Canvas().Focus(layer.entry)
		layer.entry.resetBlink()
	}
}

func (layer *tveInputLayer) MouseIn(_ *desktop.MouseEvent) {}

func (layer *tveInputLayer) MouseOut() {
	if layer.entry.scrollbarHovered {
		layer.entry.scrollbarHovered = false
		layer.entry.Refresh()
	}
}

func (layer *tveInputLayer) MouseMoved(ev *desktop.MouseEvent) {
	inScrollbar := layer.inScrollbarArea(ev.Position.X)
	if inScrollbar != layer.entry.scrollbarHovered {
		layer.entry.scrollbarHovered = inScrollbar
		layer.entry.Refresh()
	}
}

func tveIsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
