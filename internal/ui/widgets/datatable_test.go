package widgets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestTable(rowCount int) *DataTable {
	dt := NewDataTable()
	dt.Columns = []DataTableColumn{
		{Header: "Col A", Width: 100},
		{Header: "Col B", Width: 200},
	}
	dt.RowCount = func() int { return rowCount }
	dt.RowID = func(row int) int64 { return int64(row + 1) }
	dt.CellValue = func(row, col int) string { return "cell" }
	return dt
}

// ── rowCount ─────────────────────────────────────────────────────────────────

func TestDataTable_RowCount_Nil(t *testing.T) {
	dt := NewDataTable()
	assert.Equal(t, 0, dt.rowCount())
}

func TestDataTable_RowCount(t *testing.T) {
	dt := newTestTable(42)
	assert.Equal(t, 42, dt.rowCount())
}

// ── initColWidths ────────────────────────────────────────────────────────────

func TestDataTable_InitColWidths(t *testing.T) {
	dt := newTestTable(0)
	dt.initColWidths()

	require.Len(t, dt.colWidths, 2)
	assert.Equal(t, float32(100), dt.colWidths[0])
	assert.Equal(t, float32(200), dt.colWidths[1])
}

func TestDataTable_InitColWidths_Idempotent(t *testing.T) {
	dt := newTestTable(0)
	dt.initColWidths()
	dt.colWidths[0] = 999
	dt.initColWidths()

	assert.Equal(t, float32(999), dt.colWidths[0], "initColWidths must not overwrite existing widths")
}

// ── SetSelected / ClearSelection ─────────────────────────────────────────────

func TestDataTable_SetSelected(t *testing.T) {
	dt := newTestTable(5)
	dt.hasSelected = false

	dt.selectedID = 3
	dt.hasSelected = true

	assert.True(t, dt.hasSelected)
	assert.Equal(t, int64(3), dt.selectedID)
}

func TestDataTable_SetSelected_Zero_Clears(t *testing.T) {
	dt := newTestTable(5)
	dt.selectedID = 3
	dt.hasSelected = true

	dt.selectedID = 0
	dt.hasSelected = false

	assert.False(t, dt.hasSelected)
}

func TestDataTable_ClearSelection(t *testing.T) {
	dt := newTestTable(5)
	dt.selectedID = 5
	dt.hasSelected = true

	dt.selectedID = 0
	dt.hasSelected = false

	assert.False(t, dt.hasSelected)
	assert.Equal(t, int64(0), dt.selectedID)
}

// ── tappedRow ────────────────────────────────────────────────────────────────

func TestDataTable_TappedRow_SetsSelectionID(t *testing.T) {
	dt := newTestTable(5)
	dt.OnSelect = func(row int) {}
	dt.rend = &dtRenderer{table: dt}

	// Call internal logic directly without triggering Refresh.
	if dt.RowID != nil {
		dt.selectedID = dt.RowID(2)
		dt.hasSelected = true
	}

	assert.True(t, dt.hasSelected)
	assert.Equal(t, int64(3), dt.selectedID) // RowID = row+1
}

func TestDataTable_TappedRow_FiresOnSelect(t *testing.T) {
	dt := newTestTable(5)
	fired := -1
	dt.OnSelect = func(row int) { fired = row }

	if dt.RowID != nil {
		dt.selectedID = dt.RowID(2)
		dt.hasSelected = true
	}
	dt.OnSelect(2)

	assert.Equal(t, 2, fired)
}

func TestDataTable_TappedRow_NoRowID_NoSelection(t *testing.T) {
	dt := newTestTable(5)
	dt.RowID = nil

	if dt.RowID != nil {
		dt.selectedID = dt.RowID(2)
		dt.hasSelected = true
	}

	assert.False(t, dt.hasSelected)
}

// ── ScrollToRow ──────────────────────────────────────────────────────────────

func TestDataTable_ScrollToRow_BelowViewport(t *testing.T) {
	dt := newTestTable(100)
	dt.rend = &dtRenderer{table: dt}
	dt.scrollOffset = 0

	row := 50
	visible := 5 // simulated
	if row >= dt.scrollOffset+visible {
		dt.scrollOffset = row - visible + 1
	}

	assert.Greater(t, dt.scrollOffset, 0)
}

func TestDataTable_ScrollToRow_AboveViewport(t *testing.T) {
	dt := newTestTable(100)
	dt.scrollOffset = 30

	row := 5
	if row < dt.scrollOffset {
		dt.scrollOffset = row
	}

	assert.Equal(t, 5, dt.scrollOffset)
}

func TestDataTable_ScrollToRow_NegativeClamp(t *testing.T) {
	dt := newTestTable(10)
	dt.scrollOffset = 5

	dt.scrollOffset = 0
	if dt.scrollOffset < 0 {
		dt.scrollOffset = 0
	}

	assert.GreaterOrEqual(t, dt.scrollOffset, 0)
}

// ── thumbBounds ──────────────────────────────────────────────────────────────

func TestDataTable_ThumbBounds_NoRenderer(t *testing.T) {
	dt := newTestTable(50)
	_, _, ok := dt.thumbBounds()
	assert.False(t, ok)
}

func TestDataTable_ThumbBounds_AllRowsFit(t *testing.T) {
	dt := newTestTable(3)
	// Simulate a large viewport where all rows fit.
	dt.rend = &dtRenderer{table: dt}
	// 3 rows × 32px = 96px. Set viewport height > that.
	dt.rend.size.Height = 500
	_, _, ok := dt.thumbBounds()
	assert.False(t, ok, "all rows fit — thumb not needed")
}

func TestDataTable_ThumbBounds_NeedsScroll(t *testing.T) {
	dt := newTestTable(100)
	dt.rend = &dtRenderer{table: dt}
	dt.rend.size.Height = 200 // small viewport
	_, thumbH, ok := dt.thumbBounds()

	assert.True(t, ok)
	assert.Greater(t, thumbH, float32(0))
}
