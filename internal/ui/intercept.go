package ui

import (
	"bufio"
	"bytes"
	"net/http"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

type interceptTab struct {
	projectStore *store.Store
	pending      *store.PendingRequest

	toggle     *widget.Check
	editor     *widget.Entry
	forward    *widget.Button
	drop       *widget.Button
	forwardAll *widget.Button
}

func newInterceptTab(projectStore *store.Store) *interceptTab {
	return &interceptTab{projectStore: projectStore}
}

func (t *interceptTab) build() fyne.CanvasObject {
	t.toggle = widget.NewCheck("Intercept ON", func(on bool) {
		t.projectStore.Intercept.SetEnabled(on)
		if !on {
			// SetEnabled(false) triggers bypass internally — all goroutines
			// blocked in Hold are released. We only need to handle whatever
			// the UI is currently displaying.
			if t.pending != nil {
				old := t.pending
				t.pending = nil
				old.Reply <- store.Decision{Forward: true, Request: old.Request, Body: old.Body}
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
	t.forwardAll = widget.NewButtonWithIcon("Forward All", theme.MediaFastForwardIcon(), func() {
		// Forward whatever is currently displayed, then signal the gate to
		// release all goroutines currently blocked in Hold. Future requests
		// will be intercepted normally — ForwardAll is a one-shot release.
		if t.pending != nil {
			old := t.pending
			t.pending = nil
			old.Reply <- store.Decision{Forward: true, Request: old.Request, Body: old.Body}
		}
		t.clearEditor()
		t.projectStore.Intercept.ForwardAll()
	})

	t.forward.Importance = widget.HighImportance
	t.forward.Disable()
	t.drop.Disable()
	t.forwardAll.Disable()

	buttons := container.NewHBox(t.forward, t.drop, t.forwardAll)

	content := container.NewBorder(
		container.NewVBox(t.toggle, widget.NewSeparator()),
		buttons,
		nil, nil,
		t.editor,
	)

	go t.watchQueue()

	return content
}

// watchQueue reads from the gate queue and presents requests to the UI one at
// a time. Cancelled pending (released by bypass before the UI processed them)
// are skipped via the double IsDone check — once on the watchQueue goroutine
// and once inside fyne.Do on the main goroutine.
func (t *interceptTab) watchQueue() {
	for pending := range t.projectStore.Intercept.Queue() {
		p := pending
		// Fast path: already cancelled before we even schedule fyne.Do.
		if p.IsDone() {
			continue
		}
		fyne.Do(func() {
			// Second check: may have been cancelled between the fast path above
			// and this callback executing on the main goroutine.
			if p.IsDone() {
				return
			}
			t.showRequest(p)
		})
	}
}

func (t *interceptTab) showRequest(p *store.PendingRequest) {
	t.pending = p
	t.editor.Enable()
	t.editor.SetText(internalhttp.FormatRequest(p.Request, p.Body))
	t.forward.Enable()
	t.drop.Enable()
	t.forwardAll.Enable()
}

func (t *interceptTab) decide(forward bool) {
	if t.pending == nil {
		return
	}
	pending := t.pending
	rawText := t.editor.Text
	t.pending = nil
	t.clearEditor()

	if forward {
		req, body, err := parseRawRequest(rawText, pending.Request)
		if err != nil {
			logger.Error("intercept: parse edited request: %v — forwarding original", err)
			req = pending.Request
			body = pending.Body
		}
		pending.Reply <- store.Decision{Forward: true, Request: req, Body: body}
	} else {
		pending.Reply <- store.Decision{Forward: false, Request: pending.Request, Body: pending.Body}
	}
}

func (t *interceptTab) clearEditor() {
	t.editor.SetText("")
	t.editor.Disable()
	t.forward.Disable()
	t.drop.Disable()
	t.forwardAll.Disable()
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
