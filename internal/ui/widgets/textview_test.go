package widgets

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ── NewTextView / GetText / SetText ──────────────────────────────────────────

func TestTextView_GetText_Empty(t *testing.T) {
	tv := NewTextView()
	assert.Equal(t, "", tv.GetText())
}

func TestTextView_SetText_StoresRaw(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.rawFull = "hello world"
	tv.mu.Unlock()

	assert.Equal(t, "hello world", tv.GetText())
}

func TestTextView_SetText_ResetsScroll(t *testing.T) {
	tv := NewTextView()
	tv.scrollOffset = 10

	tv.mu.Lock()
	tv.rawFull = "new content"
	tv.scrollOffset = 0
	tv.scrollFrac = 0
	tv.hasSelection = false
	tv.mu.Unlock()

	assert.Equal(t, 0, tv.scrollOffset)
}

func TestTextView_SetText_ResetsSelection(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.hasSelection = true
	tv.selAnchorLine, tv.selAnchorCol = 1, 3
	tv.rawFull = "new"
	tv.hasSelection = false
	tv.mu.Unlock()

	assert.False(t, tv.hasSelection)
}

func TestTextView_SetText_Empty(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.rawFull = "something"
	tv.mu.Unlock()

	tv.mu.Lock()
	tv.rawFull = ""
	tv.mu.Unlock()

	assert.Equal(t, "", tv.GetText())
}

// ── normaliseSelection ───────────────────────────────────────────────────────

func TestTextView_NormaliseSelection_Forward(t *testing.T) {
	tv := NewTextView()
	tv.selAnchorLine, tv.selAnchorCol = 0, 2
	tv.selLine, tv.selCol = 1, 5

	sl, sc, el, ec := tv.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 2, sc)
	assert.Equal(t, 1, el)
	assert.Equal(t, 5, ec)
}

func TestTextView_NormaliseSelection_Reversed(t *testing.T) {
	tv := NewTextView()
	tv.selAnchorLine, tv.selAnchorCol = 1, 5
	tv.selLine, tv.selCol = 0, 2

	sl, sc, el, ec := tv.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 2, sc)
	assert.Equal(t, 1, el)
	assert.Equal(t, 5, ec)
}

func TestTextView_NormaliseSelection_SamePosition(t *testing.T) {
	tv := NewTextView()
	tv.selAnchorLine, tv.selAnchorCol = 0, 3
	tv.selLine, tv.selCol = 0, 3

	sl, sc, el, ec := tv.normaliseSelection()
	assert.Equal(t, 0, sl)
	assert.Equal(t, 3, sc)
	assert.Equal(t, 0, el)
	assert.Equal(t, 3, ec)
}

// ── selectedText ─────────────────────────────────────────────────────────────

func TestTextView_SelectedText_NoSelection(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.lines = []tvLine{{Raw: "hello world"}}
	tv.mu.Unlock()
	tv.hasSelection = false

	assert.Equal(t, "", tv.selectedText())
}

func TestTextView_SelectedText_SameLine(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.lines = []tvLine{{Raw: "hello world"}}
	tv.mu.Unlock()
	tv.selAnchorLine, tv.selAnchorCol = 0, 0
	tv.selLine, tv.selCol = 0, 5
	tv.hasSelection = true

	assert.Equal(t, "hello", tv.selectedText())
}

func TestTextView_SelectedText_MultiLine(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.lines = []tvLine{
		{Raw: "line one"},
		{Raw: "line two"},
	}
	tv.mu.Unlock()
	tv.selAnchorLine, tv.selAnchorCol = 0, 5
	tv.selLine, tv.selCol = 1, 4
	tv.hasSelection = true

	assert.Equal(t, "one\nline", tv.selectedText())
}

// ── pendingText ──────────────────────────────────────────────────────────────

func TestTextView_SetText_StoresPendingText(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.pendingText = "pending content"
	tv.rawFull = "pending content"
	tv.mu.Unlock()

	tv.mu.RLock()
	pending := tv.pendingText
	tv.mu.RUnlock()

	assert.Equal(t, "pending content", pending)
}

// ── scroll offset clamping ───────────────────────────────────────────────────

func TestTextView_ScrollOffset_ClampMin(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.lines = []tvLine{{Raw: "line one"}, {Raw: "line two"}}
	tv.mu.Unlock()
	tv.scrollOffset = 0

	// Simulate scroll up past top.
	tv.scrollOffset -= 5
	if tv.scrollOffset < 0 {
		tv.scrollOffset = 0
	}

	assert.Equal(t, 0, tv.scrollOffset)
}

func TestTextView_ScrollOffset_ClampMax(t *testing.T) {
	tv := NewTextView()
	tv.mu.Lock()
	tv.lines = make([]tvLine, 10)
	tv.mu.Unlock()
	tv.scrollOffset = 0

	maxOffset := 10 - 3
	tv.scrollOffset = 100
	if tv.scrollOffset > maxOffset {
		tv.scrollOffset = maxOffset
	}

	assert.Equal(t, maxOffset, tv.scrollOffset)
}
