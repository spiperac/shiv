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

	"github.com/shiv/internal/store"
)

type interceptTab struct {
	st      *store.Store
	pending *store.PendingRequest // currently displayed request, nil if none

	toggle  *widget.Check
	editor  *widget.Entry
	forward *widget.Button
	drop    *widget.Button
}

func newInterceptTab(st *store.Store) fyne.CanvasObject {
	t := &interceptTab{st: st}
	return t.build()
}

func (t *interceptTab) build() fyne.CanvasObject {
	t.toggle = widget.NewCheck("Intercept ON", func(on bool) {
		t.st.Intercept.SetEnabled(on)
		if !on {
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

// watchQueue reads pending requests from the gate and shows them in the editor.
func (t *interceptTab) watchQueue() {
	for pending := range t.st.Intercept.Queue() {
		p := pending // capture
		fyne.Do(func() {
			t.showRequest(p)
		})
	}
}

// showRequest displays a pending request in the editor.
func (t *interceptTab) showRequest(p *store.PendingRequest) {
	t.pending = p
	t.editor.Enable()
	t.editor.SetText(formatRawRequest(p.Request, p.Body))
	t.forward.Enable()
	t.drop.Enable()
}

// decide sends the user's decision back to the waiting proxy goroutine.
func (t *interceptTab) decide(forward bool) {
	if t.pending == nil {
		return
	}
	p := t.pending
	t.pending = nil
	t.clearEditor()

	if forward {
		// Parse the (possibly edited) request text back into an http.Request.
		req, body, err := parseRawRequest(t.editor.Text, p.Request)
		if err != nil {
			// If parsing fails, forward the original unmodified request.
			req = p.Request
			body = p.Body
		}
		p.Reply <- store.Decision{Forward: true, Request: req, Body: body}
	} else {
		p.Reply <- store.Decision{Forward: false}
	}
}

func (t *interceptTab) clearEditor() {
	t.editor.SetText("")
	t.editor.Disable()
	t.forward.Disable()
	t.drop.Disable()
}

// formatRawRequest formats an http.Request and body as a raw HTTP/1.1 string.
func formatRawRequest(req *http.Request, body []byte) string {
	var sb bytes.Buffer
	path := req.URL.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(&sb, "%s %s HTTP/1.1\r\n", req.Method, path)
	fmt.Fprintf(&sb, "Host: %s\r\n", req.Host)
	for k, vv := range req.Header {
		for _, v := range vv {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n")
	if len(body) > 0 {
		sb.Write(body)
	}
	return sb.String()
}

// parseRawRequest parses an edited raw HTTP request string back into an
// http.Request. Falls back to the original request on any parse error.
func parseRawRequest(raw string, original *http.Request) (*http.Request, []byte, error) {
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewBufferString(raw)))
	if err != nil {
		return nil, nil, err
	}

	// Restore scheme and host from original since http.ReadRequest strips them.
	req.URL.Scheme = original.URL.Scheme
	req.URL.Host = original.URL.Host
	if req.Host == "" {
		req.Host = original.Host
	}

	body := make([]byte, 0)
	if req.Body != nil {
		buf := new(bytes.Buffer)
		buf.ReadFrom(req.Body)
		req.Body.Close()
		body = buf.Bytes()
	}
	req.Body = http.NoBody

	return req, body, nil
}
