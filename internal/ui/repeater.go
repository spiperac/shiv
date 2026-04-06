package ui

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/gorilla/websocket"

	"github.com/shiv/internal/events"
	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
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

// ── HTTP tab ──────────────────────────────────────────────────────────────────

func (r *repeaterTab) buildHTTPTabItem(tab store.RepeaterTab) *container.TabItem {
	reqEditor := widgets.NewTextViewEntry()
	reqEditor.SetWindow(r.win)
	reqEditor.SetPlaceHolder("Paste or edit raw HTTP request here...")

	respLabel := widgets.NewTextView()
	respLabel.SetWindow(r.win)

	sendBtn := widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	sendBtn.Importance = widget.HighImportance

	var lastRawRequest string
	var lastRawResponse string
	var lastRespBody []byte
	cookieJar := make(map[string]string)

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		showInspectorDialog(store.Transaction{
			RespHeaders: internalhttp.ParseRawHeaders(lastRawResponse),
			RespBody:    lastRespBody,
		}, r.win)
	})
	inspectBtn.Disable()

	sendToLootBtn := widget.NewButtonWithIcon("Loot", AppIcon("loot"), func() {
		if r.loot != nil {
			r.loot.showAddDialog(nil, lastRawRequest, lastRawResponse)
		}
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
			start := time.Now()
			scheme := "http"
			if useTLS {
				scheme = "https"
			}
			addr := fmt.Sprintf("%s:%d", host, port)

			// normalizeRaw ensures CRLF in headers before any parsing.
			// SendRaw does the same internally — we must match it here so
			// that our header/body split is consistent with what it sends.
			normalized := normalizeRaw(rawReq)
			headerSection, rawBody := splitRaw(normalized)
			rawMethod := internalhttp.ExtractMethod(normalized)
			rawPath := internalhttp.ExtractURL(normalized)

			// finalRawReq is what we pass to SendRaw. It starts as the
			// normalized original and is replaced only if a plugin modifies it.
			finalRawReq := normalized

			// busOK gates EmitResponse — only emit if we successfully built
			// the bus request and the send succeeded.
			busOK := false
			var emitReq *http.Request
			var emitBody []byte

			if r.bus != nil {
				busReq, err := http.NewRequest(rawMethod, fmt.Sprintf("%s://%s%s", scheme, addr, rawPath), nil)
				if err == nil {
					busReq.Header = internalhttp.ParseRawHeaders(headerSection)

					result := r.bus.EmitRequest(events.RequestEvent{
						Request: busReq,
						Body:    rawBody,
					})

					if result.Drop {
						fyne.Do(func() {
							sendBtn.Enable()
							respLabel.SetText("HTTP/1.1 403 Forbidden\r\n\r\nrequest dropped by plugin")
						})
						return
					}

					// Apply only what the plugin actually changed back onto
					// the normalized raw string. SendRaw owns Content-Length,
					// Connection, and Accept-Encoding — we never touch those.
					finalRawReq = patchRaw(normalized, headerSection, rawBody, rawMethod, rawPath, result)
					emitReq = result.Request
					emitBody = result.Body
					busOK = true
				}
			}

			sendResult, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
				Host:      host,
				Port:      port,
				TLS:       useTLS,
				RawReq:    finalRawReq,
				CookieJar: cookieJar,
			})

			elapsed := time.Since(start).Milliseconds()

			if err == nil && busOK {
				r.bus.EmitResponse(events.ResponseEvent{
					Timestamp:   start,
					Host:        addr,
					Proto:       "HTTP/1.1",
					Method:      emitReq.Method,
					URL:         emitReq.URL.String(),
					ReqHeaders:  emitReq.Header,
					ReqBody:     emitBody,
					StatusCode:  sendResult.StatusCode,
					RespHeaders: internalhttp.ParseRawHeaders(sendResult.Raw),
					RespBody:    sendResult.Body,
					DurationMs:  elapsed,
					TLS:         useTLS,
				})
			}

			fyne.Do(func() {
				sendBtn.Enable()
				if err != nil {
					respLabel.SetText("Error: " + err.Error())
					logger.Error("repeater: send: %v", err)
					return
				}
				respLabel.SetText(sendResult.Raw)
				for _, c := range sendResult.Cookies {
					cookieJar[c.Name] = c.Value
				}
				lastRawRequest = rawReq
				lastRawResponse = sendResult.Raw
				lastRespBody = sendResult.Body
				r.updateTabName(rawReq, tabID)
				inspectBtn.Enable()
				sendToLootBtn.Enable()
				if saveErr := r.projectStore.UpdateRepeaterTab(tabID, rawReq, sendResult.Raw); saveErr != nil {
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

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil, reqEditor.Build())
	respPane := container.NewBorder(newBoldLabel("Response"), nil, nil, nil, respLabel.Build())

	split := container.NewHSplit(reqPane, respPane)
	split.SetOffset(0.5)

	content := container.NewBorder(container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), toolbar), nil, nil, nil, split)
	tabItem := container.NewTabItem(tab.Name, content)

	r.sendFns[tabItem] = doSend
	r.tabIDs[tabItem] = tabID
	reqEditor.SetText(tab.RawRequest)
	r.loadFns[tabItem] = func() {}

	return tabItem
}

// ── Raw request helpers ───────────────────────────────────────────────────────

// normalizeRaw ensures CRLF line endings in the header section only.
// Mirrors SendRaw's internal normalizeLineEndings so our parsing is consistent
// with what actually goes on the wire.
func normalizeRaw(raw string) string {
	headerSection, body := splitRaw(raw)
	headerSection = strings.ReplaceAll(headerSection, "\r\n", "\n")
	headerSection = strings.ReplaceAll(headerSection, "\n", "\r\n")
	return headerSection + "\r\n\r\n" + string(body)
}

// splitRaw splits a raw HTTP request into its header section and body.
// Handles both CRLF and LF delimiters. The returned header section does
// not include the blank line separator.
func splitRaw(raw string) (headerSection string, body []byte) {
	if idx := strings.Index(raw, "\r\n\r\n"); idx >= 0 {
		return raw[:idx], []byte(raw[idx+4:])
	}
	if idx := strings.Index(raw, "\n\n"); idx >= 0 {
		return raw[:idx], []byte(raw[idx+2:])
	}
	return raw, nil
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
	_, body := splitRaw(raw)
	finalBody := body
	if !bytes.Equal(result.Body, origBody) {
		finalBody = result.Body
	}

	return strings.Join(lines, "\r\n") + "\r\n\r\n" + string(finalBody)
}

// ── WebSocket tab ─────────────────────────────────────────────────────────────

type wsConnState int

const (
	wsDisconnected wsConnState = iota
	wsConnecting
	wsConnected
)

func (r *repeaterTab) buildWSTabItem(tab store.RepeaterTab) *container.TabItem {
	tabID := tab.ID

	var wsConn *websocket.Conn
	var connState wsConnState
	var connID uint64

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("wss://host:port/path")
	if tab.RawRequest != "" {
		urlEntry.SetText(tab.RawRequest)
	}

	type wsFrame struct {
		direction string
		payload   string
		ts        time.Time
	}
	var frames []wsFrame

	frameColumns := []widgets.DataTableColumn{
		{Header: "Dir", Width: 80},
		{Header: "Time", Width: 120},
		{Header: "Payload", Width: 500},
	}

	frameTable := widgets.NewDataTable()
	frameTable.SetWindow(r.win)
	frameTable.Columns = frameColumns
	frameTable.RowCount = func() int { return len(frames) }
	frameTable.RowID = func(row int) int64 { return int64(row) }
	frameTable.CellValue = func(row, col int) string {
		if row >= len(frames) {
			return ""
		}
		f := frames[row]
		switch col {
		case 0:
			return f.direction
		case 1:
			return f.ts.Format("15:04:05.000")
		case 2:
			return f.payload
		}
		return ""
	}
	frameTable.CellStyle = func(row, col int) widget.Importance {
		if col != 0 || row >= len(frames) {
			return widget.MediumImportance
		}
		if frames[row].direction == "→ Sent" {
			return widget.HighImportance
		}
		return widget.SuccessImportance
	}

	payloadEntry := widget.NewMultiLineEntry()
	payloadEntry.SetPlaceHolder("Message to send...")
	payloadEntry.SetMinRowsVisible(3)

	connectBtn := widget.NewButtonWithIcon("Connect", AppIcon("web"), nil)
	connectBtn.Importance = widget.HighImportance

	sendFrameBtn := widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	sendFrameBtn.Disable()

	clearBtn := widget.NewButtonWithIcon("Clear", AppIcon("delete"), func() {
		frames = nil
		frameTable.Refresh()
	})

	updateConnectBtn := func() {
		switch connState {
		case wsDisconnected:
			connectBtn.SetText("Connect")
			connectBtn.Importance = widget.HighImportance
			connectBtn.Enable()
			sendFrameBtn.Disable()
		case wsConnecting:
			connectBtn.SetText("Connecting...")
			connectBtn.Importance = widget.MediumImportance
			connectBtn.Disable()
			sendFrameBtn.Disable()
		case wsConnected:
			connectBtn.SetText("Disconnect")
			connectBtn.Importance = widget.DangerImportance
			connectBtn.Enable()
			sendFrameBtn.Enable()
		}
		connectBtn.Refresh()
	}

	disconnect := func() {
		if wsConn != nil {
			wsConn.Close()
			wsConn = nil
		}
		connState = wsDisconnected
		updateConnectBtn()
	}

	startReadLoop := func(conn *websocket.Conn) {
		go func() {
			for {
				msgType, payload, err := conn.ReadMessage()
				if err != nil {
					fyne.Do(func() {
						if wsConn == conn {
							wsConn = nil
							connState = wsDisconnected
							updateConnectBtn()
							frames = append(frames, wsFrame{
								direction: "⚠ Closed",
								payload:   err.Error(),
								ts:        time.Now(),
							})
							frameTable.Refresh()
						}
					})
					return
				}
				finalPayload := payload
				if r.bus != nil {
					result := r.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
						ConnectionID: connID,
						Timestamp:    time.Now(),
						Direction:    events.WebSocketServer,
						Opcode:       events.WebSocketOpcode(msgType),
						Payload:      payload,
					})
					finalPayload = result.Payload
				}
				f := wsFrame{direction: "← Recv", payload: string(finalPayload), ts: time.Now()}
				fyne.Do(func() {
					frames = append(frames, f)
					frameTable.Refresh()
					frameTable.ScrollToRow(len(frames) - 1)
				})
			}
		}()
	}

	connectBtn.OnTapped = func() {
		if connState == wsConnected {
			disconnect()
			return
		}
		rawURL := strings.TrimSpace(urlEntry.Text)
		if rawURL == "" {
			return
		}
		connState = wsConnecting
		updateConnectBtn()
		_ = r.projectStore.UpdateRepeaterTab(tabID, rawURL, "")

		go func() {
			dialer := websocket.Dialer{
				TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				HandshakeTimeout: 10 * time.Second,
				NetDial: func(network, addr string) (net.Conn, error) {
					return net.DialTimeout(network, addr, 10*time.Second)
				},
			}
			conn, _, err := dialer.Dial(rawURL, nil)
			fyne.Do(func() {
				if err != nil {
					connState = wsDisconnected
					updateConnectBtn()
					frames = append(frames, wsFrame{direction: "⚠ Error", payload: err.Error(), ts: time.Now()})
					frameTable.Refresh()
					return
				}
				wsConn = conn
				connState = wsConnected
				updateConnectBtn()
				if r.bus != nil {
					connID = r.bus.EmitWebSocketConnection(events.WebSocketConnectionEvent{
						Host:      rawURL,
						URL:       rawURL,
						TLS:       strings.HasPrefix(rawURL, "wss://"),
						Timestamp: time.Now(),
					})
				}
				frames = append(frames, wsFrame{direction: "✓ Connected", payload: rawURL, ts: time.Now()})
				frameTable.Refresh()
				startReadLoop(conn)
			})
		}()
	}

	sendFrameBtn.OnTapped = func() {
		if wsConn == nil || connState != wsConnected {
			return
		}
		payload := payloadEntry.Text
		if payload == "" {
			return
		}
		conn := wsConn
		go func() {
			finalPayload := []byte(payload)
			if r.bus != nil {
				result := r.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
					ConnectionID: connID,
					Timestamp:    time.Now(),
					Direction:    events.WebSocketClient,
					Opcode:       events.WebSocketText,
					Payload:      finalPayload,
				})
				finalPayload = result.Payload
			}
			err := conn.WriteMessage(websocket.TextMessage, finalPayload)
			fyne.Do(func() {
				if err != nil {
					frames = append(frames, wsFrame{direction: "⚠ Error", payload: "send failed: " + err.Error(), ts: time.Now()})
					frameTable.Refresh()
					return
				}
				frames = append(frames, wsFrame{direction: "→ Sent", payload: string(finalPayload), ts: time.Now()})
				frameTable.Refresh()
				frameTable.ScrollToRow(len(frames) - 1)
			})
		}()
	}

	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(connectBtn, clearBtn),
		nil,
		urlEntry,
	)

	sendBar := container.NewBorder(nil, nil, nil, sendFrameBtn, payloadEntry)
	framePane := container.NewBorder(newBoldLabel("Frames"), nil, nil, nil, frameTable.Build())

	content := container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), toolbar),
		container.NewBorder(nil, nil, newBoldLabel("Message"), nil, sendBar),
		nil, nil,
		framePane,
	)

	tabItem := container.NewTabItem(tab.Name, content)
	r.tabIDs[tabItem] = tabID
	r.loadFns[tabItem] = func() {}

	return tabItem
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
