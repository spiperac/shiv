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
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/andybalholm/brotli"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type repeaterTab struct {
	st   *store.Store
	tabs *container.DocTabs
	win  fyne.Window
}

func newRepeaterTab(st *store.Store, win fyne.Window) *repeaterTab {
	return &repeaterTab{st: st, win: win}
}

func (r *repeaterTab) build() fyne.CanvasObject {
	r.tabs = container.NewDocTabs()
	r.tabs.SetTabLocation(container.TabLocationTop)
	r.tabs.CreateTab = func() *container.TabItem {
		saved := store.RepeaterTab{
			Name:       "New Tab",
			Host:       "",
			Port:       443,
			TLS:        false,
			RawRequest: "",
		}
		id, err := r.st.SaveRepeaterTab(saved)
		if err != nil {
			logger.Error("repeater: save new tab: %v", err)
			return container.NewTabItem("New Tab", widget.NewLabel(""))
		}
		saved.ID = id
		return r.buildTabItem(saved)
	}

	saved, err := r.st.AllRepeaterTabs()
	if err != nil {
		logger.Error("repeater: load tabs: %v", err)
	}
	for _, t := range saved {
		r.tabs.Append(r.buildTabItem(t))
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
	id, err := r.st.SaveRepeaterTab(saved)
	if err != nil {
		logger.Error("repeater: save tab: %v", err)
		return
	}
	saved.ID = id
	item := r.buildTabItem(saved)
	r.tabs.Append(item)
	r.tabs.Select(item)
}

func (r *repeaterTab) buildTabItem(t store.RepeaterTab) *container.TabItem {
	reqEditor := widget.NewMultiLineEntry()
	reqEditor.SetPlaceHolder("Paste or edit raw HTTP request here...")
	reqEditor.TextStyle = fyne.TextStyle{Monospace: true}
	reqEditor.Wrapping = fyne.TextWrapOff
	reqEditor.SetText(t.RawRequest)

	respLabel := widget.NewMultiLineEntry() //widget.NewLabel("")
	respLabel.TextStyle = fyne.TextStyle{Monospace: true}
	respLabel.Wrapping = fyne.TextWrapBreak

	sendBtn := widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	sendBtn.Importance = widget.HighImportance

	tabID := t.ID
	tabHost := t.Host
	tabPort := t.Port
	tabTLS := t.TLS

	var tabItem *container.TabItem

	sendBtn.OnTapped = func() {
		rawReq := reqEditor.Text
		sendBtn.Disable()
		go func() {
			resp, err := sendRawRequest(tabHost, tabPort, tabTLS, rawReq)
			fyne.Do(func() {
				sendBtn.Enable()
				if err != nil {
					respLabel.SetText("Error: " + err.Error())
					logger.Error("repeater: send: %v", err)
				} else {
					respLabel.SetText(resp)
				}
				if saveErr := r.st.UpdateRepeaterTab(tabID, rawReq, respLabel.Text); saveErr != nil {
					logger.Error("repeater: update tab: %v", saveErr)
				}
			})
		}()
	}

	toolbar := container.NewVBox(
		widget.NewLabel(""), // adds vertical space
		container.NewHBox(sendBtn),
	)

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil,
		container.NewScroll(reqEditor))
	respPane := container.NewBorder(newBoldLabel("Response"), nil, nil, nil,
		container.NewScroll(respLabel))

	split := container.NewHSplit(reqPane, respPane)
	split.SetOffset(0.5)

	content := container.NewBorder(toolbar, nil, nil, nil, split)

	tabItem = container.NewTabItem(t.Name, content)

	r.tabs.OnClosed = func(closed *container.TabItem) {
		if closed == tabItem {
			if err := r.st.DeleteRepeaterTab(tabID); err != nil {
				logger.Error("repeater: delete tab: %v", err)
			}
		}
	}

	return tabItem
}

func scheme(useTLS bool) string {
	if useTLS {
		return "https"
	}
	return "http"
}

func sendRawRequest(host string, port int, useTLS bool, rawReq string) (string, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

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

	// Normalize line endings.
	rawReq = strings.ReplaceAll(rawReq, "\r\n", "\n")
	rawReq = strings.ReplaceAll(rawReq, "\n", "\r\n")

	// Strip port from Host header and remove Accept-Encoding so server returns plain text.
	var lines []string
	for _, line := range strings.Split(rawReq, "\r\n") {
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
		lines = append(lines, line)
	}
	rawReq = strings.Join(lines, "\r\n")
	if !strings.HasSuffix(rawReq, "\r\n\r\n") {
		rawReq += "\r\n"
	}

	if _, err := fmt.Fprint(conn, rawReq); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	body = decompressRepeaterBody(resp.Header, body)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)))
	for k, vv := range resp.Header {
		for _, v := range vv {
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
		}
	}
	sb.WriteString("\r\n")
	sb.Write(body)
	return sb.String(), nil
}
func decompressRepeaterBody(header http.Header, body []byte) []byte {
	ce := strings.ToLower(header.Get("Content-Encoding"))
	r := bytes.NewReader(body)
	switch ce {
	case "gzip":
		gr, err := gzip.NewReader(r)
		if err != nil {
			return body
		}
		defer gr.Close()
		out, err := io.ReadAll(gr)
		if err != nil {
			return body
		}
		return out
	case "deflate":
		out, err := io.ReadAll(flate.NewReader(r))
		if err != nil {
			return body
		}
		return out
	case "br":
		out, err := io.ReadAll(brotli.NewReader(r))
		if err != nil {
			return body
		}
		return out
	}
	return body
}
