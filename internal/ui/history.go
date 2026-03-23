package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type historyTab struct {
	projectStore *store.Store
	win          fyne.Window
	repeater     *repeaterTab
	loot         *lootTab
	intruder     *intruderTab

	mu       sync.RWMutex
	rows     []store.Transaction
	filtered []store.Transaction

	table        *tappableTable
	filterEntry  *widget.Entry
	showOutScope *widget.Check
	reqLabel     *readOnlyEntry
	respLabel    *readOnlyEntry
	scopeBtn     *widget.Button

	selectedTx  store.Transaction
	hasSelected bool
}

var tableColumns = []string{"Method", "Host", "Path", "Status", "Size", "Duration"}
var columnWidths = []float32{80, 200, 300, 70, 90, 90}

func newHistoryTab(projectStore *store.Store, win fyne.Window, repeater *repeaterTab, loot *lootTab, intruder *intruderTab) *historyTab {
	return &historyTab{
		projectStore: projectStore,
		win:          win,
		repeater:     repeater,
		loot:         loot,
		intruder:     intruder,
	}
}

func (h *historyTab) build() fyne.CanvasObject {
	h.filterEntry = widget.NewEntry()
	h.filterEntry.SetPlaceHolder("Filter — host, path, method, status...")
	h.filterEntry.OnChanged = func(_ string) { h.applyFilter() }

	h.showOutScope = widget.NewCheck("Show out-of-scope", func(_ bool) { h.applyFilter() })
	h.showOutScope.Checked = true

	h.scopeBtn = widget.NewButtonWithIcon("Scope", AppIcon("scope"), func() {
		showScopeDialog(h.projectStore, h.win)
	})

	clearBtn := widget.NewButtonWithIcon("Clear", AppIcon("delete"), func() {
		if err := h.projectStore.ClearHistory(); err != nil {
			logger.Error("clear history: %v", err)
			return
		}
		h.mu.Lock()
		h.rows = nil
		h.filtered = nil
		h.mu.Unlock()
		h.table.Refresh()
		h.selectedTx = store.Transaction{}
		h.hasSelected = false
		h.reqLabel.SetText("")
		h.respLabel.SetText("")
	})

	filterBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(h.showOutScope, h.scopeBtn, clearBtn),
		h.filterEntry,
	)

	h.reqLabel = newReadOnlyEntry()
	h.respLabel = newReadOnlyEntry()

	h.table = newTappableTable(
		h.win,
		func() (int, int) {
			h.mu.RLock()
			defer h.mu.RUnlock()
			return len(h.filtered) + 1, len(tableColumns)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.Importance = widget.MediumImportance
				label.SetText(tableColumns[id.Col])
				return
			}
			label.TextStyle = fyne.TextStyle{}
			h.mu.RLock()
			idx := id.Row - 1
			if idx >= len(h.filtered) {
				h.mu.RUnlock()
				label.SetText("")
				return
			}
			tx := h.filtered[idx]
			isSelected := h.hasSelected && h.selectedTx.ID == tx.ID
			h.mu.RUnlock()
			label.SetText(h.cellText(tx, id.Col))
			highlightTableRow(label, isSelected, widget.MediumImportance)
		},
		func() int {
			if !h.hasSelected {
				return -1
			}
			h.mu.RLock()
			defer h.mu.RUnlock()
			for i, tx := range h.filtered {
				if tx.ID == h.selectedTx.ID {
					return i
				}
			}
			return -1
		},
		func(row int) []ContextMenuItem {
			h.mu.RLock()
			if row >= len(h.filtered) {
				h.mu.RUnlock()
				return nil
			}
			tx := h.filtered[row]
			h.mu.RUnlock()
			return h.contextMenuItems(tx)
		},
	)

	for i, colWidth := range columnWidths {
		h.table.SetColumnWidth(i, colWidth)
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
		tx := h.filtered[idx]
		h.mu.RUnlock()

		h.selectedTx = tx
		h.hasSelected = true
		h.table.Refresh()

		go func() {
			fullTx, err := h.projectStore.GetTransaction(tx.ID)
			if err != nil {
				logger.Error("history: get transaction: %v", err)
				return
			}
			fyne.Do(func() {
				h.selectedTx = *fullTx
				h.showDetail(*fullTx)
				h.table.Refresh()
			})
		}()
	}

	h.win.Canvas().AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl}, func(_ fyne.Shortcut) {
		if h.hasSelected {
			h.sendToRepeater(h.selectedTx)
		}
	})

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		if !h.hasSelected {
			return
		}
		showInspectorDialog(h.selectedTx, h.win)
	})

	reqPane := container.NewBorder(
		newBoldLabel("Request"),
		nil, nil, nil,
		container.NewScroll(h.reqLabel),
	)

	respPane := container.NewBorder(
		container.NewBorder(nil, nil, nil, inspectBtn, newBoldLabel("Response")),
		nil, nil, nil,
		container.NewScroll(h.respLabel),
	)

	detailSplit := container.NewHSplit(reqPane, respPane)
	detailSplit.SetOffset(0.5)

	mainSplit := container.NewVSplit(
		container.NewBorder(filterBar, nil, nil, nil, h.table),
		detailSplit,
	)
	mainSplit.SetOffset(0.5)

	go func() {
		txs, err := h.projectStore.AllTransactions()
		if err != nil {
			logger.Error("history: load transactions: %v", err)
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			h.rows = txs
			h.mu.Unlock()
			h.applyFilter()
		})
	}()

	go h.watchUpdates()

	return mainSplit
}

func (h *historyTab) contextMenuItems(tx store.Transaction) []ContextMenuItem {
	return []ContextMenuItem{
		{
			Label: "Send to Repeater",
			Action: func() {
				h.sendToRepeater(tx)
			},
		},
		{
			Label: "Send to Intruder",
			Action: func() {
				h.sendToIntruder(tx)
			},
		},
		{
			Label: "Send to Loot",
			Action: func() {
				id := tx.ID
				h.loot.showAddDialog(&id, "", "")
			},
		},
		{
			Label: "Copy URL",
			Action: func() {
				h.win.Clipboard().SetContent(tx.URL)
			},
		},
		{
			Label: "Copy Request",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("history: copy request: %v", err)
						return
					}
					fyne.Do(func() {
						h.win.Clipboard().SetContent(formatRequest(*fullTx))
					})
				}()
			},
		},
		{
			Label: "Copy Response",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("history: copy response: %v", err)
						return
					}
					fyne.Do(func() {
						h.win.Clipboard().SetContent(formatResponse(*fullTx))
					})
				}()
			},
		},
	}
}

func (h *historyTab) sendToRepeater(tx store.Transaction) {
	host, portStr, err := net.SplitHostPort(tx.Host)
	if err != nil {
		host = tx.Host
		if tx.TLS {
			portStr = "443"
		} else {
			portStr = "80"
		}
	}
	port, _ := strconv.Atoi(portStr)
	path := pathOf(tx.URL)
	if len(path) > 20 {
		path = path[:20] + "..."
	}
	name := fmt.Sprintf("%s %s", tx.Method, path)
	h.repeater.AddTab(name, host, port, tx.TLS, formatRequest(tx))
}

func (h *historyTab) sendToIntruder(tx store.Transaction) {
	h.intruder.reqEditor.SetText(formatRequest(tx))
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

const maxDisplayBytes = 64 * 1024 // 64 KB

func (h *historyTab) showDetail(tx store.Transaction) {
	if len(tx.RespBody) > maxDisplayBytes {
		tx.RespBody = append(tx.RespBody[:maxDisplayBytes], []byte("\n... truncated")...)
	}
	if len(tx.ReqBody) > maxDisplayBytes {
		tx.ReqBody = append(tx.ReqBody[:maxDisplayBytes], []byte("\n... truncated")...)
	}
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
			searchable := strings.ToLower(tx.Host + tx.URL + tx.Method + strconv.Itoa(tx.StatusCode))
			if !strings.Contains(searchable, query) {
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

func (h *historyTab) watchUpdates() {
	for tx := range h.projectStore.Updates {
		transaction := tx
		fyne.Do(func() {
			h.mu.Lock()
			newRows := make([]store.Transaction, 0, len(h.rows))
			for _, row := range h.rows {
				if row.ID != transaction.ID {
					newRows = append(newRows, row)
				}
			}
			h.rows = append([]store.Transaction{transaction}, newRows...)
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

func prettyJSON(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	var jsonBuf bytes.Buffer
	if err := json.Indent(&jsonBuf, body, "", "  "); err != nil {
		return body
	}
	return jsonBuf.Bytes()
}

func formatRequest(tx store.Transaction) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s %s HTTP/1.1\r\n", tx.Method, pathOf(tx.URL))
	fmt.Fprintf(&builder, "Host: %s\r\n", tx.Host)
	keys := make([]string, 0, len(tx.ReqHeaders))
	for k := range tx.ReqHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, headerKey := range keys {
		for _, headerValue := range tx.ReqHeaders[headerKey] {
			fmt.Fprintf(&builder, "%s: %s\r\n", headerKey, headerValue)
		}
	}
	builder.WriteString("\r\n")
	if len(tx.ReqBody) > 0 {
		ct := tx.ReqHeaders.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			builder.Write(prettyJSON(tx.ReqBody))
		} else {
			builder.WriteString(string(tx.ReqBody))
		}
	}
	return builder.String()
}

func formatResponse(tx store.Transaction) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "HTTP/1.1 %d\r\n", tx.StatusCode)
	keys := make([]string, 0, len(tx.RespHeaders))
	for k := range tx.RespHeaders {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, headerKey := range keys {
		for _, headerValue := range tx.RespHeaders[headerKey] {
			fmt.Fprintf(&builder, "%s: %s\r\n", headerKey, headerValue)
		}
	}
	builder.WriteString("\r\n")
	if len(tx.RespBody) > 0 {
		ct := tx.RespHeaders.Get("Content-Type")
		if strings.Contains(ct, "application/json") {
			builder.Write(prettyJSON(tx.RespBody))
		} else {
			builder.WriteString(string(tx.RespBody))
		}
	}
	return builder.String()
}
