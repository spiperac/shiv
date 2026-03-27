package ui

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	scopeFilter func(host string) bool // nil = show all
}

func newSiteMap() *siteMap {
	return &siteMap{hosts: make(map[string]*siteMapNode)}
}

func (sm *siteMap) add(tx store.Transaction) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.hosts[tx.Host]; !ok {
		sm.hosts[tx.Host] = newSiteMapNode()
	}
	node := sm.hosts[tx.Host]
	// Requests to "/" have no segments — they are represented by the host
	// node itself. No child node is created for root, avoiding the duplicate
	// "/" nesting bug.
	for _, seg := range splitPath(PathOf(tx.URL)) {
		if _, ok := node.children[seg]; !ok {
			node.children[seg] = newSiteMapNode()
		}
		node = node.children[seg]
	}
}

func (sm *siteMap) clear() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.hosts = make(map[string]*siteMapNode)
}

// Node ID scheme:
//
//	""                            → invisible root
//	"h:example.com:443"           → host branch
//	"p:example.com:443|/shop"     → path node /shop
//	"p:example.com:443|/shop/brands" → path node /shop/brands
//
// The separator between host and path is "|" to avoid clashing with ":" in
// host:port strings.
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
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
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
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
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
		i := strings.Index(rest, "|")
		if i < 0 {
			return "", "", false
		}
		return rest[:i], rest[i+1:], true
	}
	return "", "", false
}

func splitPath(path string) []string {
	var segs []string
	for _, s := range strings.Split(path, "/") {
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

	mu       sync.RWMutex
	rows     []store.Transaction
	filtered []store.Transaction

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
	}
}

func (h *historyTab) build() fyne.CanvasObject {
	h.filterEntry = widget.NewEntry()
	h.filterEntry.SetPlaceHolder("Filter — host, path, method, status...")
	h.filterEntry.OnChanged = func(_ string) { h.applyFilter() }

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
		h.applyFilter()
	})
	h.showOutScope.Checked = true

	h.scopeBtn = widget.NewButtonWithIcon("Scope", AppIcon("scope"), func() {
		showScopeDialog(h.projectStore, h.win, func() {
			h.tree.Refresh()
			h.applyFilter()
		})
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
		h.siteMap.clear()
		h.selectedUID = ""
		h.tree.Refresh()
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
		h.applyFilter()
	}
	// OnUnselected is intentionally not set. Fyne fires OnUnselected before
	// OnSelected when the user clicks a different node. Setting it would blank
	// selectedUID between the two events, causing the table to flash empty.
	// The previous selectedUID is overwritten by OnSelected on the next node.

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

	topSplit := container.NewHSplit(
		container.NewBorder(
			container.NewHBox(newBoldLabel("Site Map"), layout.NewSpacer(), widget.NewButtonWithIcon("", AppIcon("scope"), func() {
				if h.selectedUID == "" {
					return
				}
				host, _, ok := parseNodeID(h.selectedUID)
				if !ok {
					return
				}
				bareHost, _, err := net.SplitHostPort(host)
				if err != nil {
					bareHost = host
				}
				h.projectStore.AddScopeEntry(bareHost)
				h.mu.Lock()
				for i := range h.rows {
					if h.rows[i].Host == host {
						h.rows[i].InScope = true
					}
				}
				h.mu.Unlock()
				h.tree.Refresh()
				h.applyFilter()
			})),
			nil, nil, nil,
			h.tree,
		),
		container.NewBorder(filterBar, nil, nil, nil, tableObj),
	)
	topSplit.SetOffset(0.25)

	mainSplit := container.NewVSplit(topSplit, detailSplit)
	mainSplit.SetOffset(0.55)

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
			for _, tx := range txs {
				h.siteMap.add(tx)
			}
			h.tree.Refresh()
			h.applyFilter()
		})
	}()

	go h.watchUpdates()

	return mainSplit
}

// ── Filter ────────────────────────────────────────────────────────────────────

func (h *historyTab) applyFilter() {
	query := strings.ToLower(h.filterEntry.Text)
	showOut := h.showOutScope.Checked
	terms := strings.Fields(query)
	uid := h.selectedUID

	var filtered []store.Transaction
	h.mu.RLock()
	for _, tx := range h.rows {
		if !showOut && !tx.InScope {
			continue
		}
		if uid != "" {
			host, path, ok := parseNodeID(uid)
			if ok {
				if tx.Host != host {
					continue
				}
				if path != "" {
					txPath := PathOf(tx.URL)
					// Exact match or child path.
					// /login must not match /login-redirect.
					if txPath != path && !strings.HasPrefix(txPath, path+"/") {
						continue
					}
				}
			}
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

// ── Live updates ──────────────────────────────────────────────────────────────

func (h *historyTab) watchUpdates() {
	defer recoverPanic("watchUpdates")
	for tx := range h.projectStore.Updates {
		transaction := tx
		fyne.Do(func() {
			h.mu.Lock()
			h.rows = append([]store.Transaction{transaction}, h.rows...)
			if len(h.rows) > 10000 {
				h.rows = h.rows[:10000]
			}
			isSelected := h.hasSelected && h.selectedTx.ID == transaction.ID
			if isSelected {
				h.selectedTx = transaction
			}
			h.mu.Unlock()

			h.siteMap.add(transaction)
			h.tree.Refresh()
			h.applyFilter()
			if isSelected {
				h.showDetail(transaction)
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
