package ui

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

// ── Site map ──────────────────────────────────────────────────────────────────

type siteMapNode struct {
	children map[string]*siteMapNode
}

func newSiteMapNode() *siteMapNode {
	return &siteMapNode{children: make(map[string]*siteMapNode)}
}

type siteMap struct {
	mu          sync.RWMutex
	hosts       map[string]*siteMapNode
	scopeFilter func(host string) bool
}

func newSiteMap() *siteMap {
	return &siteMap{hosts: make(map[string]*siteMapNode)}
}

// add records the full host+path from a transaction into the site map.
func (sm *siteMap) add(tx store.Transaction) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if _, ok := sm.hosts[tx.Host]; !ok {
		sm.hosts[tx.Host] = newSiteMapNode()
	}
	node := sm.hosts[tx.Host]
	for _, seg := range splitPath(PathOf(tx.URL)) {
		if _, ok := node.children[seg]; !ok {
			node.children[seg] = newSiteMapNode()
		}
		node = node.children[seg]
	}
}

// clear removes all entries. Only called on history Clear.
func (sm *siteMap) clear() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.hosts = make(map[string]*siteMapNode)
}

func (sm *siteMap) childUIDs(uid widget.TreeNodeID) []widget.TreeNodeID {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if uid == "" {
		ids := make([]widget.TreeNodeID, 0, len(sm.hosts))
		for h := range sm.hosts {
			if sm.scopeFilter != nil && !sm.scopeFilter(h) {
				continue
			}
			ids = append(ids, "h:"+h)
		}
		slices.Sort(ids)
		return ids
	}
	host, path, ok := parseNodeID(uid)
	if !ok {
		return nil
	}
	node, ok := sm.hosts[host]
	if !ok {
		return nil
	}
	for _, seg := range splitPath(path) {
		child, ok := node.children[seg]
		if !ok {
			return nil
		}
		node = child
	}
	ids := make([]widget.TreeNodeID, 0, len(node.children))
	for seg := range node.children {
		var childPath string
		if path == "" {
			childPath = "/" + seg
		} else {
			childPath = path + "/" + seg
		}
		ids = append(ids, "p:"+host+"|"+childPath)
	}
	slices.Sort(ids)
	return ids
}

func (sm *siteMap) isBranch(uid widget.TreeNodeID) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if uid == "" {
		return true
	}
	host, path, ok := parseNodeID(uid)
	if !ok {
		return false
	}
	node, ok := sm.hosts[host]
	if !ok {
		return false
	}
	for _, seg := range splitPath(path) {
		child, ok := node.children[seg]
		if !ok {
			return false
		}
		node = child
	}
	return len(node.children) > 0
}

func labelFor(uid widget.TreeNodeID) string {
	host, path, ok := parseNodeID(uid)
	if !ok {
		return uid
	}
	if path == "" {
		return host
	}
	segs := splitPath(path)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + segs[len(segs)-1]
}

func parseNodeID(uid widget.TreeNodeID) (host, path string, ok bool) {
	switch {
	case strings.HasPrefix(uid, "h:"):
		return uid[2:], "", true
	case strings.HasPrefix(uid, "p:"):
		rest := uid[2:]
		before, after, ok0 := strings.Cut(rest, "|")
		if !ok0 {
			return "", "", false
		}
		return before, after, true
	}
	return "", "", false
}

func splitPath(path string) []string {
	var segs []string
	for s := range strings.SplitSeq(path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}

// ── History tab ───────────────────────────────────────────────────────────────

type historyTab struct {
	projectStore *store.Store
	win          fyne.Window
	repeater     *repeaterTab
	loot         *lootTab
	intruder     *intruderTab

	mu sync.RWMutex

	// displayed holds the rows currently shown in the table.
	// New traffic is prepended; older rows are appended via infinite scroll.
	displayed []store.Transaction

	// cursor is the lowest ID in displayed. The next page loads id < cursor.
	cursor uint64

	// maxID is the highest ID seen, used by pollMissed.
	maxID uint64

	// loadingMore prevents concurrent next-page loads.
	loadingMore bool

	// hasMore is false when the DB has no more rows below the cursor.
	hasMore bool

	// queryID increments on every loadFirstPage call. Goroutines check it
	// before writing results so stale responses from old queries are dropped.
	queryID uint64

	siteMap     *siteMap
	tree        *widget.Tree
	selectedUID widget.TreeNodeID

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
	{Header: "Time", Width: 220},
}

func newHistoryTab(projectStore *store.Store, win fyne.Window, repeater *repeaterTab, loot *lootTab, intruder *intruderTab) *historyTab {
	return &historyTab{
		projectStore: projectStore,
		win:          win,
		repeater:     repeater,
		loot:         loot,
		intruder:     intruder,
		siteMap:      newSiteMap(),
		hasMore:      true,
	}
}

func (h *historyTab) buildSiteMapPane() fyne.CanvasObject {
	header := container.NewHBox(newBoldLabel("Site Map"), layout.NewSpacer(), h.scopeBtn)
	return container.NewBorder(header, nil, nil, nil, h.tree)
}

// currentFilter builds a TransactionFilter from the current UI state.
func (h *historyTab) currentFilter() store.TransactionFilter {
	f := store.TransactionFilter{
		Search:       h.filterEntry.Text,
		ShowOutScope: h.showOutScope.Checked,
	}
	if h.selectedUID != "" {
		host, path, ok := parseNodeID(h.selectedUID)
		if ok {
			f.Host = host
			f.PathPrefix = path
		}
	}
	return f
}

// loadFirstPage resets the table to the first page matching the current filter.
// NEVER touches the site map — the site map is independent of pagination.
// Must be called from the Fyne main goroutine.
func (h *historyTab) loadFirstPage() {
	f := h.currentFilter()
	h.mu.Lock()
	h.queryID++
	localID := h.queryID
	h.mu.Unlock()

	go func() {
		txs, err := h.projectStore.TransactionsPage(0, f)
		if err != nil {
			logger.Error("history: load first page: %v", err)
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			// Drop stale results from a superseded query.
			if localID != h.queryID {
				h.mu.Unlock()
				return
			}
			h.displayed = txs
			h.hasMore = len(txs) == store.PageSize
			if len(txs) > 0 {
				h.cursor = txs[len(txs)-1].ID
				if txs[0].ID > h.maxID {
					h.maxID = txs[0].ID
				}
			} else {
				h.cursor = 0
				h.hasMore = false
			}
			h.mu.Unlock()
			h.table.ScrollToRow(0)
		})
	}()
}

// loadNextPage appends the next page of rows to the bottom of the table.
// NEVER touches the site map.
func (h *historyTab) loadNextPage() {
	h.mu.Lock()
	if h.loadingMore || !h.hasMore || h.cursor == 0 {
		h.mu.Unlock()
		return
	}
	cursor := h.cursor
	h.loadingMore = true
	h.mu.Unlock()

	f := h.currentFilter()
	go func() {
		txs, err := h.projectStore.TransactionsPage(cursor, f)
		if err != nil {
			logger.Error("history: load next page: %v", err)
			h.mu.Lock()
			h.loadingMore = false
			h.mu.Unlock()
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			h.displayed = append(h.displayed, txs...)
			h.hasMore = len(txs) == store.PageSize
			if len(txs) > 0 {
				h.cursor = txs[len(txs)-1].ID
			}
			h.loadingMore = false
			h.mu.Unlock()
			h.table.Refresh()
		})
	}()
}

func (h *historyTab) build() fyne.CanvasObject {
	h.filterEntry = widget.NewEntry()
	h.filterEntry.SetPlaceHolder("Filter — host, path, method, status...")
	h.filterEntry.OnChanged = func(_ string) { h.loadFirstPage() }

	h.showOutScope = widget.NewCheck("Show out-of-scope", func(checked bool) {
		if checked {
			h.siteMap.scopeFilter = nil
		} else {
			h.siteMap.scopeFilter = func(host string) bool {
				return h.projectStore.InScope(host)
			}
		}
		h.tree.ScrollToTop()
		h.tree.Refresh()
		h.loadFirstPage()
	})
	h.showOutScope.Checked = true

	h.scopeBtn = widget.NewButtonWithIcon("Scope", AppIcon("scope"), func() {
		showScopeDialog(h.projectStore, h.win, func() {
			h.tree.Refresh()
			h.loadFirstPage()
		})
	})

	clearBtn := widget.NewButtonWithIcon("Clear", AppIcon("delete"), func() {
		if err := h.projectStore.ClearHistory(); err != nil {
			logger.Error("clear history: %v", err)
			return
		}
		h.mu.Lock()
		h.displayed = nil
		h.cursor = 0
		h.maxID = 0
		h.hasMore = false
		h.mu.Unlock()
		// Clear is the ONLY place that resets the site map.
		h.siteMap.clear()
		h.selectedUID = ""
		h.tree.Refresh()
		h.table.Refresh()
		h.selectedTx = store.Transaction{}
		h.hasSelected = false
		h.reqLabel.SetText("")
		h.respLabel.SetText("")
	})

	exportHARBtn := widget.NewButtonWithIcon("Export HAR", AppIcon("document"), func() {
		h.exportHAR()
	})
	wsBtn := widget.NewButtonWithIcon("WebSockets", AppIcon("web"), func() {
		showWebSocketWindow(fyne.CurrentApp(), h.projectStore, h.win, h.repeater)
	})

	filterBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(h.showOutScope, exportHARBtn, wsBtn, clearBtn),
		h.filterEntry,
	)

	h.tree = widget.NewTree(
		h.siteMap.childUIDs,
		h.siteMap.isBranch,
		func(_ bool) fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(nil), widget.NewLabel("template"))
		},
		func(uid widget.TreeNodeID, branch bool, obj fyne.CanvasObject) {
			box := obj.(*fyne.Container)
			icon := box.Objects[0].(*widget.Icon)
			label := box.Objects[1].(*widget.Label)
			if strings.HasPrefix(uid, "h:") {
				icon.SetResource(AppIcon("web"))
			} else if branch {
				icon.SetResource(AppIcon("folder"))
			} else {
				if hasSuffix(labelFor(uid), ".jpg", ".jpeg", ".png", ".svg", ".gif") {
					icon.SetResource(AppIcon("media"))
				} else {
					icon.SetResource(AppIcon("document"))
				}
			}
			label.SetText(labelFor(uid))
		},
	)
	h.tree.OnSelected = func(uid widget.TreeNodeID) {
		h.selectedUID = uid
		// Site map selection filters the table only — never touches the site map.
		h.loadFirstPage()
	}
	// OnUnselected intentionally not set — see original comment.

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
		return len(h.displayed)
	}
	h.table.CellValue = func(row, col int) string {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.displayed) {
			return ""
		}
		return h.cellText(h.displayed[row], col)
	}
	h.table.CellStyle = func(row, col int) widget.Importance {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.displayed) {
			return widget.MediumImportance
		}
		tx := h.displayed[row]
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
		if row >= len(h.displayed) {
			return 0
		}
		return int64(h.displayed[row].ID)
	}
	h.table.OnSelect = func(row int) {
		h.mu.RLock()
		if row >= len(h.displayed) {
			h.mu.RUnlock()
			return
		}
		tx := h.displayed[row]
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
		if row >= len(h.displayed) {
			h.mu.RUnlock()
			return nil
		}
		tx := h.displayed[row]
		h.mu.RUnlock()
		return h.contextMenuItems(tx)
	}
	h.table.OnNearBottom = func() {
		h.loadNextPage()
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

	topSplit := container.NewHSplit(
		h.buildSiteMapPane(),
		container.NewBorder(filterBar, nil, nil, nil, tableObj),
	)
	topSplit.SetOffset(0.25)

	mainSplit := container.NewVSplit(topSplit, detailSplit)
	mainSplit.SetOffset(0.55)

	// ── Startup: build full site map then load first page ────────────────────
	//
	// SiteMapEntries loads host+url for every historical row so the site map
	// shows all paths immediately — not just what's in the current page.
	// The site map is never cleared or modified by filter/page changes.
	// Only the Clear button resets it.

	go func() {
		entries, err := h.projectStore.SiteMapEntries()
		if err != nil {
			logger.Error("history: build site map: %v", err)
			return
		}
		fyne.Do(func() {
			for _, e := range entries {
				h.siteMap.add(store.Transaction{Host: e.Host, URL: e.URL})
			}
			h.tree.Refresh()
		})
	}()

	go func() {
		txs, err := h.projectStore.TransactionsPage(0, store.TransactionFilter{ShowOutScope: true})
		if err != nil {
			logger.Error("history: initial page load: %v", err)
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			h.displayed = txs
			h.hasMore = len(txs) == store.PageSize
			if len(txs) > 0 {
				h.cursor = txs[len(txs)-1].ID
				h.maxID = txs[0].ID
			}
			h.mu.Unlock()
			h.table.ScrollToRow(0)
		})
	}()

	go h.watchUpdates()
	go h.pollMissed()

	return mainSplit
}

// ── Live updates ──────────────────────────────────────────────────────────────

func (h *historyTab) watchUpdates() {
	defer recoverPanic("watchUpdates")
	for tx := range h.projectStore.Updates {
		transaction := tx
		fyne.Do(func() {
			h.mu.Lock()
			for _, r := range h.displayed {
				if r.ID == transaction.ID {
					h.mu.Unlock()
					return
				}
			}
			h.displayed = append([]store.Transaction{transaction}, h.displayed...)
			if transaction.ID > h.maxID {
				h.maxID = transaction.ID
			}
			// Keep cursor pointing at the last (oldest) displayed row so
			// the next page load doesn't skip or duplicate rows.
			if len(h.displayed) > 0 {
				h.cursor = h.displayed[len(h.displayed)-1].ID
			}
			isSelected := h.hasSelected && h.selectedTx.ID == transaction.ID
			if isSelected {
				h.selectedTx = transaction
			}
			h.mu.Unlock()

			// Always add to site map — independent of what's displayed.
			h.siteMap.add(transaction)
			h.tree.Refresh()
			h.table.Refresh()
			if isSelected {
				h.showDetail(transaction)
			}
		})
	}
}

func (h *historyTab) pollMissed() {
	defer recoverPanic("pollMissed")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		h.mu.RLock()
		afterID := h.maxID
		h.mu.RUnlock()

		txs, err := h.projectStore.TransactionsSince(afterID)
		if err != nil {
			logger.Error("history: poll: %v", err)
			continue
		}
		if len(txs) == 0 {
			continue
		}

		fyne.Do(func() {
			h.mu.Lock()
			known := make(map[uint64]bool, len(h.displayed))
			for _, r := range h.displayed {
				known[r.ID] = true
			}
			var added bool
			for _, tx := range txs {
				if known[tx.ID] {
					continue
				}
				h.displayed = append([]store.Transaction{tx}, h.displayed...)
				if tx.ID > h.maxID {
					h.maxID = tx.ID
				}
				added = true
				h.siteMap.add(tx)
			}
			// Keep cursor pointing at the last (oldest) displayed row.
			if added && len(h.displayed) > 0 {
				h.cursor = h.displayed[len(h.displayed)-1].ID
			}
			h.mu.Unlock()

			if added {
				h.tree.Refresh()
				h.table.Refresh()
			}
		})
	}
}

// ── Detail ────────────────────────────────────────────────────────────────────

func (h *historyTab) showDetail(tx store.Transaction) {
	tx.RespBody = TruncateBody(tx.RespBody)
	tx.ReqBody = TruncateBody(tx.ReqBody)
	h.reqLabel.SetText(FormatStoreRequest(tx))
	h.respLabel.SetText(FormatStoreResponse(tx))
}

// ── Context menu ──────────────────────────────────────────────────────────────

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
		{Label: "Send to Intruder", Action: func() { h.sendToIntruder(tx) }},
		{
			Label: "Send to Loot",
			Action: func() {
				id := tx.ID
				h.loot.showAddDialog(&id, "", "")
			},
		},
		{Label: "Copy URL", Action: func() { fyne.CurrentApp().Clipboard().SetContent(tx.URL) }},
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
						fyne.CurrentApp().Clipboard().SetContent(FormatStoreRequest(*fullTx))
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
						fyne.CurrentApp().Clipboard().SetContent(FormatStoreResponse(*fullTx))
					})
				}()
			},
		},
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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
	h.repeater.AddTab(fmt.Sprintf("%s %s", tx.Method, path), host, port, tx.TLS, FormatStoreRequest(tx))
}

func (h *historyTab) sendToIntruder(tx store.Transaction) {
	h.intruder.reqEditor.SetText(FormatStoreRequest(tx))
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
	case 6:
		return tx.Timestamp.Local().Format("2006-01-02 15:04:05")
	}
	return ""
}
