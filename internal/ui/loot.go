package ui

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"text/template"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/assets"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

var severities = []string{"Critical", "High", "Medium", "Low", "Info"}

var severityImportance = map[string]widget.Importance{
	"Critical": widget.DangerImportance,
	"High":     widget.DangerImportance,
	"Medium":   widget.WarningImportance,
	"Low":      widget.MediumImportance,
	"Info":     widget.LowImportance,
}

type lootTab struct {
	projectStore *store.Store
	win          fyne.Window
	repeater     *repeaterTab
	entries      []store.LootEntry

	table      *widgets.DataTable
	notesArea  *widgets.TextView
	viewReqBtn *widget.Button
	deleteBtn  *widget.Button
	exportBtn  *widget.Button

	selectedIdx int
}

var lootColumns = []widgets.DataTableColumn{
	{Header: "Severity", Width: 300},
	{Header: "Title", Width: 500},
	{Header: "Created", Width: 360},
}

func newLootTab(projectStore *store.Store, win fyne.Window, repeater *repeaterTab) *lootTab {
	return &lootTab{
		projectStore: projectStore,
		win:          win,
		repeater:     repeater,
		selectedIdx:  -1,
	}
}

func (l *lootTab) build() fyne.CanvasObject {
	l.table = widgets.NewDataTable()
	l.table.SetWindow(l.win)
	l.table.Columns = lootColumns
	l.table.RowCount = func() int {
		return len(l.entries)
	}
	l.table.CellValue = func(row, col int) string {
		if row >= len(l.entries) {
			return ""
		}
		entry := l.entries[row]
		switch col {
		case 0:
			return entry.Severity
		case 1:
			return entry.Title
		case 2:
			return entry.CreatedAt.Format("2006-01-02 15:04")
		}
		return ""
	}
	l.table.CellStyle = func(row, col int) widget.Importance {
		if col != 0 || row >= len(l.entries) {
			return widget.MediumImportance
		}
		if imp, ok := severityImportance[l.entries[row].Severity]; ok {
			return imp
		}
		return widget.MediumImportance
	}
	l.table.RowID = func(row int) int64 {
		if row >= len(l.entries) {
			return 0
		}
		return int64(l.entries[row].ID)
	}
	l.table.OnSelect = func(row int) {
		if row >= len(l.entries) {
			return
		}
		l.selectedIdx = row
		l.deleteBtn.Enable()
		entry := l.entries[row]
		l.notesArea.SetText(fmt.Sprintf("Severity: %s\nCreated: %s\n\n%s",
			entry.Severity, entry.CreatedAt.Format("2006-01-02 15:04"), entry.Notes))
		if entry.HistoryID != nil || entry.RawRequest != "" {
			l.viewReqBtn.Enable()
		} else {
			l.viewReqBtn.Disable()
		}
	}
	l.table.MenuItems = func(row int) []widgets.ContextMenuItem {
		if row >= len(l.entries) {
			return nil
		}
		entry := l.entries[row]
		return []widgets.ContextMenuItem{
			{
				Label: "Delete",
				Action: func() {
					dialog.ShowConfirm("Delete", fmt.Sprintf("Delete '%s'?", entry.Title), func(ok bool) {
						if !ok {
							return
						}
						if err := l.projectStore.DeleteLoot(entry.ID); err != nil {
							logger.Error("loot: delete: %v", err)
							return
						}
						l.reload()
						l.selectedIdx = -1
						l.deleteBtn.Disable()
						l.notesArea.SetText("")
					}, l.win)
				},
			},
			{
				Label: "View Request",
				Action: func() {
					if entry.HistoryID != nil || entry.RawRequest != "" {
						l.showLinkedRequest(entry)
					}
				},
			},
		}
	}

	tableObj := l.table.Build()

	l.notesArea = widgets.NewTextView()
	l.notesArea.SetWindow(l.win)
	l.notesArea.SetPlaceHolder("Select an entry to view details...")

	l.deleteBtn = widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		if l.selectedIdx < 0 || l.selectedIdx >= len(l.entries) {
			return
		}
		entry := l.entries[l.selectedIdx]
		dialog.ShowConfirm("Delete", fmt.Sprintf("Delete '%s'?", entry.Title), func(ok bool) {
			if !ok {
				return
			}
			if err := l.projectStore.DeleteLoot(entry.ID); err != nil {
				logger.Error("loot: delete: %v", err)
				return
			}
			l.reload()
			l.selectedIdx = -1
			l.deleteBtn.Disable()
			l.notesArea.SetText("")
		}, l.win)
	})
	l.deleteBtn.Disable()

	addBtn := widget.NewButtonWithIcon("Add", theme.ContentAddIcon(), func() {
		l.showAddDialog(nil, "", "")
	})
	addBtn.Importance = widget.HighImportance

	l.exportBtn = widget.NewButtonWithIcon("Export Markdown", theme.DocumentSaveIcon(), func() {
		l.exportMarkdown()
	})

	l.viewReqBtn = widget.NewButtonWithIcon("View Request", theme.SearchIcon(), func() {
		if l.selectedIdx < 0 || l.selectedIdx >= len(l.entries) {
			return
		}
		l.showLinkedRequest(l.entries[l.selectedIdx])
	})
	l.viewReqBtn.Disable()

	toolbar := container.NewHBox(addBtn, l.deleteBtn, l.viewReqBtn, l.exportBtn)

	notesPane := container.NewBorder(newBoldLabel("Notes"), nil, nil, nil,
		l.notesArea.Build())

	split := container.NewVSplit(
		container.NewBorder(toolbar, nil, nil, nil, tableObj),
		notesPane,
	)
	split.SetOffset(0.6)

	l.reload()
	return split
}

func (l *lootTab) reload() {
	entries, err := l.projectStore.AllLoot()
	if err != nil {
		logger.Error("loot: load: %v", err)
		return
	}
	l.entries = entries
	l.table.Refresh()
}

func (l *lootTab) showAddDialog(historyID *uint64, rawRequest string, rawResponse string) {
	titleEntry := widget.NewEntry()
	titleEntry.SetPlaceHolder("e.g. Admin credentials found")

	severitySelect := widget.NewSelect(severities, nil)
	severitySelect.SetSelected("High")

	notesEntry := widget.NewMultiLineEntry()
	notesEntry.SetPlaceHolder("Describe the finding, include evidence, impact...")
	notesEntry.SetMinRowsVisible(6)

	form := widget.NewForm(
		widget.NewFormItem("Title", titleEntry),
		widget.NewFormItem("Severity", severitySelect),
		widget.NewFormItem("Notes", notesEntry),
	)

	sized := container.NewGridWrap(fyne.NewSize(500, 350), form)
	addLootDialog := dialog.NewCustomConfirm("Add Loot", "Save", "Cancel", sized, func(confirmed bool) {
		if !confirmed {
			return
		}
		title := strings.TrimSpace(titleEntry.Text)
		if title == "" {
			return
		}
		entry := store.LootEntry{
			Title:       title,
			Severity:    severitySelect.Selected,
			Notes:       notesEntry.Text,
			HistoryID:   historyID,
			RawRequest:  rawRequest,
			RawResponse: rawResponse,
		}
		if _, err := l.projectStore.AddLoot(entry); err != nil {
			logger.Error("loot: add: %v", err)
			return
		}
		l.reload()
	}, l.win)
	closeOnEscape(l.win, addLootDialog.Dismiss)
	addLootDialog.Show()
}

func (l *lootTab) exportMarkdown() {
	entries, err := l.projectStore.AllLoot()
	if err != nil {
		logger.Error("loot: export: %v", err)
		return
	}
	if len(entries) == 0 {
		dialog.ShowInformation("Export", "No loot entries to export.", l.win)
		return
	}

	type entryData struct {
		Severity string
		Title    string
		Date     string
		Notes    string
		Request  string
		Response string
	}

	type templateData struct {
		Generated string
		Entries   []entryData
	}

	data := templateData{
		Generated: time.Now().Format("2006-01-02 15:04"),
	}

	for _, entry := range entries {
		ed := entryData{
			Severity: entry.Severity,
			Title:    entry.Title,
			Date:     entry.CreatedAt.Format("2006-01-02 15:04"),
			Notes:    entry.Notes,
		}
		if entry.HistoryID != nil {
			tx, err := l.projectStore.GetTransaction(*entry.HistoryID)
			if err == nil {
				ed.Request = formatRequest(*tx)
				ed.Response = formatResponse(*tx)
			}
		} else {
			ed.Request = entry.RawRequest
			ed.Response = entry.RawResponse
		}
		data.Entries = append(data.Entries, ed)
	}

	tmpl, err := template.New("findings").Parse(assets.FindingsTemplate)
	if err != nil {
		logger.Error("loot: parse template: %v", err)
		dialog.ShowError(err, l.win)
		return
	}

	var builder strings.Builder
	if err := tmpl.Execute(&builder, data); err != nil {
		logger.Error("loot: execute template: %v", err)
		dialog.ShowError(err, l.win)
		return
	}

	saveDialog := dialog.NewFileSave(func(writeCloser fyne.URIWriteCloser, err error) {
		if err != nil || writeCloser == nil {
			return
		}
		defer writeCloser.Close()
		if _, err := writeCloser.Write([]byte(builder.String())); err != nil {
			logger.Error("loot: write export: %v", err)
			dialog.ShowError(err, l.win)
		}
	}, l.win)
	saveDialog.SetFileName(fmt.Sprintf("findings-%s.md", time.Now().Format("2006-01-02")))
	saveDialog.Show()
}

func (l *lootTab) showLinkedRequest(entry store.LootEntry) {
	if entry.HistoryID != nil {
		l.showLinkedRequestFromHistory(entry)
	} else {
		l.showLinkedRequestFromRaw(entry)
	}
}

func (l *lootTab) showLinkedRequestFromHistory(entry store.LootEntry) {
	go func() {
		tx, err := l.projectStore.GetTransaction(*entry.HistoryID)
		if err != nil {
			logger.Error("loot: get linked request: %v", err)
			return
		}
		fyne.Do(func() {
			l.showRequestResponseDialog(
				fmt.Sprintf("Linked Request — %s %s", tx.Method, tx.URL),
				formatRequest(*tx),
				formatResponse(*tx),
				tx.Host,
				tx.TLS,
			)
		})
	}()
}

func (l *lootTab) showLinkedRequestFromRaw(entry store.LootEntry) {
	host, _, useTLS := parseHostFromRaw(entry.RawRequest)
	l.showRequestResponseDialog("Linked Request", entry.RawRequest, entry.RawResponse, host, useTLS)
}

func (l *lootTab) showRequestResponseDialog(title, rawRequest, rawResponse, host string, useTLS bool) {
	reqEntry := widgets.NewTextView()
	reqEntry.SetWindow(l.win)
	reqEntry.SetText(rawRequest)

	respEntry := widgets.NewTextView()
	respEntry.SetWindow(l.win)
	respEntry.SetText(rawResponse)

	sendBtn := widget.NewButtonWithIcon("Send to Repeater", theme.MailForwardIcon(), nil)

	reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil,
		reqEntry.Build())
	respPane := container.NewBorder(newBoldLabel("Response"), nil, nil, nil,
		respEntry.Build())

	split := container.NewHSplit(reqPane, respPane)
	split.SetOffset(0.5)

	linkedDialog := dialog.NewCustom(
		title,
		"Close",
		container.NewBorder(nil, sendBtn, nil, nil, split),
		l.win,
	)

	sendBtn.OnTapped = func() {
		hostOnly, portStr, err := net.SplitHostPort(host)
		if err != nil {
			hostOnly = host
			if useTLS {
				portStr = "443"
			} else {
				portStr = "80"
			}
		}
		port, _ := strconv.Atoi(portStr)
		path := pathOf(extractURLFromRaw(rawRequest))
		if len(path) > 20 {
			path = path[:20] + "..."
		}
		name := fmt.Sprintf("%s %s", extractMethodFromRaw(rawRequest), path)
		l.repeater.AddTab(name, hostOnly, port, useTLS, rawRequest)
		linkedDialog.Hide()
	}

	closeOnEscape(l.win, linkedDialog.Dismiss)
	linkedDialog.Show()
	linkedDialog.Resize(fyne.NewSize(900, 600))
}

// extractURLFromRaw extracts the URL path from the first line of a raw HTTP request.
func extractURLFromRaw(rawRequest string) string {
	firstLine := strings.SplitN(rawRequest, "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) >= 2 {
		return parts[1]
	}
	return "/"
}

// extractMethodFromRaw extracts the HTTP method from the first line of a raw HTTP request.
func extractMethodFromRaw(rawRequest string) string {
	firstLine := strings.SplitN(rawRequest, "\n", 2)[0]
	parts := strings.Fields(firstLine)
	if len(parts) >= 1 {
		return parts[0]
	}
	return "GET"
}
