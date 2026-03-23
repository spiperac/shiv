package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type interceptTab struct {
	projectStore *store.Store
	pending      *store.PendingRequest

	toggle  *widget.Check
	editor  *widget.Entry
	forward *widget.Button
	drop    *widget.Button
}

func newInterceptTab(st *store.Store) fyne.CanvasObject {
	t := &interceptTab{projectStore: st}
	return t.build()
}

func (t *interceptTab) build() fyne.CanvasObject {
	t.toggle = widget.NewCheck("Intercept ON", func(on bool) {
		t.projectStore.Intercept.SetEnabled(on)
		if !on {
			// If a request is waiting on Reply, forward it unmodified.
			// Without this the proxy goroutine blocks forever.
			if t.pending != nil {
				p := t.pending
				t.pending = nil
				p.Reply <- store.Decision{Forward: true, Request: p.Request, Body: p.Body}
			}
			t.clearEditor()
		}
	})

	t.editor = widget.NewMultiLineEntry()
	t.editor.SetPlaceHolder("No request intercepted yet.")
	t.editor.TextStyle = fyne.TextStyle{Monospace: true}
	t.editor.Wrapping = fyne.TextWrapOff
	t.editor.Disable()

	t.forward = widget.NewButtonWithIcon("Forward", theme.ConfirmIcon(), func() {
		t.decide(true)
	})
	t.drop = widget.NewButtonWithIcon("Drop", theme.DeleteIcon(), func() {
		t.decide(false)
	})
	t.forward.Importance = widget.HighImportance
	t.forward.Disable()
	t.drop.Disable()

	buttons := container.NewHBox(t.forward, t.drop)

	content := container.NewBorder(
		container.NewVBox(t.toggle, widget.NewSeparator()),
		buttons,
		nil, nil,
		t.editor,
	)

	go t.watchQueue()

	return content
}

func (t *interceptTab) watchQueue() {
	for pending := range t.projectStore.Intercept.Queue() {
		pendingRequest := pending
		fyne.Do(func() {
			t.showRequest(pendingRequest)
		})
	}
}

func (t *interceptTab) showRequest(p *store.PendingRequest) {
	t.pending = p
	t.editor.Enable()
	t.editor.SetText(formatRawRequest(p.Request, p.Body))
	t.forward.Enable()
	t.drop.Enable()
}

func (t *interceptTab) decide(forward bool) {
	if t.pending == nil {
		return
	}
	pendingRequest := t.pending
	rawText := t.editor.Text // capture BEFORE clearing
	t.pending = nil
	t.clearEditor()

	if forward {
		req, body, err := parseRawRequest(rawText, pendingRequest.Request)
		if err != nil {
			logger.Error("intercept: parse edited request: %v — forwarding original", err)
			req = pendingRequest.Request
			body = pendingRequest.Body
		}
		pendingRequest.Reply <- store.Decision{Forward: true, Request: req, Body: body}
	} else {
		pendingRequest.Reply <- store.Decision{Forward: false}
	}
}

func (t *interceptTab) clearEditor() {
	t.editor.SetText("")
	t.editor.Disable()
	t.forward.Disable()
	t.drop.Disable()
}

func formatRawRequest(req *http.Request, body []byte) string {
	var builder bytes.Buffer
	path := req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(&builder, "%s %s HTTP/1.1\r\n", req.Method, path)
	fmt.Fprintf(&builder, "Host: %s\r\n", req.Host)
	for headerKey, headerValues := range req.Header {
		for _, headerValue := range headerValues {
			fmt.Fprintf(&builder, "%s: %s\r\n", headerKey, headerValue)
		}
	}
	builder.WriteString("\r\n")
	if len(body) > 0 {
		builder.Write(body)
	}
	return builder.String()
}

func parseRawRequest(raw string, original *http.Request) (*http.Request, []byte, error) {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewBufferString(raw)))
	if err != nil {
		return nil, nil, err
	}

	req.URL.Scheme = original.URL.Scheme
	req.URL.Host = original.URL.Host
	if req.Host == "" {
		req.Host = original.Host
	}

	var body []byte
	if req.Body != nil {
		buf := new(bytes.Buffer)
		buf.ReadFrom(req.Body)
		req.Body.Close()
		body = buf.Bytes()
	}
	req.Body = http.NoBody

	return req, body, nil
}
