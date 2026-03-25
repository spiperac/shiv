package ui

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/andybalholm/brotli"
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
	if tabId, ok := r.tabIDs[closed]; ok {
		logger.Info("repeater: OnClosed called, found=%v id=%d", ok, tabId)
		if err := r.projectStore.DeleteRepeaterTab(tabId); err != nil {
			logger.Error("repeater: delete tab: %v", err)
		}
		delete(r.tabIDs, closed)
	}
	delete(r.sendFns, closed)
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

	var lastRawRequest string
	var lastRawResponse string

	inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
		lastTx := store.Transaction{
			RespHeaders: parseRawHeaders(lastRawResponse),
			RespBody:    []byte(lastRawResponse),
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
		host, port, useTLS := parseHostFromRaw(rawReq)
		if host == "" {
			respLabel.SetText("Error: no Host header found in request")
			return
		}
		sendBtn.Disable()
		go func() {
			resp, err := sendRawRequest(host, port, useTLS, rawReq)
			fyne.Do(func() {
				sendBtn.Enable()
				if err != nil {
					respLabel.SetText("Error: " + err.Error())
					logger.Error("repeater: send: %v", err)
				} else {
					respLabel.SetText(resp)
					lastRawRequest = rawReq
					lastRawResponse = resp
					inspectBtn.Enable()
					sendToLootBtn.Enable()
				}
				if saveErr := r.projectStore.UpdateRepeaterTab(tabID, rawReq, respLabel.GetText()); saveErr != nil {
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
		host, port, useTLS := parseHostFromRaw(raw)
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
	r.loadFns[tabItem] = func() { reqEditor.SetText(tab.RawRequest) }

	return tabItem
}

func sendRawRequest(host string, port int, useTLS bool, rawReq string) (string, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	var conn net.Conn
	var err error

	if useTLS {
		conn, err = tls.Dial("tcp", addr, &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: true, //nolint:gosec
		})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	rawReq = strings.ReplaceAll(rawReq, "\r\n", "\n")
	rawReq = strings.ReplaceAll(rawReq, "\n", "\r\n")

	parts := strings.SplitN(rawReq, "\r\n\r\n", 2)
	headerSection := parts[0]
	body := ""
	if len(parts) == 2 {
		body = parts[1]
		body = strings.TrimRight(body, "\r\n")
	}

	headerLines := strings.Split(headerSection, "\r\n")
	var newLines []string
	for _, line := range headerLines {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "host:") {
			hostVal := strings.TrimSpace(line[5:])
			if h, _, err := net.SplitHostPort(hostVal); err == nil {
				line = "Host: " + h
			}
		}
		if strings.HasPrefix(lower, "accept-encoding:") {
			continue
		}
		if strings.HasPrefix(lower, "content-length:") {
			continue
		}
		newLines = append(newLines, line)
	}

	if len(body) > 0 {
		newLines = append(newLines, fmt.Sprintf("Content-Length: %d", len(body)))
	}

	finalReq := strings.Join(newLines, "\r\n") + "\r\n\r\n" + body

	logger.Debug("repeater: sending request:\n%s", finalReq)
	if _, err := fmt.Fprint(conn, finalReq); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	httpResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	respBody = decompressRepeaterBody(httpResp.Header, respBody)
	if len(respBody) > 64*1024 {
		respBody = append(respBody[:64*1024], []byte("\n... truncated")...)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "HTTP/1.1 %d %s\r\n", httpResp.StatusCode, http.StatusText(httpResp.StatusCode))
	for headerKey, headerValues := range httpResp.Header {
		for _, headerValue := range headerValues {
			fmt.Fprintf(&builder, "%s: %s\r\n", headerKey, headerValue)
		}
	}
	builder.WriteString("\r\n")
	builder.Write(respBody)
	return builder.String(), nil
}

func decompressRepeaterBody(header http.Header, body []byte) []byte {
	contentEncoding := strings.ToLower(header.Get("Content-Encoding"))
	bodyReader := bytes.NewReader(body)
	switch contentEncoding {
	case "gzip":
		gzipReader, err := gzip.NewReader(bodyReader)
		if err != nil {
			return body
		}
		defer gzipReader.Close()
		out, err := io.ReadAll(gzipReader)
		if err != nil {
			return body
		}
		return out
	case "deflate":
		out, err := io.ReadAll(flate.NewReader(bodyReader))
		if err != nil {
			return body
		}
		return out
	case "br":
		out, err := io.ReadAll(brotli.NewReader(bodyReader))
		if err != nil {
			return body
		}
		return out
	}
	return body
}

func parseRawHeaders(raw string) http.Header {
	headers := http.Header{}
	lines := strings.Split(raw, "\r\n")
	for _, line := range lines[1:] {
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) == 2 {
			headers.Add(parts[0], parts[1])
		}
	}
	return headers
}

func parseHostFromRaw(raw string) (host string, port int, useTLS bool) {
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			hostVal := strings.TrimSpace(line[5:])
			if hostname, portStr, err := net.SplitHostPort(hostVal); err == nil {
				host = hostname
				port, _ = strconv.Atoi(portStr)
				useTLS = port == 443
			} else {
				host = hostVal
				port = 443
				useTLS = true
			}
			return
		}
	}
	return "", 0, false
}
