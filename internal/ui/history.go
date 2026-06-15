package ui

import (
	"fmt"
	"image/color"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

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
	textFilter  string
}

func newSiteMap() *siteMap {
	return &siteMap{hosts: make(map[string]*siteMapNode)}
}

func (sm *siteMap) add(tx store.Transaction) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	key := hostPortKey(tx.Host, tx.Port)
	if _, ok := sm.hosts[key]; !ok {
		sm.hosts[key] = newSiteMapNode()
	}
	node := sm.hosts[key]
	for _, seg := range splitPath(treePathOf(tx.URL)) {
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

func (sm *siteMap) childUIDs(uid widget.TreeNodeID) []widget.TreeNodeID {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if uid == "" {
		ids := make([]widget.TreeNodeID, 0, len(sm.hosts))
		for key := range sm.hosts {
			// key is "host:port"; extract bare host for scope and text filter checks.
			h, portStr, err := net.SplitHostPort(key)
			if err != nil || h == "" {
				h = key
			}
			if sm.scopeFilter != nil && !sm.scopeFilter(h) {
				continue
			}
			if sm.textFilter != "" {
				port, _ := strconv.Atoi(portStr)
				display := strings.ToLower(displayHost(h, port))
				if !strings.Contains(display, strings.ToLower(sm.textFilter)) {
					continue
				}
			}
			ids = append(ids, "h:"+key)
		}
		slices.Sort(ids)
		return ids
	}
	host, port, path, ok := parseNodeID(uid)
	if !ok {
		return nil
	}
	node, ok := sm.hosts[hostPortKey(host, port)]
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
		ids = append(ids, "p:"+hostPortKey(host, port)+"|"+childPath)
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
	host, port, path, ok := parseNodeID(uid)
	if !ok {
		return false
	}
	node, ok := sm.hosts[hostPortKey(host, port)]
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
	host, port, path, ok := parseNodeID(uid)
	if !ok {
		return uid
	}
	if path == "" {
		return displayHost(host, port)
	}
	segs := splitPath(path)
	return "/" + segs[len(segs)-1]
}

// parseNodeID decodes a tree node UID into its components.
// Host nodes:  "h:example.com:1337"  → host="example.com", port=1337, path=""
// Path nodes:  "p:example.com:1337|/api/v1" → host="example.com", port=1337, path="/api/v1"
func parseNodeID(uid widget.TreeNodeID) (host string, port int, path string, ok bool) {
	switch {
	case strings.HasPrefix(uid, "h:"):
		h, p, err := net.SplitHostPort(uid[2:])
		if err != nil {
			// Backward-compat: bare hostname with no port (old DB entries).
			return uid[2:], 0, "", true
		}
		port, _ = strconv.Atoi(p)
		return h, port, "", true
	case strings.HasPrefix(uid, "p:"):
		hostPort, p, ok0 := strings.Cut(uid[2:], "|")
		if !ok0 {
			return "", 0, "", false
		}
		h, portStr, err := net.SplitHostPort(hostPort)
		if err != nil {
			return hostPort, 0, p, true
		}
		port, _ = strconv.Atoi(portStr)
		return h, port, p, true
	}
	return "", 0, "", false
}

// hostPortKey is the internal site-map map key for a host+port pair.
// It is always "host:port" regardless of whether the port is default.
func hostPortKey(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// displayHost formats a host+port for display. Default ports (80, 443) are
// omitted since they add no information and clutter the site map.
func displayHost(host string, port int) string {
	if port == 0 || port == 80 || port == 443 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
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

// treePathOf returns the path portion of a URL with query string stripped.
// Used for site map tree nodes so that /api?page=1 and /api?page=2 map to
// the same /api node instead of creating separate leaves per unique query.
func treePathOf(rawURL string) string {
	p := PathOf(rawURL)
	if idx := strings.IndexByte(p, '?'); idx >= 0 {
		return p[:idx]
	}
	return p
}

var staticExtensions = []string{
	".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".ico", ".svg",
	".woff", ".woff2", ".ttf", ".eot", ".map", ".webp", ".avif",
}

func matchesFilter(tx store.Transaction, f store.TransactionFilter) bool {
	if f.Host != "" && tx.Host != f.Host {
		return false
	}
	if f.Port != 0 && tx.Port != f.Port {
		return false
	}
	if f.PathPrefix != "" {
		p := treePathOf(tx.URL)
		if p != f.PathPrefix && !strings.HasPrefix(p, f.PathPrefix+"/") {
			return false
		}
	}
	if !f.ShowOutScope && !tx.InScope {
		return false
	}
	if len(f.Methods) > 0 {
		found := false
		for _, m := range f.Methods {
			if strings.EqualFold(tx.Method, m) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.StatusMin > 0 && tx.StatusCode < f.StatusMin {
		return false
	}
	if f.StatusMax > 0 && tx.StatusCode > f.StatusMax {
		return false
	}
	if f.HideStatic {
		p := strings.ToLower(treePathOf(tx.URL))
		for _, ext := range staticExtensions {
			if strings.HasSuffix(p, ext) {
				return false
			}
		}
	}
	return true
}

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

	table           *widgets.DataTable
	filterEntry     *widget.Entry
	treeFilterEntry *widget.Entry
	showOutScope    *widget.Check
	methodSelect    *widget.Select
	statusSelect    *widget.Select
	hideStatic      *widget.Check
	reqLabel        *widgets.TextView
	respLabel       *widgets.TextView
	scopeBtn        *widget.Button
	deleteBtn       *widget.Button
	reqCopyBtn      *widget.Button
	respCopyBtn     *widget.Button
	respViewFullBtn *widget.Button

	selectedTx  store.Transaction
	hasSelected bool

	annotationsMu sync.RWMutex
	annotations   map[uint64]store.Annotation
}

var tableColumns = []widgets.DataTableColumn{
	{Header: "Method", Width: 80},
	{Header: "Host", Width: 180},
	{Header: "Path", Width: 260},
	{Header: "Status", Width: 70},
	{Header: "Proto", Width: 80},
	{Header: "Size", Width: 80},
	{Header: "Duration", Width: 80},
	{Header: "Time", Width: 160},
	{Header: "Comment", Width: 160},
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
		annotations:  make(map[uint64]store.Annotation),
	}
}

func (h *historyTab) currentFilter() store.TransactionFilter {
	var f store.TransactionFilter
	if h.filterEntry != nil {
		f.Search = h.filterEntry.Text
	}
	if h.showOutScope != nil {
		f.ShowOutScope = h.showOutScope.Checked
	}
	if h.hideStatic != nil {
		f.HideStatic = h.hideStatic.Checked
	}
	if h.methodSelect != nil {
		if sel := h.methodSelect.Selected; sel != "" && sel != "All" {
			f.Methods = []string{sel}
		}
	}
	if h.statusSelect != nil {
		switch h.statusSelect.Selected {
		case "2xx":
			f.StatusMin, f.StatusMax = 200, 299
		case "3xx":
			f.StatusMin, f.StatusMax = 300, 399
		case "4xx":
			f.StatusMin, f.StatusMax = 400, 499
		case "5xx":
			f.StatusMin, f.StatusMax = 500, 599
		}
	}

	if h.selectedUID != "" {
		host, port, path, ok := parseNodeID(h.selectedUID)
		if ok {
			f.Host = host
			f.Port = port
			f.PathPrefix = path
		}
	}
	return f
}

// fetchPage fetches a page of transactions from the DB and applies
// path-prefix filtering. Returns filtered results and unfiltered count.
func (h *historyTab) fetchPage(cursor uint64, f store.TransactionFilter) ([]store.Transaction, int, error) {
	txs, err := h.projectStore.TransactionsPage(cursor, f)
	if err != nil {
		return nil, 0, err
	}
	fullLen := len(txs)
	if f.PathPrefix != "" || f.HideStatic {
		filtered := make([]store.Transaction, 0, len(txs))
		for _, tx := range txs {
			if matchesFilter(tx, f) {
				filtered = append(filtered, tx)
			}
		}
		txs = filtered
	}
	return txs, fullLen, nil
}

func (h *historyTab) loadFirstPage() {
	f := h.currentFilter()
	h.mu.Lock()
	h.queryID++
	localID := h.queryID
	h.mu.Unlock()

	go func() {
		txs, fullLen, err := h.fetchPage(0, f)
		if err != nil {
			logger.Error("history: load first page: %v", err)
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			if localID != h.queryID {
				h.mu.Unlock()
				return
			}
			h.loadingMore = false
			h.displayed = txs
			h.hasMore = fullLen == store.PageSize
			if len(txs) > 0 {
				h.cursor = txs[len(txs)-1].ID
			} else {
				h.cursor = 0
				h.hasMore = false
			}
			h.mu.Unlock()
			h.table.ScrollToRow(0)
		})
	}()
}

func (h *historyTab) loadNextPage() {
	h.mu.Lock()
	if h.loadingMore || !h.hasMore || h.cursor == 0 {
		h.mu.Unlock()
		return
	}
	cursor := h.cursor
	localID := h.queryID
	h.loadingMore = true
	h.mu.Unlock()

	f := h.currentFilter()
	go func() {
		txs, fullLen, err := h.fetchPage(cursor, f)
		if err != nil {
			logger.Error("history: load next page: %v", err)
			h.mu.Lock()
			h.loadingMore = false
			h.mu.Unlock()
			return
		}
		fyne.Do(func() {
			h.mu.Lock()
			if localID != h.queryID {
				h.loadingMore = false
				h.mu.Unlock()
				return
			}
			h.displayed = append(h.displayed, txs...)
			h.hasMore = fullLen == store.PageSize
			if len(txs) > 0 {
				h.cursor = txs[len(txs)-1].ID
			}
			h.loadingMore = false
			h.mu.Unlock()
			h.table.Refresh()
		})
	}()
}

func (h *historyTab) buildFilterBar() fyne.CanvasObject {
	h.filterEntry = widget.NewEntry()
	h.filterEntry.SetPlaceHolder("Filter — host, path, method, status...")
	h.filterEntry.OnChanged = func(_ string) { h.loadFirstPage() }

	h.methodSelect = widget.NewSelect(
		[]string{"All", "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"},
		func(_ string) { h.loadFirstPage() },
	)
	h.methodSelect.SetSelected("All")

	h.statusSelect = widget.NewSelect(
		[]string{"All", "2xx", "3xx", "4xx", "5xx"},
		func(_ string) { h.loadFirstPage() },
	)
	h.statusSelect.SetSelected("All")

	h.hideStatic = widget.NewCheck("Hide static", func(_ bool) { h.loadFirstPage() })

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

	exportHARBtn := widget.NewButtonWithIcon("Export HAR", AppIcon("document"), func() {
		h.exportHAR()
	})
	wsBtn := widget.NewButtonWithIcon("WebSockets", AppIcon("web"), func() {
		showWebSocketWindow(fyne.CurrentApp(), h.projectStore, h.win, h.repeater)
	})
	clearBtn := widget.NewButtonWithIcon("Clear", AppIcon("delete"), func() {
		h.clearHistory()
	})

	filterRow2 := container.NewBorder(nil, nil, nil,
		container.NewHBox(h.showOutScope, h.hideStatic, exportHARBtn, wsBtn, clearBtn),
		container.NewHBox(
			widget.NewLabel("Method:"), h.methodSelect,
			widget.NewLabel("Status:"), h.statusSelect,
		),
	)
	return container.NewVBox(
		container.NewBorder(nil, nil, nil, nil, h.filterEntry),
		filterRow2,
	)
}

func (h *historyTab) deleteSelectedHost() {
	if h.selectedUID == "" || !strings.HasPrefix(h.selectedUID, "h:") {
		return
	}
	host, port, _, ok := parseNodeID(h.selectedUID)
	if !ok {
		return
	}
	if err := h.projectStore.DeleteTransactionsByHostPort(host, port); err != nil {
		logger.Error("history: delete %s:%d: %v", host, port, err)
		return
	}
	h.siteMap.mu.Lock()
	delete(h.siteMap.hosts, hostPortKey(host, port))
	h.siteMap.mu.Unlock()
	h.selectedUID = ""
	h.mu.Lock()
	h.displayed = nil
	h.cursor = 0
	h.hasMore = true
	h.mu.Unlock()
	h.tree.UnselectAll()
	h.tree.Refresh()
	h.deleteBtn.Disable()
	h.loadFirstPage()
}

func (h *historyTab) updateTreeNode(uid widget.TreeNodeID, branch bool, obj fyne.CanvasObject) {
	box := obj.(*fyne.Container)
	icon := box.Objects[0].(*widget.Icon)
	label := box.Objects[1].(*widget.Label)
	if strings.HasPrefix(uid, "h:") {
		icon.SetResource(AppIcon("web"))
	} else if branch {
		icon.SetResource(AppIcon("folder"))
	} else if hasSuffix(labelFor(uid), ".jpg", ".jpeg", ".png", ".svg", ".gif") {
		icon.SetResource(AppIcon("media"))
	} else {
		icon.SetResource(AppIcon("document"))
	}
	label.SetText(labelFor(uid))
}

func (h *historyTab) buildSiteMap() fyne.CanvasObject {
	h.scopeBtn = widget.NewButtonWithIcon("Scope", AppIcon("scope"), func() {
		showScopeDialog(h.projectStore, h.win, func() {
			h.tree.Refresh()
			h.loadFirstPage()
		})
	})

	h.deleteBtn = widget.NewButtonWithIcon("", AppIcon("delete"), func() {
		h.deleteSelectedHost()
	})
	h.deleteBtn.Disable()

	h.tree = widget.NewTree(
		h.siteMap.childUIDs,
		h.siteMap.isBranch,
		func(_ bool) fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(nil), widget.NewLabel("template"))
		},
		h.updateTreeNode,
	)
	h.tree.OnSelected = func(uid widget.TreeNodeID) {
		h.selectedUID = uid
		if strings.HasPrefix(uid, "h:") {
			h.deleteBtn.Enable()
		} else {
			h.deleteBtn.Disable()
		}
		h.loadFirstPage()
	}

	h.treeFilterEntry = widget.NewEntry()
	h.treeFilterEntry.SetPlaceHolder("Filter hosts...")
	h.treeFilterEntry.OnChanged = func(text string) {
		h.siteMap.mu.Lock()
		h.siteMap.textFilter = text
		h.siteMap.mu.Unlock()
		h.tree.Refresh()
	}

	allBtn := widget.NewButton("All", func() {
		h.selectedUID = ""
		h.tree.UnselectAll()
		h.deleteBtn.Disable()
		h.loadFirstPage()
	})

	siteMapHeader := container.NewHBox(newBoldLabel("Site Map"), layout.NewSpacer(), allBtn, h.deleteBtn, h.scopeBtn)
	return container.NewBorder(
		container.NewVBox(siteMapHeader, h.treeFilterEntry),
		nil, nil, nil,
		h.tree,
	)
}

func (h *historyTab) tableCellStyle(row, col int) widget.Importance {
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

func (h *historyTab) tableOnSelect(row int) {
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

func (h *historyTab) tableRowBgColor(row int) color.Color {
	h.mu.RLock()
	if row >= len(h.displayed) {
		h.mu.RUnlock()
		return nil
	}
	id := h.displayed[row].ID
	h.mu.RUnlock()
	h.annotationsMu.RLock()
	a, ok := h.annotations[id]
	h.annotationsMu.RUnlock()
	if !ok || a.Color == "" {
		return nil
	}
	return annotationColor(a.Color)
}

func (h *historyTab) buildTable() fyne.CanvasObject {
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
	h.table.CellStyle = h.tableCellStyle
	h.table.RowID = func(row int) int64 {
		h.mu.RLock()
		defer h.mu.RUnlock()
		if row >= len(h.displayed) {
			return 0
		}
		return int64(h.displayed[row].ID)
	}
	h.table.OnSelect = h.tableOnSelect
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
	h.table.RowBgColor = h.tableRowBgColor
	return h.table.Build()
}

func (h *historyTab) buildDetailPane() fyne.CanvasObject {
	h.reqLabel = widgets.NewTextView()
	h.reqLabel.SetWindow(h.win)
	h.respLabel = widgets.NewTextView()
	h.respLabel.SetWindow(h.win)

	h.reqCopyBtn = widget.NewButton("Copy", func() {
		fyne.CurrentApp().Clipboard().SetContent(FormatStoreRequest(h.selectedTx))
	})
	h.reqCopyBtn.Disable()

	h.respCopyBtn = widget.NewButton("Copy", func() {
		fyne.CurrentApp().Clipboard().SetContent(FormatStoreResponse(h.selectedTx))
	})
	h.respCopyBtn.Disable()

	h.respViewFullBtn = widget.NewButton("View Full", func() {
		showFullBodyDialog(h.selectedTx.RespBody, h.win)
	})
	h.respViewFullBtn.Disable()
	h.respViewFullBtn.Hide()

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		if !h.hasSelected {
			return
		}
		showInspectorDialog(h.selectedTx, h.win)
	})

	reqPane := container.NewBorder(
		paneHeader("Request", h.reqCopyBtn),
		nil, nil, nil, h.reqLabel.Build(),
	)
	respPane := container.NewBorder(
		paneHeader("Response", h.respViewFullBtn, h.respCopyBtn, inspectBtn),
		nil, nil, nil,
		h.respLabel.Build(),
	)

	detailSplit := container.NewHSplit(reqPane, respPane)
	detailSplit.SetOffset(0.5)
	return detailSplit
}

func (h *historyTab) loadSiteMapEntries() {
	entries, err := h.projectStore.SiteMapEntries()
	if err != nil {
		logger.Error("history: build site map: %v", err)
		return
	}
	fyne.Do(func() {
		for _, e := range entries {
			h.siteMap.add(store.Transaction{Host: e.Host, Port: e.Port, URL: e.URL})
		}
		h.tree.Refresh()
	})
}

func (h *historyTab) loadAnnotations() {
	all, err := h.projectStore.AllAnnotations()
	if err != nil {
		logger.Error("history: load annotations: %v", err)
		return
	}
	fyne.Do(func() {
		h.annotationsMu.Lock()
		for _, a := range all {
			h.annotations[a.HistoryID] = a
		}
		h.annotationsMu.Unlock()
		h.table.Refresh()
	})
}

func (h *historyTab) clearHistory() {
	if err := h.projectStore.ClearHistory(); err != nil {
		logger.Error("clear history: %v", err)
		return
	}
	h.mu.Lock()
	h.displayed = nil
	h.cursor = 0
	h.hasMore = false
	h.mu.Unlock()
	h.siteMap.clear()
	h.selectedUID = ""
	h.tree.Refresh()
	h.table.Refresh()
	h.selectedTx = store.Transaction{}
	h.hasSelected = false
	h.reqLabel.SetText("")
	h.respLabel.SetText("")
}

func (h *historyTab) build() fyne.CanvasObject {
	filterBar := h.buildFilterBar()
	siteMapPane := h.buildSiteMap()
	tableObj := h.buildTable()
	detailSplit := h.buildDetailPane()

	topSplit := container.NewHSplit(
		siteMapPane,
		container.NewBorder(filterBar, nil, nil, nil, tableObj),
	)
	topSplit.SetOffset(0.25)

	mainSplit := container.NewVSplit(topSplit, detailSplit)
	mainSplit.SetOffset(0.55)

	go h.loadSiteMapEntries()
	go h.loadAnnotations()
	h.loadFirstPage()
	go h.watchUpdates()

	return mainSplit
}

func (h *historyTab) watchUpdates() {
	defer recoverPanic("watchUpdates")
	for tx := range h.projectStore.Updates {
		fyne.Do(func() {
			h.mu.Lock()
			for _, r := range h.displayed {
				if r.ID == tx.ID {
					h.mu.Unlock()
					return
				}
			}
			f := h.currentFilter()
			show := matchesFilter(tx, f)
			if show {
				h.displayed = append([]store.Transaction{tx}, h.displayed...)
				h.cursor = h.displayed[len(h.displayed)-1].ID
			}
			isSelected := h.hasSelected && h.selectedTx.ID == tx.ID
			if isSelected {
				h.selectedTx = tx
			}
			h.mu.Unlock()
			h.siteMap.add(tx)
			h.tree.Refresh()
			if show {
				h.table.Refresh()
			}
			if isSelected {
				h.showDetail(tx)
			}
		})
	}
}

func (h *historyTab) showDetail(tx store.Transaction) {
	truncated := len(tx.RespBody) > internalhttp.MaxDisplayBytes
	display := tx
	display.RespBody = internalhttp.TruncateBody(tx.RespBody)
	display.ReqBody = internalhttp.TruncateBody(tx.ReqBody)
	h.reqLabel.SetText(FormatStoreRequest(display))
	h.respLabel.SetText(FormatStoreResponse(display))

	h.reqCopyBtn.Enable()
	h.respCopyBtn.Enable()
	if truncated {
		h.respViewFullBtn.Show()
		h.respViewFullBtn.Enable()
	} else {
		h.respViewFullBtn.Hide()
	}
}

func showFullBodyDialog(body []byte, win fyne.Window) {
	tv := widgets.NewTextView()
	tv.SetWindow(win)
	tv.SetText(string(body))
	d := dialog.NewCustom("Full Response Body", "Close", tv.Build(), win)
	d.Resize(fyne.NewSize(800, 600))
	closeOnEscape(win, d.Dismiss)
	d.Show()
}

func (h *historyTab) annotationMenuItems(tx store.Transaction) []widgets.ContextMenuItem {
	return []widgets.ContextMenuItem{
		{
			Label: "Add Comment...",
			Action: func() { h.showCommentDialog(tx.ID) },
		},
		{
			Label: "Highlight",
			Action: func() { h.showHighlightMenu(tx.ID) },
		},
		{
			Label: "Clear Annotation",
			Action: func() {
				if err := h.projectStore.DeleteAnnotation(tx.ID); err != nil {
					logger.Error("history: clear annotation: %v", err)
					return
				}
				h.annotationsMu.Lock()
				delete(h.annotations, tx.ID)
				h.annotationsMu.Unlock()
				h.table.Refresh()
			},
		},
	}
}

func (h *historyTab) contextMenuItems(tx store.Transaction) []widgets.ContextMenuItem {
	items := []widgets.ContextMenuItem{
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
		{
			Label: "Save Request...",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("history: save request: %v", err)
						return
					}
					fyne.Do(func() {
						saveToFile([]byte(FormatStoreRequest(*fullTx)), "request.txt", h.win)
					})
				}()
			},
		},
		{
			Label: "Save Response...",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("history: save response: %v", err)
						return
					}
					fyne.Do(func() {
						saveResponseToFile(*fullTx, h.win)
					})
				}()
			},
		},
		{
			Label: "Generate CSRF PoC",
			Action: func() {
				go func() {
					fullTx, err := h.projectStore.GetTransaction(tx.ID)
					if err != nil {
						logger.Error("history: csrf poc: %v", err)
						return
					}
					fyne.Do(func() {
						showCSRFPoCDialog(*fullTx, h.win)
					})
				}()
			},
		},
	}
	return append(items, h.annotationMenuItems(tx)...)
}

func (h *historyTab) sendToRepeater(tx store.Transaction) {
	path := PathOf(tx.URL)
	if len(path) > 20 {
		path = path[:20] + "..."
	}
	h.repeater.AddTab(fmt.Sprintf("%s %s", tx.Method, path), tx.Host, tx.Port, tx.TLS, FormatStoreRequest(tx))
}

func (h *historyTab) sendToIntruder(tx store.Transaction) {
	h.intruder.reqEditor.SetText(FormatStoreRequest(tx))
}

func protoLabel(tx store.Transaction) string {
	if tx.TLS {
		if tx.Proto == "HTTP/2" {
			return "H2 TLS"
		}
		return "H1 TLS"
	}
	if tx.Proto == "HTTP/2" {
		return "H2"
	}
	return "H1.1"
}

func (h *historyTab) cellText(tx store.Transaction, col int) string {
	switch col {
	case 0:
		return tx.Method
	case 1:
		return displayHost(tx.Host, tx.Port)
	case 2:
		return PathOf(tx.URL)
	case 3:
		return fmt.Sprintf("%d", tx.StatusCode)
	case 4:
		return protoLabel(tx)
	case 5:
		return formatSize(tx.RespSize)
	case 6:
		return fmt.Sprintf("%dms", tx.DurationMs)
	case 7:
		return tx.Timestamp.Local().Format("2006-01-02 15:04:05")
	case 8:
		h.annotationsMu.RLock()
		a, ok := h.annotations[tx.ID]
		h.annotationsMu.RUnlock()
		if ok {
			return a.Comment
		}
		return ""
	}
	return ""
}

func saveResponseToFile(tx store.Transaction, win fyne.Window) {
	ext := ".txt"
	ct := tx.RespHeaders.Get("Content-Type")
	switch {
	case strings.Contains(ct, "application/json"):
		ext = ".json"
	case strings.Contains(ct, "text/html"):
		ext = ".html"
	case strings.Contains(ct, "image/png"):
		ext = ".png"
	case strings.Contains(ct, "image/jpeg"):
		ext = ".jpg"
	case strings.Contains(ct, "application/pdf"):
		ext = ".pdf"
	}
	defaultName := "response" + ext

	// Try to derive filename from URL path.
	if base := filepath.Base(strings.TrimRight(treePathOf(tx.URL), "/")); base != "" && base != "." && base != "/" {
		if filepath.Ext(base) != "" {
			defaultName = base
		}
	}

	// Write raw bytes (not formatted) so binary content is preserved.
	saveToFile(tx.RespBody, defaultName, win)
}

func showCSRFPoCDialog(tx store.Transaction, win fyne.Window) {
	poc := generateCSRFPoC(tx)

	tv := widgets.NewTextView()
	tv.SetWindow(win)
	tv.SetText(poc)

	copyBtn := widget.NewButton("Copy HTML", func() {
		fyne.CurrentApp().Clipboard().SetContent(poc)
	})
	saveBtn := widget.NewButton("Save as .html...", func() {
		saveToFile([]byte(poc), "csrf-poc.html", win)
	})

	content := container.NewBorder(
		container.NewHBox(copyBtn, saveBtn), nil, nil, nil,
		tv.Build(),
	)

	d := dialog.NewCustom("CSRF PoC", "Close", content, win)
	d.Resize(fyne.NewSize(700, 500))
	closeOnEscape(win, d.Dismiss)
	d.Show()
}

func generateCSRFPoC(tx store.Transaction) string {
	action := tx.URL
	method := strings.ToUpper(tx.Method)
	if method == "" {
		method = "POST"
	}

	var fields strings.Builder
	ct := tx.ReqHeaders.Get("Content-Type")
	if strings.Contains(ct, "application/x-www-form-urlencoded") && len(tx.ReqBody) > 0 {
		// Parse form body and emit hidden inputs.
		pairs := strings.Split(string(tx.ReqBody), "&")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			name := htmlAttrEscape(kv[0])
			val := ""
			if len(kv) == 2 {
				val = htmlAttrEscape(kv[1])
			}
			fields.WriteString(fmt.Sprintf("    <input type=\"hidden\" name=\"%s\" value=\"%s\" />\n", name, val))
		}
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<body>\n")
	b.WriteString(fmt.Sprintf("<form id=\"csrf\" action=\"%s\" method=\"%s\">\n", htmlAttrEscape(action), method))
	b.WriteString(fields.String())
	b.WriteString("  <input type=\"submit\" value=\"Submit\" />\n")
	b.WriteString("</form>\n")
	b.WriteString("<script>document.getElementById('csrf').submit();</script>\n")
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func htmlAttrEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func (h *historyTab) showCommentDialog(historyID uint64) {
	h.annotationsMu.RLock()
	existing := h.annotations[historyID].Comment
	h.annotationsMu.RUnlock()

	entry := widget.NewMultiLineEntry()
	entry.SetText(existing)
	entry.SetPlaceHolder("Enter comment...")
	entry.Resize(fyne.NewSize(400, 120))

	d := dialog.NewCustomConfirm("Add Comment", "Save", "Cancel",
		container.NewStack(entry),
		func(ok bool) {
			if !ok {
				return
			}
			h.annotationsMu.RLock()
			a := h.annotations[historyID]
			h.annotationsMu.RUnlock()
			a.HistoryID = historyID
			a.Comment = entry.Text
			if err := h.projectStore.SetAnnotation(a); err != nil {
				logger.Error("history: set annotation comment: %v", err)
				return
			}
			h.annotationsMu.Lock()
			h.annotations[historyID] = a
			h.annotationsMu.Unlock()
			h.table.Refresh()
		}, h.win)
	d.Resize(fyne.NewSize(440, 220))
	closeOnEscape(h.win, d.Dismiss)
	d.Show()
}

var highlightColors = []struct {
	name  string
	color string
}{
	{"Red", "red"},
	{"Orange", "orange"},
	{"Yellow", "yellow"},
	{"Green", "green"},
	{"Blue", "blue"},
	{"Purple", "purple"},
}

func (h *historyTab) showHighlightMenu(historyID uint64) {
	items := make([]*widget.Button, 0, len(highlightColors)+1)
	var d *dialog.CustomDialog
	for _, hc := range highlightColors {
		hc := hc
		btn := widget.NewButton(hc.name, func() {
			h.annotationsMu.RLock()
			a := h.annotations[historyID]
			h.annotationsMu.RUnlock()
			a.HistoryID = historyID
			a.Color = hc.color
			if err := h.projectStore.SetAnnotation(a); err != nil {
				logger.Error("history: set annotation color: %v", err)
				return
			}
			h.annotationsMu.Lock()
			h.annotations[historyID] = a
			h.annotationsMu.Unlock()
			h.table.Refresh()
			d.Hide()
		})
		items = append(items, btn)
	}
	var objs []fyne.CanvasObject
	for _, btn := range items {
		objs = append(objs, btn)
	}
	content := container.NewVBox(objs...)
	d = dialog.NewCustom("Highlight Color", "Cancel", content, h.win)
	closeOnEscape(h.win, d.Dismiss)
	d.Show()
}

func annotationColor(name string) color.Color {
	switch name {
	case "red":
		return color.NRGBA{R: 220, G: 80, B: 80, A: 100}
	case "orange":
		return color.NRGBA{R: 230, G: 140, B: 50, A: 100}
	case "yellow":
		return color.NRGBA{R: 220, G: 200, B: 50, A: 100}
	case "green":
		return color.NRGBA{R: 60, G: 180, B: 80, A: 100}
	case "blue":
		return color.NRGBA{R: 60, G: 130, B: 220, A: 100}
	case "purple":
		return color.NRGBA{R: 160, G: 80, B: 200, A: 100}
	}
	return nil
}

