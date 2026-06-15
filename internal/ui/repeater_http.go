package ui

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/events"
	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

type httpHistEntry struct{ req, resp string }

type httpTabState struct {
	r               *repeaterTab
	tab             store.RepeaterTab
	tabID           int64
	reqEditor       *widgets.TextViewEntry
	respLabel       *widgets.TextView
	sendBtn         *widget.Button
	inspectBtn      *widget.Button
	sendToLootBtn   *widget.Button
	prevBtn         *widget.Button
	nextBtn         *widget.Button
	cookieJar       map[string]string
	lastRawRequest  string
	lastRawResponse string
	lastRespBody    []byte
	sendHistory     []httpHistEntry
	histIdx         int
}

func (s *httpTabState) loadHistEntry(idx int) {
	if idx < 0 || idx >= len(s.sendHistory) {
		return
	}
	s.histIdx = idx
	s.reqEditor.SetText(s.sendHistory[idx].req)
	s.respLabel.SetText(s.sendHistory[idx].resp)
	if s.histIdx > 0 {
		s.prevBtn.Enable()
	} else {
		s.prevBtn.Disable()
	}
	if s.histIdx < len(s.sendHistory)-1 {
		s.nextBtn.Enable()
	} else {
		s.nextBtn.Disable()
	}
}

// applyBusRequest runs the bus plugin pipeline on rawReq. Returns dropped=true
// when a plugin rejects the request (UI already updated); caller must return.
// busOK=false (with dropped=false) means no bus or NewRequest failed — caller
// should still send, just skip EmitResponse.
func (s *httpTabState) applyBusRequest(normalized, headerSection string, rawBody []byte, rawMethod, rawPath, scheme, addr string) (finalRaw string, emitReq *http.Request, emitBody []byte, busOK, dropped bool) {
	finalRaw = normalized
	if s.r.bus == nil {
		return
	}
	busReq, err := http.NewRequest(rawMethod, fmt.Sprintf("%s://%s%s", scheme, addr, rawPath), nil)
	if err != nil {
		return
	}
	busReq.Header = internalhttp.ParseRawHeaders(headerSection)
	result := s.r.bus.EmitRequest(events.RequestEvent{Request: busReq, Body: rawBody})
	if result.Drop {
		fyne.Do(func() {
			s.sendBtn.Enable()
			s.respLabel.SetText("HTTP/1.1 403 Forbidden\r\n\r\nrequest dropped by plugin")
		})
		dropped = true
		return
	}
	// Apply only what the plugin actually changed back onto
	// the normalized raw string. SendRaw owns Content-Length,
	// Connection, and Accept-Encoding — we never touch those.
	finalRaw = patchRaw(normalized, headerSection, rawBody, rawMethod, rawPath, result)
	return finalRaw, result.Request, result.Body, true, false
}

// applySendResult runs inside fyne.Do after a successful or failed send.
func (s *httpTabState) applySendResult(rawReq string, sendResult *internalhttp.RawResponse, err error) {
	s.sendBtn.Enable()
	if err != nil {
		s.respLabel.SetText("Error: " + err.Error())
		logger.Error("repeater: send: %v", err)
		return
	}
	s.respLabel.SetText(sendResult.Raw)
	for _, c := range sendResult.Cookies {
		s.cookieJar[c.Name] = c.Value
	}
	s.lastRawRequest = rawReq
	s.lastRawResponse = sendResult.Raw
	s.lastRespBody = sendResult.Body
	s.r.updateTabName(rawReq, s.tabID)
	s.inspectBtn.Enable()
	s.sendToLootBtn.Enable()
	if saveErr := s.r.projectStore.UpdateRepeaterTab(s.tabID, rawReq, sendResult.Raw); saveErr != nil {
		logger.Error("repeater: update tab: %v", saveErr)
	}

	// Record in per-tab send history.
	const maxHistory = 50
	s.sendHistory = append(s.sendHistory, httpHistEntry{req: rawReq, resp: sendResult.Raw})
	if len(s.sendHistory) > maxHistory {
		s.sendHistory = s.sendHistory[len(s.sendHistory)-maxHistory:]
	}
	s.histIdx = len(s.sendHistory) - 1
	if s.prevBtn != nil {
		if s.histIdx > 0 {
			s.prevBtn.Enable()
		}
		s.nextBtn.Disable()
	}
}

func (s *httpTabState) doSend() {
	if s.sendBtn.Disabled() {
		return
	}
	rawReq := s.reqEditor.GetText()
	host, port, useTLS := internalhttp.ParseHostFromRaw(rawReq)
	if host == "" {
		s.respLabel.SetText("Error: no Host header found in request")
		return
	}
	// ParseHostFromRaw defaults to port 80 / plain HTTP when the Host header
	// has no explicit port and the request line has no scheme prefix.
	// Use the tab's saved port/TLS as the authoritative values in that case.
	if port == 80 && !useTLS && s.tab.Port != 0 {
		port = s.tab.Port
		useTLS = s.tab.TLS
	}
	s.sendBtn.Disable()
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
		normalized := internalhttp.NormalizeLineEndings(rawReq)
		headerSection, rawBody := internalhttp.SplitRaw(normalized)
		rawMethod := internalhttp.ExtractMethod(normalized)
		rawPath := internalhttp.ExtractURL(normalized)

		finalRawReq, emitReq, emitBody, busOK, dropped := s.applyBusRequest(normalized, headerSection, rawBody, rawMethod, rawPath, scheme, addr)
		if dropped {
			return
		}

		sendResult, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
			Host:      host,
			Port:      port,
			TLS:       useTLS,
			RawReq:    finalRawReq,
			CookieJar: s.cookieJar,
		})

		elapsed := time.Since(start).Milliseconds()

		if err == nil && busOK {
			s.r.bus.EmitResponse(events.ResponseEvent{
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

		fyne.Do(func() { s.applySendResult(rawReq, sendResult, err) })
	}()
}

func (s *httpTabState) buildEditors() {
	s.reqEditor = widgets.NewTextViewEntry()
	s.reqEditor.SetWindow(s.r.win)
	s.reqEditor.SetPlaceHolder("Paste or edit raw HTTP request here...")

	s.respLabel = widgets.NewTextView()
	s.respLabel.SetWindow(s.r.win)
}

func (s *httpTabState) buildActionButtons() {
	s.inspectBtn = widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		_, reqBody := internalhttp.SplitRaw(s.lastRawRequest)
		showInspectorDialog(store.Transaction{
			ReqHeaders:  internalhttp.ParseRawHeaders(s.lastRawRequest),
			ReqBody:     reqBody,
			RespHeaders: internalhttp.ParseRawHeaders(s.lastRawResponse),
			RespBody:    s.lastRespBody,
		}, s.r.win)
	})
	s.inspectBtn.Disable()

	s.sendToLootBtn = widget.NewButtonWithIcon("Loot", AppIcon("loot"), func() {
		if s.r.loot != nil {
			s.r.loot.showAddDialog(nil, s.lastRawRequest, s.lastRawResponse)
		}
	})
	s.sendToLootBtn.Disable()
}

func (s *httpTabState) buildSendHistory() {
	s.prevBtn = widget.NewButton("◀", func() { s.loadHistEntry(s.histIdx - 1) })
	s.nextBtn = widget.NewButton("▶", func() { s.loadHistEntry(s.histIdx + 1) })
	s.prevBtn.Disable()
	s.nextBtn.Disable()
}

func (s *httpTabState) buildToolbar() fyne.CanvasObject {
	cloneBtn := widget.NewButtonWithIcon("Clone", theme.ContentCopyIcon(), func() {
		raw := s.reqEditor.GetText()
		firstLine := strings.SplitN(raw, "\n", 2)[0]
		parts := strings.Fields(firstLine)
		name := s.tab.Name
		if len(parts) >= 2 {
			path := parts[1]
			if len(path) > 20 {
				path = path[:20] + "..."
			}
			name = fmt.Sprintf("%s %s", parts[0], path)
		}
		host, port, useTLS := internalhttp.ParseHostFromRaw(raw)
		s.r.AddTab(name, host, port, useTLS, raw)
	})

	return container.NewBorder(nil, nil,
		container.NewHBox(s.sendBtn, cloneBtn, s.prevBtn, s.nextBtn),
		s.sendToLootBtn,
		widget.NewLabel(""),
	)
}

func (s *httpTabState) buildLayout() fyne.CanvasObject {
	toolbar := s.buildToolbar()

	copyReqBtn := widget.NewButton("Copy", func() {
		fyne.CurrentApp().Clipboard().SetContent(s.reqEditor.GetText())
	})
	copyRespBtn := widget.NewButton("Copy", func() {
		fyne.CurrentApp().Clipboard().SetContent(s.lastRawResponse)
	})

	reqPane := container.NewBorder(
		paneHeader("Request", copyReqBtn),
		nil, nil, nil, s.reqEditor.Build(),
	)
	respPane := container.NewBorder(
		paneHeader("Response", copyRespBtn, s.inspectBtn),
		nil, nil, nil, s.respLabel.Build(),
	)

	split := container.NewHSplit(reqPane, respPane)
	split.SetOffset(0.5)

	return container.NewBorder(container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), toolbar), nil, nil, nil, split)
}

func (r *repeaterTab) buildHTTPTabItem(tab store.RepeaterTab) *container.TabItem {
	s := &httpTabState{
		r:         r,
		tab:       tab,
		tabID:     tab.ID,
		cookieJar: make(map[string]string),
		histIdx:   -1,
	}

	s.buildEditors()
	s.buildActionButtons()
	s.buildSendHistory()

	s.sendBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	s.sendBtn.Importance = widget.HighImportance
	s.sendBtn.OnTapped = s.doSend

	content := s.buildLayout()
	tabItem := container.NewTabItem(tab.Name, content)

	r.sendFns[tabItem] = s.doSend
	r.tabIDs[tabItem] = s.tabID
	s.reqEditor.SetText(tab.RawRequest)
	r.loadFns[tabItem] = func() {}

	return tabItem
}
