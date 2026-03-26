package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
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

	table        *widgets.DataTable
	filterEntry  *widget.Entry
	showOutScope *widget.Check
	reqLabel     *widgets.TextView
	respLabel    *widgets.TextView
	scopeBtn     *widget.Button

	selectedTx  store.Transaction
	hasSelected bool
}

var tableColumns = []widgets.DataTableColumn{
	{Header: "Method", Width: 80},
	{Header: "Host", Width: 200},
	{Header: "Path", Width: 300},
	{Header: "Status", Width: 70},
	{Header: "Size", Width: 90},
	{Header: "Duration", Width: 90},
}

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

	h.reqLabel = widgets.NewTextView()
	h.reqLabel.SetWindow(h.win)
	h.respLabel = widgets.NewTextView()
	h.respLabel.SetWindow(h.win)

	h.table = widgets.NewDataTable()
	h.table.SetWindow(h.win)
	h.table.Columns = tableColumns
	h.table.RowCount = func() int {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return len(h.filtered)
	}
	h.table.CellValue = func(row, col int) string {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.filtered) {
			return ""
		}
		return h.cellText(h.filtered[row], col)
	}
	h.table.CellStyle = func(row, col int) widget.Importance {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.filtered) {
			return widget.MediumImportance
		}
		tx := h.filtered[row]
		switch col {
		case 0:
			switch tx.Method {
			case "POST", "PUT", "PATCH", "DELETE":
				return widget.WarningImportance
			}
		case 3:
			switch {
			case tx.StatusCode >= 500:
				return widget.DangerImportance
			case tx.StatusCode >= 400:
				return widget.WarningImportance
			case tx.StatusCode >= 300:
				return widget.LowImportance
			case tx.StatusCode >= 200:
				return widget.SuccessImportance
			}
		}
		return widget.MediumImportance
	}
	h.table.RowID = func(row int) int64 {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.filtered) {
			return 0
		}
		return int64(h.filtered[row].ID)
	}
	h.table.OnSelect = func(row int) {
		h.mu.RLock()
		if row >= len(h.filtered) {
			h.mu.RUnlock()
			return
		}
		tx := h.filtered[row]
		h.mu.RUnlock()

		h.selectedTx = tx
		h.hasSelected = true

		go func() {
			fullTx, err := h.projectStore.GetTransaction(tx.ID)
			if err != nil {
				logger.Error("history: get transaction: %v", err)
				return
			}
			fyne.Do(func() {
				h.selectedTx = *fullTx
				h.showDetail(*fullTx)
			})
		}()
	}
	h.table.MenuItems = func(row int) []widgets.ContextMenuItem {
		h.mu.RLock()
		if row >= len(h.filtered) {
			h.mu.RUnlock()
			return nil
		}
		tx := h.filtered[row]
		h.mu.RUnlock()
		return h.contextMenuItems(tx)
	}

	tableObj := h.table.Build()

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		if !h.hasSelected {
			return
		}
		showInspectorDialog(h.selectedTx, h.win)
	})

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil, h.reqLabel.Build())
	respPane := container.NewBorder(
		container.NewBorder(nil, nil, nil, inspectBtn, newBoldLabel("Response")),
		nil, nil, nil,
		h.respLabel.Build(),
	)

	detailSplit := container.NewHSplit(reqPane, respPane)
	detailSplit.SetOffset(0.5)

	mainSplit := container.NewVSplit(
		container.NewBorder(filterBar, nil, nil, nil, tableObj),
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

func (h *historyTab) contextMenuItems(tx store.Transaction) []widgets.ContextMenuItem {
	return []widgets.ContextMenuItem{
		{
			Label: "Send to Repeater",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("send to repeater: %v", err)
						return
					}
					fyne.Do(func() { h.sendToRepeater(*fullTx) })
				}()
			},
		},
		{
			Label:  "Send to Intruder",
			Action: func() { h.sendToIntruder(tx) },
		},
		{
			Label: "Send to Loot",
			Action: func() {
				id := tx.ID
				h.loot.showAddDialog(&id, "", "")
			},
		},
		{
			Label:  "Copy URL",
			Action: func() { fyne.CurrentApp().Clipboard().SetContent(tx.URL) },
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
						fyne.CurrentApp().Clipboard().SetContent(FormatRequest(*fullTx))
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
						fyne.CurrentApp().Clipboard().SetContent(FormatResponse(*fullTx))
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
	path := PathOf(tx.URL)
	if len(path) > 20 {
		path = path[:20] + "..."
	}
	h.repeater.AddTab(fmt.Sprintf("%s %s", tx.Method, path), host, port, tx.TLS, FormatRequest(tx))
}

func (h *historyTab) sendToIntruder(tx store.Transaction) {
	h.intruder.reqEditor.SetText(FormatRequest(tx))
}

func (h *historyTab) cellText(tx store.Transaction, col int) string {
	switch col {
	case 0:
		return tx.Method
	case 1:
		return tx.Host
	case 2:
		return PathOf(tx.URL)
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
	tx.RespBody = TruncateBody(tx.RespBody)
	tx.ReqBody = TruncateBody(tx.ReqBody)
	h.reqLabel.SetText(FormatRequest(tx))
	h.respLabel.SetText(FormatResponse(tx))
}

func (h *historyTab) applyFilter() {
	query := strings.ToLower(h.filterEntry.Text)
	showOut := h.showOutScope.Checked
	terms := strings.Fields(query)

	var filtered []store.Transaction
	h.mu.RLock()
	for _, tx := range h.rows {
		if !showOut && !tx.InScope {
			continue
		}
		if len(terms) > 0 {
			searchable := strings.ToLower(tx.Host + tx.URL + tx.Method + strconv.Itoa(tx.StatusCode))
			match := true
			for _, term := range terms {
				if !strings.Contains(searchable, term) {
					match = false
					break
				}
			}
			if !match {
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
			isSelected := h.hasSelected && h.selectedTx.ID == transaction.ID
			if isSelected {
				h.selectedTx = transaction
			}
			h.mu.Unlock()
			h.applyFilter()
			if isSelected {
				h.showDetail(transaction)
			}
		})
	}
}
