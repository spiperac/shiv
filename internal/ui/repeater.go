package ui

import (
	"bytes"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type repeaterTab struct {
	projectStore *store.Store
	bus          *events.Bus
	loot         *lootTab
	tabs         *container.DocTabs
	win          fyne.Window
	tabIDs       map[*container.TabItem]int64
	sendFns      map[*container.TabItem]func()
	loadFns      map[*container.TabItem]func()
}

func newRepeaterTab(projectStore *store.Store, bus *events.Bus, win fyne.Window) *repeaterTab {
	return &repeaterTab{
		projectStore: projectStore,
		bus:          bus,
		win:          win,
		tabIDs:       make(map[*container.TabItem]int64),
		sendFns:      make(map[*container.TabItem]func()),
		loadFns:      make(map[*container.TabItem]func()),
	}
}

func (r *repeaterTab) closeTab(closed *container.TabItem) {
	if tabID, ok := r.tabIDs[closed]; ok {
		if err := r.projectStore.DeleteRepeaterTab(tabID); err != nil {
			logger.Error("repeater: delete tab: %v", err)
		}
		delete(r.tabIDs, closed)
	}
	delete(r.sendFns, closed)
	delete(r.loadFns, closed)
}

func (r *repeaterTab) build() fyne.CanvasObject {
	r.tabs = container.NewDocTabs()
	r.tabs.SetTabLocation(container.TabLocationTop)

	r.tabs.OnSelected = func(item *container.TabItem) {
		if fn, ok := r.loadFns[item]; ok {
			fn()
			delete(r.loadFns, item)
		}
	}

	r.tabs.OnClosed = r.closeTab

	r.tabs.CreateTab = func() *container.TabItem {
		saved := store.RepeaterTab{
			Name:    "New Tab",
			Port:    443,
			TabType: "http",
		}
		id, err := r.projectStore.SaveRepeaterTab(saved)
		if err != nil {
			logger.Error("repeater: save new tab: %v", err)
			return container.NewTabItem("New Tab", widget.NewLabel(""))
		}
		saved.ID = id
		return r.buildTabItem(saved)
	}

	saved, err := r.projectStore.AllRepeaterTabs()
	if err != nil {
		logger.Error("repeater: load tabs: %v", err)
	}
	for _, t := range saved {
		r.tabs.Append(r.buildTabItem(t))
	}
	if selected := r.tabs.Selected(); selected != nil {
		if fn, ok := r.loadFns[selected]; ok {
			fn()
			delete(r.loadFns, selected)
		}
	}

	return r.tabs
}

func (r *repeaterTab) AddTab(name, host string, port int, useTLS bool, rawRequest string) {
	saved := store.RepeaterTab{
		Name:       name,
		Host:       host,
		Port:       port,
		TLS:        useTLS,
		TabType:    "http",
		RawRequest: rawRequest,
	}
	id, err := r.projectStore.SaveRepeaterTab(saved)
	if err != nil {
		logger.Error("repeater: save tab: %v", err)
		return
	}
	saved.ID = id
	item := r.buildTabItem(saved)
	r.tabs.Append(item)
	r.tabs.Select(item)
}

// AddWSTab opens a new WebSocket repeater tab for the given connection.
func (r *repeaterTab) AddWSTab(name, wsURL string, useTLS bool) {
	saved := store.RepeaterTab{
		Name:       name,
		Host:       wsURL,
		TLS:        useTLS,
		TabType:    "websocket",
		RawRequest: wsURL,
	}
	id, err := r.projectStore.SaveRepeaterTab(saved)
	if err != nil {
		logger.Error("repeater: save ws tab: %v", err)
		return
	}
	saved.ID = id
	item := r.buildTabItem(saved)
	r.tabs.Append(item)
	r.tabs.Select(item)
}

func (r *repeaterTab) buildTabItem(tab store.RepeaterTab) *container.TabItem {
	if tab.TabType == "websocket" {
		return r.buildWSTabItem(tab)
	}
	return r.buildHTTPTabItem(tab)
}

// updateTabName updates the DocTab label after an HTTP send.
func (r *repeaterTab) updateTabName(rawReq string, tabID int64) {
	firstLine := strings.SplitN(rawReq, "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return
	}
	path := parts[1]
	if len(path) > 24 {
		path = path[:24] + "..."
	}
	name := fmt.Sprintf("%s %s", parts[0], path)

	for item, id := range r.tabIDs {
		if id == tabID {
			item.Text = name
			r.tabs.Refresh()
			break
		}
	}

	if err := r.projectStore.RenameRepeaterTab(tabID, name); err != nil {
		logger.Error("repeater: rename tab: %v", err)
	}
}

// patchRaw applies plugin modifications surgically to the normalized raw
// request string. Only fields the plugin actually changed are rewritten.
// SendRaw owns Content-Length, Connection, and Accept-Encoding — we leave
// those untouched so its internal rewriteHeaders handles them correctly.
func patchRaw(raw, headerSection string, origBody []byte, origMethod, origPath string, result events.RequestResult) string {
	lines := strings.Split(headerSection, "\r\n")
	if len(lines) == 0 {
		return raw
	}

	// Rewrite request line only if method or path actually changed.
	newMethod := result.Request.Method
	newPath := result.Request.URL.RequestURI()
	if newMethod == "" {
		newMethod = origMethod
	}
	if newPath == "" || newPath == "?" {
		newPath = origPath
	}
	if newMethod != origMethod || newPath != origPath {
		lines[0] = fmt.Sprintf("%s %s HTTP/1.1", newMethod, newPath)
	}

	// Apply header changes: update existing headers in-place (preserving
	// original casing and order), append new ones at the end.
	for pluginKey, pluginVals := range result.Request.Header {
		if len(pluginVals) == 0 {
			continue
		}
		pluginVal := strings.Join(pluginVals, ", ")
		found := false
		for i := 1; i < len(lines); i++ {
			if lines[i] == "" {
				break
			}
			parts := strings.SplitN(lines[i], ": ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], pluginKey) {
				lines[i] = parts[0] + ": " + pluginVal
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, pluginKey+": "+pluginVal)
		}
	}

	// Replace body only if the plugin actually changed it.
	_, body := internalhttp.SplitRaw(raw)
	finalBody := body
	if !bytes.Equal(result.Body, origBody) {
		finalBody = result.Body
	}

	return strings.Join(lines, "\r\n") + "\r\n\r\n" + string(finalBody)
}
