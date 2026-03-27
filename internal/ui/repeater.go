package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

type repeaterTab struct {
	projectStore *store.Store
	loot         *lootTab
	tabs         *container.DocTabs
	win          fyne.Window
	tabIDs       map[*container.TabItem]int64
	sendFns      map[*container.TabItem]func()
	loadFns      map[*container.TabItem]func()
}

func newRepeaterTab(projectStore *store.Store, win fyne.Window) *repeaterTab {
	return &repeaterTab{
		projectStore: projectStore,
		win:          win,
		tabIDs:       make(map[*container.TabItem]int64),
		sendFns:      make(map[*container.TabItem]func()),
		loadFns:      make(map[*container.TabItem]func()),
	}
}

func (r *repeaterTab) closeTab(closed *container.TabItem) {
	if tabID, ok := r.tabIDs[closed]; ok {
		logger.Info("repeater: OnClosed called, found=%v id=%d", ok, tabID)
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
			Name:       "New Tab",
			Host:       "",
			Port:       443,
			TLS:        false,
			RawRequest: "",
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

func (r *repeaterTab) buildTabItem(tab store.RepeaterTab) *container.TabItem {
	reqEditor := widgets.NewTextViewEntry()
	reqEditor.SetWindow(r.win)
	reqEditor.SetPlaceHolder("Paste or edit raw HTTP request here...")

	respLabel := widgets.NewTextView()
	respLabel.SetWindow(r.win)

	sendBtn := widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	sendBtn.Importance = widget.HighImportance

	// lastRawRequest and lastRawResponse are only accessed on the Fyne main
	// goroutine (inside fyne.Do callbacks and UI event handlers), so no mutex
	// is needed. cookieJar is read by the send goroutine before it starts and
	// written back inside fyne.Do before sendBtn.Enable() — the Enable() acts
	// as a happens-before barrier, so access is safe.
	var lastRawRequest string
	var lastRawResponse string
	var lastRespBody []byte
	cookieJar := make(map[string]string)

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		// Use the already-split headers and body — do NOT pass the full raw
		// response string as the body.
		respHeaders := internalhttp.ParseRawHeaders(lastRawResponse)
		lastTx := store.Transaction{
			RespHeaders: respHeaders,
			RespBody:    lastRespBody,
		}
		showInspectorDialog(lastTx, r.win)
	})
	inspectBtn.Disable()

	sendToLootBtn := widget.NewButtonWithIcon("Loot", AppIcon("loot"), func() {
		if r.loot == nil {
			return
		}
		r.loot.showAddDialog(nil, lastRawRequest, lastRawResponse)
	})
	sendToLootBtn.Disable()

	tabID := tab.ID

	doSend := func() {
		if sendBtn.Disabled() {
			return
		}
		rawReq := reqEditor.GetText()
		host, port, useTLS := internalhttp.ParseHostFromRaw(rawReq)
		if host == "" {
			respLabel.SetText("Error: no Host header found in request")
			return
		}

		sendBtn.Disable()
		go func() {
			result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
				Host:      host,
				Port:      port,
				TLS:       useTLS,
				RawReq:    rawReq,
				CookieJar: cookieJar,
			})
			fyne.Do(func() {
				sendBtn.Enable()
				if err != nil {
					respLabel.SetText("Error: " + err.Error())
					logger.Error("repeater: send: %v", err)
					// Don't update last request/response on error.
					return
				}

				respLabel.SetText(result.Raw)

				// Update cookie jar with any new cookies from the response.
				for _, c := range result.Cookies {
					cookieJar[c.Name] = c.Value
				}

				// Store request and split response for inspector/loot use.
				lastRawRequest = rawReq
				lastRawResponse = result.Raw
				lastRespBody = result.Body // already decompressed by SendRaw

				// Update tab name to reflect the current request line.
				r.updateTabName(rawReq, tabID)

				inspectBtn.Enable()
				sendToLootBtn.Enable()

				if saveErr := r.projectStore.UpdateRepeaterTab(tabID, rawReq, result.Raw); saveErr != nil {
					logger.Error("repeater: update tab: %v", saveErr)
				}
			})
		}()
	}

	sendBtn.OnTapped = doSend

	cloneBtn := widget.NewButtonWithIcon("Clone", theme.ContentCopyIcon(), func() {
		raw := reqEditor.GetText()
		firstLine := strings.SplitN(raw, "\n", 2)[0]
		parts := strings.Fields(firstLine)
		name := tab.Name
		if len(parts) >= 2 {
			path := parts[1]
			if len(path) > 20 {
				path = path[:20] + "..."
			}
			name = fmt.Sprintf("%s %s", parts[0], path)
		}
		host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
		r.AddTab(name, host, port, useTLS, raw)
	})

	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(sendBtn, cloneBtn),
		container.NewHBox(sendToLootBtn, inspectBtn),
		widget.NewLabel(""),
	)

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil,
		reqEditor.Build())

	respPane := container.NewBorder(newBoldLabel("Response"), nil, nil, nil,
		respLabel.Build())

	split := container.NewHSplit(reqPane, respPane)
	split.SetOffset(0.5)

	content := container.NewBorder(container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), toolbar), nil, nil, nil, split)
	tabItem := container.NewTabItem(tab.Name, content)

	r.sendFns[tabItem] = doSend
	r.tabIDs[tabItem] = tabID
	// Lazy-load: populate the editor only when the tab is first selected,
	// so opening a project with many tabs doesn't block startup.
	r.loadFns[tabItem] = func() { reqEditor.SetText(tab.RawRequest) }

	return tabItem
}

// updateTabName updates the DocTab label to reflect the current request's
// method and path, matching Burp's auto-rename behaviour on send.
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

	// Find the tab item for this tabID and update its text.
	for item, id := range r.tabIDs {
		if id == tabID {
			item.Text = name
			r.tabs.Refresh()
			break
		}
	}

	// Persist the new name.
	if err := r.projectStore.RenameRepeaterTab(tabID, name); err != nil {
		logger.Error("repeater: rename tab: %v", err)
	}
}
