package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/store"
)

var intruderMarkerRegex = regexp.MustCompile(`\$<[^>]+>`)

type intruderResult struct {
	payload    string
	statusCode int
	size       int
	durationMs int64
	err        string
	rawResp    string
	rawReq     string
}

type intruderTab struct {
	win          fyne.Window
	projectStore *store.Store

	reqEditor     *repeaterEntry
	payloadEntry  *widget.Entry
	filterEntry   *widget.Entry
	startBtn      *widget.Button
	stopBtn       *widget.Button
	progressLabel *widget.Label
	responsePane  *readOnlyEntry
	requestPane   *readOnlyEntry

	config store.IntruderConfig

	mu       sync.RWMutex
	results  []intruderResult
	filtered []intruderResult

	table *widget.Table

	running  atomic.Bool
	stopChan chan struct{}
}

func newIntruderTab(win fyne.Window, projectStore *store.Store) fyne.CanvasObject {
	tab := &intruderTab{
		win:          win,
		projectStore: projectStore,
		config:       projectStore.LoadIntruderConfig(),
	}
	return tab.build()
}

var intruderResultColumns = []string{"Payload", "Status", "Size", "Duration"}
var intruderResultWidths = []float32{200, 80, 100, 100}

func (t *intruderTab) showConfigDialog() {
	delayEntry := widget.NewEntry()
	delayEntry.SetText(strconv.Itoa(t.config.DelayMs))
	delayEntry.SetPlaceHolder("0")

	stopOnStatusEntry := widget.NewEntry()
	if t.config.StopOnStatus != 0 {
		stopOnStatusEntry.SetText(strconv.Itoa(t.config.StopOnStatus))
	}
	stopOnStatusEntry.SetPlaceHolder("0 = disabled")

	maxRedirectsEntry := widget.NewEntry()
	maxRedirectsEntry.SetText(strconv.Itoa(t.config.MaxRedirects))
	maxRedirectsEntry.SetPlaceHolder("10")

	followRedirectsSelect := widget.NewSelect([]string{"never", "always", "in-scope"}, nil)
	followRedirectsSelect.SetSelected(t.config.FollowRedirects)

	timeoutEntry := widget.NewEntry()
	timeoutEntry.SetText(strconv.Itoa(t.config.TimeoutMs))
	timeoutEntry.SetPlaceHolder("30000")

	form := widget.NewForm(
		widget.NewFormItem("Delay between requests (ms)", delayEntry),
		widget.NewFormItem("Stop on status code", stopOnStatusEntry),
		widget.NewFormItem("Follow redirects", followRedirectsSelect),
		widget.NewFormItem("Max redirects", maxRedirectsEntry),
		widget.NewFormItem("Timeout (ms)", timeoutEntry),
	)

	sized := container.NewGridWrap(fyne.NewSize(420, 280), form)
	configDialog := dialog.NewCustomConfirm("Attack Configuration", "Save", "Cancel", sized, func(confirmed bool) {
		if !confirmed {
			return
		}

		if delay, err := strconv.Atoi(strings.TrimSpace(delayEntry.Text)); err == nil && delay >= 0 {
			t.config.DelayMs = delay
		}

		if statusText := strings.TrimSpace(stopOnStatusEntry.Text); statusText == "" || statusText == "0" {
			t.config.StopOnStatus = 0
		} else if status, err := strconv.Atoi(statusText); err == nil && status > 0 {
			t.config.StopOnStatus = status
		}

		if maxRedirects, err := strconv.Atoi(strings.TrimSpace(maxRedirectsEntry.Text)); err == nil && maxRedirects >= 0 {
			t.config.MaxRedirects = maxRedirects
		}

		t.config.FollowRedirects = followRedirectsSelect.Selected

		if timeout, err := strconv.Atoi(strings.TrimSpace(timeoutEntry.Text)); err == nil && timeout > 0 {
			t.config.TimeoutMs = timeout
		}

		t.config.RawRequest = t.reqEditor.Text
		t.config.Payloads = t.payloadEntry.Text
		t.projectStore.SaveIntruderConfig(t.config)
	}, t.win)

	closeOnEscape(t.win, configDialog.Dismiss)
	configDialog.Show()
}

func (t *intruderTab) build() fyne.CanvasObject {
	t.reqEditor = newRepeaterEntry()
	t.reqEditor.SetPlaceHolder("Paste raw HTTP request here, mark injection points with $<n>\n\nExample:\nGET /search?q=$<query> HTTP/1.1\nHost: example.com")
	t.reqEditor.SetText(t.config.RawRequest)

	t.payloadEntry = widget.NewMultiLineEntry()
	t.payloadEntry.SetPlaceHolder("One payload per line:\nadmin\npassword\n' OR 1=1--")
	t.payloadEntry.SetMinRowsVisible(8)
	t.payloadEntry.SetText(t.config.Payloads)

	loadPayloadsBtn := widget.NewButtonWithIcon("Load from file", theme.FolderOpenIcon(), func() {
		fileDialog := dialog.NewFileOpen(func(readCloser fyne.URIReadCloser, err error) {
			if err != nil || readCloser == nil {
				return
			}
			defer readCloser.Close()
			buf := new(strings.Builder)
			data := make([]byte, 4096)
			for {
				n, readErr := readCloser.Read(data)
				if n > 0 {
					buf.Write(data[:n])
				}
				if readErr != nil {
					break
				}
			}
			t.payloadEntry.SetText(buf.String())
		}, t.win)
		fileDialog.SetFilter(storage.NewExtensionFileFilter([]string{".txt"}))
		fileDialog.Show()
	})

	t.filterEntry = widget.NewEntry()
	t.filterEntry.SetPlaceHolder("Filter results — payload, status...")
	t.filterEntry.OnChanged = func(_ string) { t.applyFilter() }

	t.progressLabel = widget.NewLabel("")
	t.progressLabel.Importance = widget.LowImportance

	t.startBtn = widget.NewButtonWithIcon("Start Attack", theme.MediaPlayIcon(), func() {
		t.startAttack()
	})
	t.startBtn.Importance = widget.HighImportance

	t.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		t.stopAttack()
	})
	t.stopBtn.Disable()

	configBtn := widget.NewButtonWithIcon("Config", AppIcon("settings"), func() {
		t.showConfigDialog()
	})

	clearBtn := widget.NewButtonWithIcon("Clear Results", AppIcon("delete"), func() {
		t.mu.Lock()
		t.results = nil
		t.filtered = nil
		t.mu.Unlock()
		t.table.Refresh()
		t.progressLabel.SetText("")
		t.responsePane.SetText("")
		t.requestPane.SetText("")
	})

	t.responsePane = newReadOnlyEntry()
	t.responsePane.SetPlaceHolder("Select a result to view response...")

	t.requestPane = newReadOnlyEntry()
	t.requestPane.SetPlaceHolder("Select a result to view request...")

	detailTabs := container.NewAppTabs(
		container.NewTabItem("Response", container.NewScroll(t.responsePane)),
		container.NewTabItem("Request", container.NewScroll(t.requestPane)),
	)
	detailTabs.SetTabLocation(container.TabLocationTop)

	t.table = widget.NewTable(
		func() (int, int) {
			t.mu.RLock()
			defer t.mu.RUnlock()
			return len(t.filtered) + 1, len(intruderResultColumns)
		},
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			label := obj.(*widget.Label)
			if id.Row == 0 {
				label.TextStyle = fyne.TextStyle{Bold: true}
				label.SetText(intruderResultColumns[id.Col])
				return
			}
			label.TextStyle = fyne.TextStyle{}
			t.mu.RLock()
			idx := id.Row - 1
			if idx >= len(t.filtered) {
				t.mu.RUnlock()
				label.SetText("")
				return
			}
			result := t.filtered[idx]
			t.mu.RUnlock()
			switch id.Col {
			case 0:
				label.SetText(result.payload)
			case 1:
				if result.err != "" {
					label.SetText("ERR")
				} else {
					label.SetText(fmt.Sprintf("%d", result.statusCode))
				}
			case 2:
				if result.err != "" {
					label.SetText("-")
				} else {
					label.SetText(fmt.Sprintf("%db", result.size))
				}
			case 3:
				if result.err != "" {
					label.SetText("-")
				} else {
					label.SetText(fmt.Sprintf("%dms", result.durationMs))
				}
			}
		},
	)

	for i, width := range intruderResultWidths {
		t.table.SetColumnWidth(i, width)
	}

	t.table.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			t.table.UnselectAll()
			return
		}
		t.mu.RLock()
		idx := id.Row - 1
		if idx >= len(t.filtered) {
			t.mu.RUnlock()
			return
		}
		result := t.filtered[idx]
		t.mu.RUnlock()
		if result.err != "" {
			t.responsePane.SetText("Error: " + result.err)
			t.requestPane.SetText(result.rawReq)
		} else {
			t.responsePane.SetText(result.rawResp)
			t.requestPane.SetText(result.rawReq)
		}
	}

	toolbar := container.NewHBox(t.startBtn, t.stopBtn, configBtn, clearBtn, t.progressLabel)

	reqPane := container.NewBorder(
		newBoldLabel("Request"),
		nil, nil, nil,
		container.NewScroll(t.reqEditor),
	)

	payloadPane := container.NewBorder(
		container.NewBorder(nil, nil, nil, loadPayloadsBtn, newBoldLabel("Payloads")),
		nil, nil, nil,
		container.NewScroll(t.payloadEntry),
	)

	configSplit := container.NewHSplit(reqPane, payloadPane)
	configSplit.SetOffset(0.6)

	tablePane := container.NewBorder(
		container.NewBorder(nil, nil, newBoldLabel("Results"), nil, t.filterEntry),
		nil, nil, nil,
		t.table,
	)

	resultsSplit := container.NewHSplit(tablePane, detailTabs)
	resultsSplit.SetOffset(0.4)

	mainSplit := container.NewVSplit(configSplit, resultsSplit)
	mainSplit.SetOffset(0.45)

	return container.NewBorder(toolbar, nil, nil, nil, mainSplit)
}

func (t *intruderTab) followRedirect(rawResp string, originalHost string) (string, bool) {
	firstLine := strings.SplitN(rawResp, "\r\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) < 2 {
		return "", false
	}

	var statusCode int
	fmt.Sscanf(parts[1], "%d", &statusCode)
	if statusCode < 300 || statusCode >= 400 {
		return "", false
	}

	// find Location header
	location := ""
	for _, line := range strings.Split(rawResp, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "location:") {
			location = strings.TrimSpace(line[9:])
			break
		}
	}

	if location == "" {
		return "", false
	}

	// check follow scope
	switch t.config.FollowRedirects {
	case "never":
		return "", false
	case "in-scope":
		if !t.projectStore.InScope(location) {
			return "", false
		}
	case "always":
		// always follow
	}

	// build a simple GET request to the redirect location
	var redirectHost string
	var redirectPath string
	var useTLS bool

	if strings.HasPrefix(location, "https://") {
		rest := location[8:]
		useTLS = true
		if slash := strings.Index(rest, "/"); slash >= 0 {
			redirectHost = rest[:slash]
			redirectPath = rest[slash:]
		} else {
			redirectHost = rest
			redirectPath = "/"
		}
	} else if strings.HasPrefix(location, "http://") {
		rest := location[7:]
		useTLS = false
		if slash := strings.Index(rest, "/"); slash >= 0 {
			redirectHost = rest[:slash]
			redirectPath = rest[slash:]
		} else {
			redirectHost = rest
			redirectPath = "/"
		}
	} else {
		// relative redirect — use original host
		redirectHost = originalHost
		redirectPath = location
		useTLS = strings.HasSuffix(originalHost, ":443")
	}

	port := 443
	if !useTLS {
		port = 80
	}
	host, portStr, err := parsePort(redirectHost)
	if err == nil {
		redirectHost = host
		fmt.Sscanf(portStr, "%d", &port)
	}

	redirectReq := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\n\r\n", redirectPath, redirectHost)
	return sendRawRequestWithHost(redirectHost, port, useTLS, redirectReq)
}

func parsePort(hostport string) (host, port string, err error) {
	colonIdx := strings.LastIndex(hostport, ":")
	if colonIdx < 0 {
		return "", "", fmt.Errorf("no port")
	}
	return hostport[:colonIdx], hostport[colonIdx+1:], nil
}

func sendRawRequestWithHost(host string, port int, useTLS bool, rawReq string) (string, bool) {
	resp, err := sendRawRequest(host, port, useTLS, rawReq)
	if err != nil {
		return "", false
	}
	return resp, true
}

func (t *intruderTab) startAttack() {
	rawReq := t.reqEditor.Text
	if rawReq == "" {
		dialog.ShowInformation("Error", "Please enter a request.", t.win)
		return
	}

	markers := intruderMarkerRegex.FindAllString(rawReq, -1)
	if len(markers) == 0 {
		dialog.ShowInformation("Error", "No injection points found. Mark them with $<n>.", t.win)
		return
	}

	payloadsText := strings.TrimSpace(t.payloadEntry.Text)
	if payloadsText == "" {
		dialog.ShowInformation("Error", "Please enter at least one payload.", t.win)
		return
	}

	payloads := strings.Split(payloadsText, "\n")
	var cleanPayloads []string
	for _, payload := range payloads {
		payload = strings.TrimSpace(payload)
		if payload != "" {
			cleanPayloads = append(cleanPayloads, payload)
		}
	}

	if len(cleanPayloads) == 0 {
		dialog.ShowInformation("Error", "No valid payloads found.", t.win)
		return
	}

	// persist current request and payloads
	t.config.RawRequest = rawReq
	t.config.Payloads = t.payloadEntry.Text
	t.projectStore.SaveIntruderConfig(t.config)

	t.mu.Lock()
	t.results = nil
	t.filtered = nil
	t.mu.Unlock()
	t.table.Refresh()
	t.responsePane.SetText("")
	t.requestPane.SetText("")

	t.running.Store(true)
	t.stopChan = make(chan struct{})
	t.startBtn.Disable()
	t.stopBtn.Enable()

	config := t.config
	total := len(cleanPayloads) * len(markers)
	done := 0

	go func() {
		for _, marker := range markers {
			for _, payload := range cleanPayloads {
				select {
				case <-t.stopChan:
					fyne.Do(func() {
						t.progressLabel.SetText(fmt.Sprintf("Stopped after %d/%d requests", done, total))
						t.startBtn.Enable()
						t.stopBtn.Disable()
					})
					return
				default:
				}

				if config.DelayMs > 0 {
					time.Sleep(time.Duration(config.DelayMs) * time.Millisecond)
				}

				injected := strings.ReplaceAll(rawReq, marker, payload)
				host, port, useTLS := parseHostFromRaw(injected)

				var result intruderResult
				result.payload = payload
				result.rawReq = injected

				if host == "" {
					result.err = "no Host header found"
				} else {
					start := time.Now()
					resp, err := sendRawRequest(host, port, useTLS, injected)
					elapsed := time.Since(start).Milliseconds()
					if err != nil {
						result.err = err.Error()
					} else {
						// follow redirects
						if config.FollowRedirects != "never" && config.MaxRedirects > 0 {
							for redirectCount := 0; redirectCount < config.MaxRedirects; redirectCount++ {
								redirectResp, followed := t.followRedirect(resp, host)
								if !followed {
									break
								}
								resp = redirectResp
							}
						}

						result.durationMs = elapsed
						result.size = len(resp)
						result.rawResp = resp
						firstLine := strings.SplitN(resp, "\r\n", 2)[0]
						parts := strings.Fields(firstLine)
						if len(parts) >= 2 {
							fmt.Sscanf(parts[1], "%d", &result.statusCode)
						}
					}
				}

				done++
				shouldStop := config.StopOnStatus != 0 && result.statusCode == config.StopOnStatus
				resultCopy := result

				fyne.Do(func() {
					t.mu.Lock()
					t.results = append(t.results, resultCopy)
					t.mu.Unlock()
					t.applyFilter()
					t.progressLabel.SetText(fmt.Sprintf("%d/%d", done, total))
				})

				if shouldStop {
					fyne.Do(func() {
						t.progressLabel.SetText(fmt.Sprintf("Stopped — status %d matched after %d requests", config.StopOnStatus, done))
						t.startBtn.Enable()
						t.stopBtn.Disable()
					})
					return
				}
			}
		}

		fyne.Do(func() {
			t.running.Store(false)
			t.startBtn.Enable()
			t.stopBtn.Disable()
			t.progressLabel.SetText(fmt.Sprintf("Done — %d requests", done))
		})
	}()
}

func (t *intruderTab) stopAttack() {
	if t.stopChan != nil {
		close(t.stopChan)
	}
	t.running.Store(false)
}

func (t *intruderTab) applyFilter() {
	query := strings.ToLower(t.filterEntry.Text)

	t.mu.RLock()
	var filtered []intruderResult
	for _, result := range t.results {
		if query != "" {
			searchable := strings.ToLower(fmt.Sprintf("%s %d %s", result.payload, result.statusCode, result.err))
			if !strings.Contains(searchable, query) {
				continue
			}
		}
		filtered = append(filtered, result)
	}
	t.mu.RUnlock()

	t.mu.Lock()
	t.filtered = filtered
	t.mu.Unlock()

	t.table.Refresh()
}
