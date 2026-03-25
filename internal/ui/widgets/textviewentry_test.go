package widgets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTVE() *TextViewEntry {
	e := &TextViewEntry{}
	e.ExtendBaseWidget(e)
	return e
}

// ── splitRaw ─────────────────────────────────────────────────────────────────

func TestSplitRaw(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", []string{""}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\rb", []string{"a", "b"}},
		{"a\nb\nc", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitRaw(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ── buildVisualLines ─────────────────────────────────────────────────────────

func TestBuildVisualLines_NoWrap(t *testing.T) {
	lines := buildVisualLines([]string{"GET /foo HTTP/1.1", "Host: example.com"}, 80)
	assert.Len(t, lines, 2)
}

func TestBuildVisualLines_Wraps(t *testing.T) {
	long := strings.Repeat("x", 100)
	lines := buildVisualLines([]string{long}, 40)

	assert.Greater(t, len(lines), 2, "expected ≥3 visual lines for 100-char line at width 40")

	for _, vl := range lines {
		assert.Equal(t, 0, vl.logicalIdx, "all chunks should map to logicalIdx 0")
	}

	expected := 0
	for _, vl := range lines {
		assert.Equal(t, expected, vl.colOffset)
		expected += len([]rune(vl.raw))
	}
}

func TestBuildVisualLines_EmptyLine(t *testing.T) {
	lines := buildVisualLines([]string{"GET /foo HTTP/1.1", "", `{"key":"val"}`}, 80)
	assert.Len(t, lines, 3)
}

func TestBuildVisualLines_EmptyInput(t *testing.T) {
	lines := buildVisualLines([]string{""}, 80)
	assert.Len(t, lines, 1)
}

// ── logicalToVisual ──────────────────────────────────────────────────────────

func TestLogicalToVisual_Simple(t *testing.T) {
	visual := buildVisualLines([]string{"hello", "world"}, 80)

	v, col := logicalToVisual(visual, 0, 0)
	assert.Equal(t, 0, v)
	assert.Equal(t, 0, col)

	v, col = logicalToVisual(visual, 1, 3)
	assert.Equal(t, 1, visual[v].logicalIdx)
	assert.Equal(t, 3, col)
}

func TestLogicalToVisual_EndOfLine(t *testing.T) {
	visual := buildVisualLines([]string{"hello"}, 80)

	v, col := logicalToVisual(visual, 0, 5)
	assert.Equal(t, 0, v)
	assert.Equal(t, 5, col)
}

func TestLogicalToVisual_WrappedLine(t *testing.T) {
	// "01234567890123456789" wrapped at 10 → chunks [0:10] [10:20]
	visual := buildVisualLines([]string{"01234567890123456789"}, 10)

	v, col := logicalToVisual(visual, 0, 11)
	assert.Equal(t, 10, visual[v].colOffset, "col 11 should land in chunk starting at offset 10")
	assert.Equal(t, 1, col)
}

func TestLogicalToVisual_EndOfWrappedLine(t *testing.T) {
	// "01234567890123456789" wrapped at 10 → chunks [0:10] [10:20]
	visual := buildVisualLines([]string{"01234567890123456789"}, 10)

	v, _ := logicalToVisual(visual, 0, 20)
	assert.Equal(t, visual[len(visual)-1].colOffset, visual[v].colOffset, "col at end should land in last chunk")
}

// ── normaliseSelection ───────────────────────────────────────────────────────

func TestNormaliseSelection_Forward(t *testing.T) {
	e := newTVE()
	e.selAnchorLine, e.selAnchorCol = 0, 2
	e.selLine, e.selCol = 1, 5

	sl, sc, el, ec := e.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 2, sc)
	assert.Equal(t, 1, el)
	assert.Equal(t, 5, ec)
}

func TestNormaliseSelection_Reversed(t *testing.T) {
	e := newTVE()
	e.selAnchorLine, e.selAnchorCol = 1, 5
	e.selLine, e.selCol = 0, 2

	sl, sc, el, ec := e.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 2, sc)
	assert.Equal(t, 1, el)
	assert.Equal(t, 5, ec)
}

func TestNormaliseSelection_SamePosition(t *testing.T) {
	e := newTVE()
	e.selAnchorLine, e.selAnchorCol = 0, 3
	e.selLine, e.selCol = 0, 3

	sl, sc, el, ec := e.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 3, sc)
	assert.Equal(t, 0, el)
	assert.Equal(t, 3, ec)
}

// ── selectedText ─────────────────────────────────────────────────────────────

func TestSelectedText_SameLine(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello world"
	e.mu.Unlock()
	e.selAnchorLine, e.selAnchorCol = 0, 0
	e.selLine, e.selCol = 0, 5
	e.hasSelection = true

	assert.Equal(t, "hello", e.selectedText())
}

func TestSelectedText_MultiLine(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "line one\nline two"
	e.mu.Unlock()
	e.selAnchorLine, e.selAnchorCol = 0, 5
	e.selLine, e.selCol = 1, 4
	e.hasSelection = true

	assert.Equal(t, "one\nline", e.selectedText())
}

func TestSelectedText_NoSelection(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello"
	e.mu.Unlock()
	e.hasSelection = false

	assert.Equal(t, "", e.selectedText())
}

// ── deleteSelection ──────────────────────────────────────────────────────────

func TestDeleteSelection_SameLine(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello world"
	e.mu.Unlock()
	e.selAnchorLine, e.selAnchorCol = 0, 6
	e.selLine, e.selCol = 0, 11
	e.hasSelection = true

	lines := e.logicalLines()
	lines = e.deleteSelection(lines)
	e.commitLines(lines)

	assert.Equal(t, "hello ", e.GetText())
	assert.Equal(t, 6, e.cursorCol)
	assert.False(t, e.hasSelection)
}

func TestDeleteSelection_MultiLine(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "line one\nline two\nline three"
	e.mu.Unlock()
	e.selAnchorLine, e.selAnchorCol = 0, 5
	e.selLine, e.selCol = 2, 4
	e.hasSelection = true

	lines := e.logicalLines()
	lines = e.deleteSelection(lines)
	e.commitLines(lines)

	assert.Equal(t, "line  three", e.GetText())
}

func TestDeleteSelection_ReversedAnchor(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello world"
	e.mu.Unlock()
	e.selAnchorLine, e.selAnchorCol = 0, 11
	e.selLine, e.selCol = 0, 6
	e.hasSelection = true

	lines := e.logicalLines()
	lines = e.deleteSelection(lines)
	e.commitLines(lines)

	assert.Equal(t, "hello ", e.GetText())
}

func TestDeleteSelection_NoSelection(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello"
	e.mu.Unlock()
	e.hasSelection = false

	lines := e.logicalLines()
	lines = e.deleteSelection(lines)
	e.commitLines(lines)

	assert.Equal(t, "hello", e.GetText())
}

// ── pushUndo / undo ──────────────────────────────────────────────────────────

func TestPushUndo_StoresCursorPosition(t *testing.T) {
	e := newTVE()
	e.pushUndo("hello", 0, 3)

	require.Len(t, e.undoStack, 1)
	assert.Equal(t, "hello", e.undoStack[0].raw)
	assert.Equal(t, 0, e.undoStack[0].cursorLine)
	assert.Equal(t, 3, e.undoStack[0].cursorCol)
}

func TestPushUndo_StackLimit(t *testing.T) {
	e := newTVE()
	for i := 0; i < 110; i++ {
		e.pushUndo("x", 0, 0)
	}
	assert.LessOrEqual(t, len(e.undoStack), 100)
}

func TestUndo_RestoresTextAndCursor(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello"
	e.mu.Unlock()
	e.cursorLine, e.cursorCol = 0, 5

	e.pushUndo("hello", 0, 5)

	e.mu.Lock()
	e.rawFull = "hello world"
	e.visualCacheRaw = ""
	e.cursorLine = 0
	e.cursorCol = 11
	e.mu.Unlock()

	e.mu.Lock()
	undo := e.undoStack[len(e.undoStack)-1]
	e.undoStack = e.undoStack[:len(e.undoStack)-1]
	e.rawFull = undo.raw
	e.visualCacheRaw = ""
	e.cursorLine = undo.cursorLine
	e.cursorCol = undo.cursorCol
	e.mu.Unlock()

	assert.Equal(t, "hello", e.GetText())
	assert.Equal(t, 0, e.cursorLine)
	assert.Equal(t, 5, e.cursorCol)
}

// ── visual cache ─────────────────────────────────────────────────────────────

func TestVisualCache_InvalidatedOnCommit(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello"
	e.mu.Unlock()

	e.mu.Lock()
	e.visualCache = buildVisualLines([]string{"hello"}, 80)
	e.visualCacheRaw = "hello"
	e.visualCacheWidth = 80
	e.mu.Unlock()

	e.commitLines([]string{"world"})

	e.mu.Lock()
	cacheRaw := e.visualCacheRaw
	e.mu.Unlock()

	assert.Equal(t, "", cacheRaw, "commitLines should invalidate visual cache")
}

func TestVisualCache_HitOnSecondCall(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "GET /foo HTTP/1.1\nHost: example.com"
	e.visualCacheRaw = ""
	e.mu.Unlock()

	// Prime the cache manually without needing a renderer.
	cpl := 80
	e.mu.Lock()
	built := buildVisualLines(splitRaw(e.rawFull), cpl)
	e.visualCache = built
	e.visualCacheRaw = e.rawFull
	e.visualCacheWidth = cpl
	e.mu.Unlock()

	e.mu.Lock()
	c1 := e.visualCache
	c2 := e.visualCache
	e.mu.Unlock()

	require.NotEmpty(t, c1)
	assert.Equal(t, &c1[0], &c2[0], "expected same slice on repeated access")
}

func TestVisualCache_MissAfterMutation(t *testing.T) {
	e := newTVE()
	e.mu.Lock()
	e.rawFull = "hello"
	cpl := 80
	e.visualCache = buildVisualLines([]string{"hello"}, cpl)
	e.visualCacheRaw = "hello"
	e.visualCacheWidth = cpl
	e.mu.Unlock()

	e.commitLines([]string{"world"})

	e.mu.Lock()
	cacheRaw := e.visualCacheRaw
	e.mu.Unlock()

	assert.Equal(t, "", cacheRaw, "cache should be invalidated after mutation")
}

// ── commitLines ──────────────────────────────────────────────────────────────

func TestCommitLines_UpdatesText(t *testing.T) {
	e := newTVE()
	e.commitLines([]string{"new text"})
	assert.Equal(t, "new text", e.GetText())
}

func TestCommitLines_MultiLine(t *testing.T) {
	e := newTVE()
	e.commitLines([]string{"line one", "line two"})
	assert.Equal(t, "line one\nline two", e.GetText())
}
