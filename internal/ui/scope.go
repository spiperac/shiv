package ui

import (
	"net"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

func showScopeDialog(st *store.Store, win fyne.Window) {
	entries, err := st.AllScopeEntries()
	if err != nil {
		logger.Error("scope: load entries: %v", err)
		entries = nil
	}

	// we keep a local copy to drive the list
	type row struct {
		id   int64
		host string
	}
	rows := make([]row, len(entries))
	for i, e := range entries {
		rows[i] = row{e.ID, e.Host}
	}

	var list *widget.List
	var deleteBtn *widget.Button
	selectedIdx := -1

	list = widget.NewList(
		func() int { return len(rows) },
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			obj.(*widget.Label).SetText(rows[i].host)
		},
	)
	list.OnSelected = func(i widget.ListItemID) {
		selectedIdx = i
		deleteBtn.Enable()
	}
	list.OnUnselected = func(_ widget.ListItemID) {
		selectedIdx = -1
		deleteBtn.Disable()
	}

	hostEntry := widget.NewEntry()
	hostEntry.SetPlaceHolder("example.com")

	addBtn := widget.NewButtonWithIcon("Add", AppIcon("save"), func() {
		host := strings.TrimSpace(hostEntry.Text)
		if host == "" {
			return
		}
		// strip protocol if someone pastes a URL
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		// strip path
		if idx := strings.Index(host, "/"); idx >= 0 {
			host = host[:idx]
		}
		// strip port, avoid breaking ipv6
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if err := st.AddScopeEntry(host); err != nil {
			logger.Error("scope: add entry: %v", err)
			return
		}
		newEntries, _ := st.AllScopeEntries()
		rows = make([]row, len(newEntries))
		for i, e := range newEntries {
			rows[i] = row{e.ID, e.Host}
		}
		hostEntry.SetText("")
		list.Refresh()
	})
	addBtn.Importance = widget.HighImportance

	deleteBtn = widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
		if selectedIdx < 0 || selectedIdx >= len(rows) {
			return
		}
		id := rows[selectedIdx].id
		if err := st.DeleteScopeEntry(id); err != nil {
			logger.Error("scope: delete entry: %v", err)
			return
		}
		newEntries, _ := st.AllScopeEntries()
		rows = make([]row, len(newEntries))
		for i, e := range newEntries {
			rows[i] = row{e.ID, e.Host}
		}
		selectedIdx = -1
		deleteBtn.Disable()
		list.Refresh()
	})
	deleteBtn.Disable()

	inputRow := container.NewBorder(nil, nil, nil, addBtn, hostEntry)

	content := container.NewBorder(
		inputRow,
		deleteBtn,
		nil, nil,
		list,
	)
	content.Resize(fyne.NewSize(400, 300))

	d := dialog.NewCustom("Scope", "Close", content, win)
	d.Resize(fyne.NewSize(400, 350))
	d.Show()
}
