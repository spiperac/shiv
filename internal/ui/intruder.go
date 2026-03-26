package ui

import (
	"fmt"
	"net"
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

	internalhttp "github.com/shiv/internal/http"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
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
	repeater     *repeaterTab
	loot         *lootTab

	reqEditor     *widgets.TextViewEntry
	payloadEntry  *widget.Entry
	filterEntry   *widget.Entry
	startBtn      *widget.Button
	stopBtn       *widget.Button
	progressLabel *widget.Label
	responsePane  *widgets.TextView
	requestPane   *widgets.TextView

	sendToRepeaterBtn *widget.Button
	sendToLootBtn     *widget.Button

	selectedResult *intruderResult

	config store.IntruderConfig

	mu       sync.RWMutex
	results  []intruderResult
	filtered []intruderResult

	table *widgets.DataTable

	running  atomic.Bool
	stopChan chan struct{}
}

func newIntruderTab(win fyne.Window, projectStore *store.Store, repeater *repeaterTab, loot *lootTab) *intruderTab {
	return &intruderTab{
		win:          win,
		projectStore: projectStore,
		repeater:     repeater,
		loot:         loot,
		config:       projectStore.LoadIntruderConfig(),
	}
}

var intruderColumns = []widgets.DataTableColumn{
	{Header: "Payload", Width: 200},
	{Header: "Status", Width: 80},
	{Header: "Size", Width: 100},
	{Header: "Duration", Width: 100},
}

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
		t.config.RawRequest = t.reqEditor.GetText()
		t.config.Payloads = t.payloadEntry.Text
		t.projectStore.SaveIntruderConfig(t.config)
	}, t.win)

	closeOnEscape(t.win, configDialog.Dismiss)
	configDialog.Show()
}

func (t *intruderTab) build() fyne.CanvasObject {
	t.reqEditor = widgets.NewTextViewEntry()
	t.reqEditor.SetWindow(t.win)
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
		t.selectedResult = nil
		t.sendToRepeaterBtn.Disable()
		t.sendToLootBtn.Disable()
	})

	t.sendToRepeaterBtn = widget.NewButtonWithIcon("Repeater", theme.MailForwardIcon(), func() {
		if t.selectedResult == nil {
			return
		}
		host, port, useTLS := internalhttp.ParseHostFromRaw(t.selectedResult.rawReq)
		path := PathOf(extractURLFromRaw(t.selectedResult.rawReq))
		if len(path) > 20 {
			path = path[:20] + "..."
		}
		t.repeater.AddTab(fmt.Sprintf("Intruder %s", path), host, port, useTLS, t.selectedResult.rawReq)
	})
	t.sendToRepeaterBtn.Disable()

	t.sendToLootBtn = widget.NewButtonWithIcon("Loot", AppIcon("loot"), func() {
		if t.selectedResult == nil {
			return
		}
		t.loot.showAddDialog(nil, t.selectedResult.rawReq, t.selectedResult.rawResp)
	})
	t.sendToLootBtn.Disable()

	t.responsePane = widgets.NewTextView()
	t.responsePane.SetWindow(t.win)
	t.responsePane.SetPlaceHolder("Select a result to view response...")

	t.requestPane = widgets.NewTextView()
	t.requestPane.SetWindow(t.win)
	t.requestPane.SetPlaceHolder("Select a result to view request...")

	t.table = widgets.NewDataTable()
	t.table.SetWindow(t.win)
	t.table.Columns = intruderColumns
	t.table.RowCount = func() int {
		t.mu.RLock()
		defer t.mu.RUnlock()
		return len(t.filtered)
	}
	t.table.CellValue = func(row, col int) string {
		t.mu.RLock()
		defer t.mu.RUnlock()
		if row >= len(t.filtered) {
			return ""
		}
		result := t.filtered[row]
		switch col {
		case 0:
			return result.payload
		case 1:
			if result.err != "" {
				return "ERR"
			}
			return fmt.Sprintf("%d", result.statusCode)
		case 2:
			if result.err != "" {
				return "-"
			}
			return fmt.Sprintf("%db", result.size)
		case 3:
			if result.err != "" {
				return "-"
			}
			return fmt.Sprintf("%dms", result.durationMs)
		}
		return ""
	}
	t.table.CellStyle = func(row, col int) widget.Importance {
		if col != 1 {
			return widget.MediumImportance
		}
		t.mu.RLock()
		defer t.mu.RUnlock()
		if row >= len(t.filtered) {
			return widget.MediumImportance
		}
		result := t.filtered[row]
		if result.err != "" {
			return widget.DangerImportance
		}
		switch {
		case result.statusCode >= 500:
			return widget.DangerImportance
		case result.statusCode >= 400:
			return widget.WarningImportance
		case result.statusCode >= 200 && result.statusCode < 300:
			return widget.SuccessImportance
		}
		return widget.MediumImportance
	}
	t.table.RowID = func(row int) int64 {
		return int64(row)
	}
	t.table.OnSelect = func(row int) {
		t.mu.RLock()
		if row >= len(t.filtered) {
			t.mu.RUnlock()
			return
		}
		result := t.filtered[row]
		t.mu.RUnlock()

		t.selectedResult = &result
		t.responsePane.SetText(result.rawResp)
		t.requestPane.SetText(result.rawReq)
		t.sendToRepeaterBtn.Enable()
		t.sendToLootBtn.Enable()
	}
	t.table.MenuItems = func(row int) []widgets.ContextMenuItem {
		t.mu.RLock()
		if row >= len(t.filtered) {
			t.mu.RUnlock()
			return nil
		}
		result := t.filtered[row]
		t.mu.RUnlock()
		return t.contextMenuItems(result)
	}

	tableObj := t.table.Build()

	detailPane := container.NewHSplit(
		container.NewBorder(newBoldLabel("Request"), nil, nil, nil, t.requestPane.Build()),
		container.NewBorder(newBoldLabel("Response"), nil, nil, nil, t.responsePane.Build()),
	)

	toolbar := container.NewHBox(t.startBtn, t.stopBtn, configBtn, clearBtn, t.progressLabel)

	payloadPane := container.NewBorder(
		newBoldLabel("Payloads"),
		container.NewHBox(loadPayloadsBtn),
		nil, nil,
		t.payloadEntry,
	)

	configSplit := container.NewHSplit(
		container.NewBorder(newBoldLabel("Request"), nil, nil, nil, t.reqEditor.Build()),
		payloadPane,
	)
	configSplit.SetOffset(0.6)

	tablePane := container.NewBorder(
		container.NewBorder(nil, nil, newBoldLabel("Results"), nil, t.filterEntry),
		nil, nil, nil,
		tableObj,
	)

	resultsSplit := container.NewHSplit(tablePane, detailPane)
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

	switch t.config.FollowRedirects {
	case "never":
		return "", false
	case "in-scope":
		if !t.projectStore.InScope(location) {
			return "", false
		}
	}

	var redirectHost, redirectPath string
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
		redirectHost = originalHost
		redirectPath = location
		useTLS = strings.HasSuffix(originalHost, ":443")
	}

	port := 443
	if !useTLS {
		port = 80
	}
	if host, portStr, err := net.SplitHostPort(redirectHost); err == nil {
		redirectHost = host
		fmt.Sscanf(portStr, "%d", &port)
	}

	redirectReq := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\n\r\n", redirectPath, redirectHost)
	result, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
		Host:   redirectHost,
		Port:   port,
		TLS:    useTLS,
		RawReq: redirectReq,
	})
	if err != nil {
		return "", false
	}
	return result.Raw, true
}

func (t *intruderTab) startAttack() {
	rawReq := t.reqEditor.GetText()
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

	t.config.RawRequest = rawReq
	t.config.Payloads = t.payloadEntry.Text
	t.projectStore.SaveIntruderConfig(t.config)

	t.mu.Lock()
	t.results = nil
	t.filtered = nil
	t.mu.Unlock()
	t.table.ClearSelection()
	t.table.Refresh()
	t.responsePane.SetText("")
	t.requestPane.SetText("")
	t.selectedResult = nil
	t.sendToRepeaterBtn.Disable()
	t.sendToLootBtn.Disable()

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
				host, port, useTLS := internalhttp.ParseHostFromRaw(injected)

				var result intruderResult
				result.payload = payload
				result.rawReq = injected

				if host == "" {
					result.err = "no Host header found"
				} else {
					start := time.Now()
					resp, err := internalhttp.SendRaw(internalhttp.RawRequestOptions{
						Host:   host,
						Port:   port,
						TLS:    useTLS,
						RawReq: injected,
					})
					elapsed := time.Since(start).Milliseconds()
					if err != nil {
						result.err = err.Error()
					} else {
						rawResp := resp.Raw
						if config.FollowRedirects != "never" && config.MaxRedirects > 0 {
							for redirectCount := 0; redirectCount < config.MaxRedirects; redirectCount++ {
								redirectResp, followed := t.followRedirect(rawResp, host)
								if !followed {
									break
								}
								rawResp = redirectResp
							}
						}
						result.durationMs = elapsed
						result.size = len(rawResp)
						result.rawResp = rawResp
						firstLine := strings.SplitN(rawResp, "\r\n", 2)[0]
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

func (t *intruderTab) contextMenuItems(result intruderResult) []widgets.ContextMenuItem {
	return []widgets.ContextMenuItem{
		{
			Label: "Send to Repeater",
			Action: func() {
				host, port, useTLS := internalhttp.ParseHostFromRaw(result.rawReq)
				path := PathOf(extractURLFromRaw(result.rawReq))
				if len(path) > 20 {
					path = path[:20] + "..."
				}
				t.repeater.AddTab(fmt.Sprintf("Intruder %s", path), host, port, useTLS, result.rawReq)
			},
		},
		{
			Label: "Send to Loot",
			Action: func() {
				t.loot.showAddDialog(nil, result.rawReq, result.rawResp)
			},
		},
		{
			Label: "Copy Request",
			Action: func() {
				fyne.CurrentApp().Clipboard().SetContent(result.rawReq)
			},
		},
		{
			Label: "Copy Response",
			Action: func() {
				fyne.CurrentApp().Clipboard().SetContent(result.rawResp)
			},
		},
	}
}
