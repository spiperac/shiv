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
)

var severities = []string{"Critical", "High", "Medium", "Low", "Info"}

var severityColors = map[string]widget.Importance{
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

	table      *widget.Table
	notesArea  *readOnlyEntry
	viewReqBtn *widget.Button
	deleteBtn  *widget.Button
	exportBtn  *widget.Button

	selectedIdx int
}

var lootColumns = []string{"Severity", "Title", "Created"}
var lootColumnWidths = []float32{300, 500, 360}

func (l *lootTab) build() fyne.CanvasObject {
	l.table = widget.NewTable(
		func() (int, int) { return len(l.entries) + 1, len(lootColumns) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			lb := obj.(*widget.Label)
			if id.Row == 0 {
				lb.TextStyle = fyne.TextStyle{Bold: true}
				lb.Importance = widget.MediumImportance
				lb.SetText(lootColumns[id.Col])
				return
			}
			lb.TextStyle = fyne.TextStyle{}
			idx := id.Row - 1
			if idx >= len(l.entries) {
				lb.SetText("")
				return
			}
			e := l.entries[idx]
			switch id.Col {
			case 0:
				lb.Importance = severityColors[e.Severity]
				lb.SetText(e.Severity)
			case 1:
				lb.Importance = widget.MediumImportance
				lb.SetText(e.Title)
			case 2:
				lb.Importance = widget.LowImportance
				lb.SetText(e.CreatedAt.Format("2006-01-02 15:04"))
			}
		},
	)
	for i, colWidth := range lootColumnWidths {
		l.table.SetColumnWidth(i, colWidth)
	}

	l.table.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			l.table.UnselectAll()
			return
		}
		idx := id.Row - 1
		if idx >= len(l.entries) {
			return
		}
		l.selectedIdx = idx
		l.deleteBtn.Enable()
		entry := l.entries[idx]
		l.notesArea.SetText(fmt.Sprintf("Severity: %s\nCreated: %s\n\n%s",
			entry.Severity, entry.CreatedAt.Format("2006-01-02 15:04"), entry.Notes))

		if entry.HistoryID != nil {
			l.viewReqBtn.Enable()
		} else {
			l.viewReqBtn.Disable()
		}
	}

	l.notesArea = newReadOnlyEntry()
	l.notesArea.SetPlaceHolder("Select an entry to view details...")

	l.deleteBtn = widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
		if l.selectedIdx < 0 || l.selectedIdx >= len(l.entries) {
			return
		}
		e := l.entries[l.selectedIdx]
		dialog.ShowConfirm("Delete", fmt.Sprintf("Delete '%s'?", e.Title), func(ok bool) {
			if !ok {
				return
			}
			if err := l.projectStore.DeleteLoot(e.ID); err != nil {
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
		l.showAddDialog(nil)
	})
	addBtn.Importance = widget.HighImportance

	l.exportBtn = widget.NewButtonWithIcon("Export Markdown", theme.DocumentSaveIcon(), func() {
		l.exportMarkdown()
	})

	l.viewReqBtn = widget.NewButtonWithIcon("View Request", theme.SearchIcon(), func() {
		if l.selectedIdx < 0 || l.selectedIdx >= len(l.entries) {
			return
		}
		e := l.entries[l.selectedIdx]
		if e.HistoryID == nil {
			return
		}
		l.showLinkedRequest(e)
	})
	l.viewReqBtn.Disable()

	toolbar := container.NewHBox(addBtn, l.deleteBtn, l.viewReqBtn, l.exportBtn)

	notesPane := container.NewBorder(newBoldLabel("Notes"), nil, nil, nil,
		container.NewScroll(l.notesArea))

	split := container.NewVSplit(
		container.NewBorder(toolbar, nil, nil, nil, l.table),
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

func (l *lootTab) showAddDialog(historyID *uint64) {
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
	dialog.ShowCustomConfirm("Add Loot", "Save", "Cancel", sized, func(confirmed bool) {
		if !confirmed {
			return
		}
		title := strings.TrimSpace(titleEntry.Text)
		if title == "" {
			return
		}
		entry := store.LootEntry{
			Title:     title,
			Severity:  severitySelect.Selected,
			Notes:     notesEntry.Text,
			HistoryID: historyID,
		}
		if _, err := l.projectStore.AddLoot(entry); err != nil {
			logger.Error("loot: add: %v", err)
			return
		}
		l.reload()
	}, l.win)
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
		entryData := entryData{
			Severity: entry.Severity,
			Title:    entry.Title,
			Date:     entry.CreatedAt.Format("2006-01-02 15:04"),
			Notes:    entry.Notes,
		}
		if entry.HistoryID != nil {
			transaction, err := l.projectStore.GetTransaction(*entry.HistoryID)
			if err == nil {
				entryData.Request = formatRequest(*transaction)
				entryData.Response = formatResponse(*transaction)
			}
		}
		data.Entries = append(data.Entries, entryData)
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

func (l *lootTab) showLinkedRequest(e store.LootEntry) {
	go func() {
		transaction, err := l.projectStore.GetTransaction(*e.HistoryID)
		if err != nil {
			logger.Error("loot: get linked request: %v", err)
			return
		}
		fyne.Do(func() {
			reqEntry := newReadOnlyEntry()
			reqEntry.SetText(formatRequest(*transaction))

			respEntry := newReadOnlyEntry()
			respEntry.SetText(formatResponse(*transaction))

			inspectBtn := widget.NewButtonWithIcon("Inspector", AppIcon("inspector"), func() {
				showInspectorDialog(*transaction, l.win)
			})

			sendBtn := widget.NewButtonWithIcon("Send to Repeater", theme.MailForwardIcon(), nil)

			reqPane := container.NewBorder(newBoldLabel("Request"), nil, nil, nil,
				container.NewScroll(reqEntry))
			respPane := container.NewBorder(
				container.NewBorder(nil, nil, nil, inspectBtn, newBoldLabel("Response")),
				nil, nil, nil,
				container.NewScroll(respEntry))

			split := container.NewHSplit(reqPane, respPane)
			split.SetOffset(0.5)

			linkedDialog := dialog.NewCustom(
				fmt.Sprintf("Linked Request — %s %s", transaction.Method, transaction.URL),
				"Close",
				container.NewBorder(nil, sendBtn, nil, nil, split),
				l.win,
			)

			sendBtn.OnTapped = func() {
				host, portStr, err := net.SplitHostPort(transaction.Host)
				if err != nil {
					host = transaction.Host
					portStr = "443"
					if !transaction.TLS {
						portStr = "80"
					}
				}
				port, _ := strconv.Atoi(portStr)
				path := pathOf(transaction.URL)
				if len(path) > 20 {
					path = path[:20] + "..."
				}
				name := fmt.Sprintf("%s %s", transaction.Method, path)
				l.repeater.AddTab(name, host, port, transaction.TLS, formatRequest(*transaction))
				linkedDialog.Hide()
			}

			linkedDialog.Show()
			linkedDialog.Resize(fyne.NewSize(900, 600))
		})
	}()
}
