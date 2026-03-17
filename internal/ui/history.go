package ui

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/store"
)

type historyTab struct {
	st  *store.Store
	win fyne.Window

	mu       sync.RWMutex
	rows     []store.Transaction
	filtered []store.Transaction

	table        *widget.Table
	filterEntry  *widget.Entry
	showOutScope *widget.Check
	reqLabel     *widget.Label
	respLabel    *widget.Label
	sendRepeater *widget.Button
	sendLoot     *widget.Button

	selectedTx  store.Transaction
	hasSelected bool
}

var tableColumns = []string{"Method", "Host", "Path", "Status", "Size", "Duration"}
var columnWidths = []float32{80, 200, 300, 70, 90, 90}

func newHistoryTab(st *store.Store, win fyne.Window) fyne.CanvasObject {
	h := &historyTab{st: st, win: win}
	return h.build()
}

func (h *historyTab) build() fyne.CanvasObject {
	h.filterEntry = widget.NewEntry()
	h.filterEntry.SetPlaceHolder("Filter — host, path, method...")
	h.filterEntry.OnChanged = func(_ string) { h.applyFilter() }

	h.showOutScope = widget.NewCheck("Show out-of-scope", func(_ bool) { h.applyFilter() })
	h.showOutScope.Checked = true

	filterBar := container.NewBorder(nil, nil, nil, h.showOutScope, h.filterEntry)

	h.table = widget.NewTable(
		func() (int, int) {
			h.mu.RLock()
			defer h.mu.RUnlock()
			return len(h.filtered) + 1, len(tableColumns)
		},
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			l := obj.(*widget.Label)
			if id.Row == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
				l.SetText(tableColumns[id.Col])
				return
			}
			l.TextStyle = fyne.TextStyle{}
			h.mu.RLock()
			idx := id.Row - 1
			if idx >= len(h.filtered) {
				h.mu.RUnlock()
				l.SetText("")
				return
			}
			tx := h.filtered[idx]
			h.mu.RUnlock()
			l.SetText(h.cellText(tx, id.Col))
		},
	)

	for i, w := range columnWidths {
		h.table.SetColumnWidth(i, w)
	}

	h.table.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			h.table.UnselectAll()
			return
		}
		h.mu.RLock()
		idx := id.Row - 1
		if idx >= len(h.filtered) {
			h.mu.RUnlock()
			return
		}
		tx := h.filtered[idx] // copy by value right now
		h.mu.RUnlock()

		h.selectedTx = tx
		h.hasSelected = true
		h.showDetail(tx)
		h.sendRepeater.Enable()
		h.sendLoot.Enable()
	}

	h.reqLabel = widget.NewLabel("")
	h.reqLabel.TextStyle = fyne.TextStyle{Monospace: true}
	h.reqLabel.Wrapping = fyne.TextWrapBreak

	h.respLabel = widget.NewLabel("")
	h.respLabel.TextStyle = fyne.TextStyle{Monospace: true}
	h.respLabel.Wrapping = fyne.TextWrapBreak

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil,
		container.NewScroll(h.reqLabel))
	respPane := container.NewBorder(newBoldLabel("Response"), nil, nil, nil,
		container.NewScroll(h.respLabel))

	detailSplit := container.NewHSplit(reqPane, respPane)
	detailSplit.SetOffset(0.5)

	h.sendRepeater = widget.NewButtonWithIcon("Send to Repeater", theme.MailForwardIcon(), func() {})
	h.sendLoot = widget.NewButtonWithIcon("Send to Loot", theme.WarningIcon(), func() {})
	h.sendRepeater.Disable()
	h.sendLoot.Disable()

	detailPane := container.NewBorder(
		nil,
		container.NewHBox(h.sendRepeater, h.sendLoot),
		nil, nil,
		detailSplit,
	)

	mainSplit := container.NewVSplit(
		container.NewBorder(filterBar, nil, nil, nil, h.table),
		detailPane,
	)
	mainSplit.SetOffset(0.5)

	// Load existing rows from DB on startup.
	if txs, err := h.st.AllTransactions(); err == nil {
		h.mu.Lock()
		h.rows = txs
		h.mu.Unlock()
		h.applyFilter()
	}

	go h.watchUpdates()

	return mainSplit
}

func (h *historyTab) cellText(tx store.Transaction, col int) string {
	switch col {
	case 0:
		return tx.Method
	case 1:
		return tx.Host
	case 2:
		return pathOf(tx.URL)
	case 3:
		return fmt.Sprintf("%d", tx.StatusCode)
	case 4:
		return fmt.Sprintf("%db", len(tx.RespBody))
	case 5:
		return fmt.Sprintf("%dms", tx.DurationMs)
	}
	return ""
}

func (h *historyTab) showDetail(tx store.Transaction) {
	h.reqLabel.SetText(formatRequest(tx))
	h.respLabel.SetText(formatResponse(tx))
}

func (h *historyTab) applyFilter() {
	query := strings.ToLower(h.filterEntry.Text)
	showOut := h.showOutScope.Checked

	var filtered []store.Transaction
	h.mu.RLock()
	for _, tx := range h.rows {
		if !showOut && !tx.InScope {
			continue
		}
		if query != "" {
			if !strings.Contains(strings.ToLower(tx.Host+tx.URL+tx.Method), query) {
				continue
			}
		}
		filtered = append(filtered, tx)
	}
	h.mu.RUnlock()

	h.mu.Lock()
	h.filtered = filtered
	h.mu.Unlock()

	h.table.Refresh()
}

// watchUpdates receives new transactions and schedules a filtered rebuild
// on a short timer to avoid mutating filtered between cell clicks.
func (h *historyTab) watchUpdates() {
	for tx := range h.st.Updates {
		t := tx
		fyne.Do(func() {
			h.mu.Lock()
			h.rows = append([]store.Transaction{t}, h.rows...)
			if len(h.rows) > 10000 {
				h.rows = h.rows[:10000]
			}
			h.mu.Unlock()
			h.applyFilter()
		})
	}
}

func pathOf(rawURL string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(rawURL, prefix) {
			rest := rawURL[len(prefix):]
			if slash := strings.Index(rest, "/"); slash >= 0 {
				return rest[slash:]
			}
			return "/"
		}
	}
	return rawURL
}

func formatRequest(tx store.Transaction) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s %s HTTP/1.1\r\n", tx.Method, pathOf(tx.URL)))
	sb.WriteString(fmt.Sprintf("Host: %s\r\n", tx.Host))
	keys := make([]string, 0, len(tx.ReqHeaders))
	for k := range tx.ReqHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range tx.ReqHeaders[k] {
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	}
	sb.WriteString("\r\n")
	if len(tx.ReqBody) > 0 {
		sb.WriteString(string(tx.ReqBody))
	}
	return sb.String()
}

func formatResponse(tx store.Transaction) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d\r\n", tx.StatusCode))
	keys := make([]string, 0, len(tx.RespHeaders))
	for k := range tx.RespHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range tx.RespHeaders[k] {
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	}
	sb.WriteString("\r\n")
	if len(tx.RespBody) > 0 {
		sb.WriteString(string(tx.RespBody))
	}
	return sb.String()
}

func newLabel(text string) *widget.Label {
	return widget.NewLabel(text)
}

func newBoldLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}
