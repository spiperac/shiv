package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

var wsWin fyne.Window

func showWebSocketWindow(fyneApp fyne.App, projectStore *store.Store, parentWin fyne.Window, repeater *repeaterTab) {
	if wsWin != nil {
		wsWin.RequestFocus()
		return
	}
	wsWin = fyneApp.NewWindow("WebSockets")
	wsWin.Resize(fyne.NewSize(1100, 650))
	wsWin.SetOnClosed(func() { wsWin = nil })
	closeOnEscape(wsWin, wsWin.Close)
	wsWin.SetContent(buildWebSocketContent(projectStore, wsWin, repeater))
	wsWin.Show()
}

func buildWebSocketContent(projectStore *store.Store, win fyne.Window, repeater *repeaterTab) fyne.CanvasObject {

	// ── state ─────────────────────────────────────────────────────────────────

	var allConns []store.WebSocketConnection
	var filteredConns []store.WebSocketConnection
	var allFrames []store.WebSocketFrame
	var filteredFrames []store.WebSocketFrame
	var selectedConnID uint64

	// ── filter functions — always return fresh slices, never alias ────────────

	connFilterEntry := widget.NewEntry()
	connFilterEntry.SetPlaceHolder("Filter — host, url...")

	frameFilterEntry := widget.NewEntry()
	frameFilterEntry.SetPlaceHolder("Filter frames — payload, type...")

	filterConn := func(query string) []store.WebSocketConnection {
		q := strings.ToLower(strings.TrimSpace(query))
		if q == "" {
			out := make([]store.WebSocketConnection, len(allConns))
			copy(out, allConns)
			return out
		}
		terms := strings.Fields(q)
		var out []store.WebSocketConnection
		for _, c := range allConns {
			s := strings.ToLower(c.Host + " " + c.URL)
			ok := true
			for _, t := range terms {
				if !strings.Contains(s, t) {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, c)
			}
		}
		return out
	}

	filterFrame := func(query string) []store.WebSocketFrame {
		q := strings.ToLower(strings.TrimSpace(query))
		if q == "" {
			out := make([]store.WebSocketFrame, len(allFrames))
			copy(out, allFrames)
			return out
		}
		terms := strings.Fields(q)
		var out []store.WebSocketFrame
		for _, f := range allFrames {
			s := strings.ToLower(string(f.Payload) + " " + opcodeLabel(f.Opcode))
			ok := true
			for _, t := range terms {
				if !strings.Contains(s, t) {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, f)
			}
		}
		return out
	}

	// ── connections table ─────────────────────────────────────────────────────

	connColumns := []widgets.DataTableColumn{
		{Header: "ID", Width: 50},
		{Header: "Host", Width: 220},
		{Header: "URL", Width: 380},
		{Header: "TLS", Width: 45},
		{Header: "Frames", Width: 70},
		{Header: "Time", Width: 180},
	}

	connTable := widgets.NewDataTable()
	connTable.SetWindow(win)
	connTable.Columns = connColumns
	connTable.RowCount = func() int { return len(filteredConns) }
	connTable.RowID = func(row int) int64 {
		if row >= len(filteredConns) {
			return 0
		}
		return int64(filteredConns[row].ID)
	}
	connTable.CellValue = func(row, col int) string {
		if row >= len(filteredConns) {
			return ""
		}
		c := filteredConns[row]
		switch col {
		case 0:
			return fmt.Sprintf("%d", c.ID)
		case 1:
			return c.Host
		case 2:
			return c.URL
		case 3:
			if c.TLS {
				return "✓"
			}
			return ""
		case 4:
			return fmt.Sprintf("%d", c.FrameCount)
		case 5:
			return c.Timestamp.Local().Format("2006-01-02 15:04:05")
		}
		return ""
	}

	// ── frames table ─────────────────────────────────────────────────────────

	frameColumns := []widgets.DataTableColumn{
		{Header: "Dir", Width: 90},
		{Header: "Type", Width: 70},
		{Header: "Length", Width: 80},
		{Header: "Time", Width: 150},
	}

	frameTable := widgets.NewDataTable()
	frameTable.SetWindow(win)
	frameTable.Columns = frameColumns
	frameTable.RowCount = func() int { return len(filteredFrames) }
	frameTable.RowID = func(row int) int64 {
		if row >= len(filteredFrames) {
			return 0
		}
		return int64(filteredFrames[row].ID)
	}
	frameTable.CellValue = func(row, col int) string {
		if row >= len(filteredFrames) {
			return ""
		}
		f := filteredFrames[row]
		switch col {
		case 0:
			if f.Direction == store.WebSocketClient {
				return "→ Client"
			}
			return "← Server"
		case 1:
			return opcodeLabel(f.Opcode)
		case 2:
			return fmt.Sprintf("%db", len(f.Payload))
		case 3:
			return f.Timestamp.Local().Format("15:04:05.000")
		}
		return ""
	}
	frameTable.CellStyle = func(row, col int) widget.Importance {
		if col != 0 || row >= len(filteredFrames) {
			return widget.MediumImportance
		}
		if filteredFrames[row].Direction == store.WebSocketClient {
			return widget.HighImportance
		}
		return widget.SuccessImportance
	}

	// ── payload view ──────────────────────────────────────────────────────────

	payloadView := widgets.NewTextView()
	payloadView.SetWindow(win)
	payloadView.SetPlaceHolder("Select a frame to view payload...")

	frameTable.OnSelect = func(row int) {
		if row >= len(filteredFrames) {
			return
		}
		payload := filteredFrames[row].Payload
		if len(payload) == 0 {
			payloadView.SetText("(empty)")
		} else {
			payloadView.SetText(string(payload))
		}
	}

	// ── load frames on connection select ──────────────────────────────────────

	loadFrames := func(connID uint64) {
		go func() {
			loaded, err := projectStore.FramesForConnection(connID)
			if err != nil {
				logger.Error("ws ui: load frames %d: %v", connID, err)
				return
			}
			fyne.Do(func() {
				allFrames = loaded
				filteredFrames = filterFrame(frameFilterEntry.Text)
				frameTable.Refresh()
				payloadView.SetText("")
			})
		}()
	}

	connTable.OnSelect = func(row int) {
		if row >= len(filteredConns) {
			return
		}
		selectedConnID = filteredConns[row].ID
		loadFrames(selectedConnID)
	}

	connTable.MenuItems = func(row int) []widgets.ContextMenuItem {
		if row >= len(filteredConns) {
			return nil
		}
		c := filteredConns[row]
		return []widgets.ContextMenuItem{
			{
				Label: "Send to Repeater",
				Action: func() {
					if repeater != nil {
						name := c.Host
						repeater.AddWSTab(name, c.URL, c.TLS)
					}
				},
			},
			{
				Label: "Copy URL",
				Action: func() {
					fyne.CurrentApp().Clipboard().SetContent(c.URL)
				},
			},
		}
	}

	// ── filter wiring ─────────────────────────────────────────────────────────

	connFilterEntry.OnChanged = func(q string) {
		filteredConns = filterConn(q)
		connTable.Refresh()
	}

	frameFilterEntry.OnChanged = func(q string) {
		filteredFrames = filterFrame(q)
		frameTable.Refresh()
	}

	// ── live streaming updates ────────────────────────────────────────────────

	// Watch for new connections — prepend to allConns, same as HTTP history.
	go func() {
		for conn := range projectStore.WebSocketConnections {
			c := conn
			fyne.Do(func() {
				// Deduplicate by ID in case initial load already has it.
				for _, existing := range allConns {
					if existing.ID == c.ID {
						return
					}
				}
				allConns = append([]store.WebSocketConnection{c}, allConns...)
				filteredConns = filterConn(connFilterEntry.Text)
				connTable.Refresh()
			})
		}
	}()

	// Watch for new frames — reload from DB for the selected connection.
	// Appending live frames directly causes ordering and dedup issues;
	// reloading is correct and fast enough for pentest-scale traffic.
	go func() {
		for frame := range projectStore.WebSocketFrames {
			f := frame
			fyne.Do(func() {
				// Always update frame count on the matching connection.
				for i := range allConns {
					if allConns[i].ID == f.ConnectionID {
						allConns[i].FrameCount++
						break
					}
				}
				filteredConns = filterConn(connFilterEntry.Text)
				connTable.Refresh()

				// Only reload frames if this belongs to the selected connection.
				if f.ConnectionID != selectedConnID {
					return
				}
				go func() {
					loaded, err := projectStore.FramesForConnection(f.ConnectionID)
					if err != nil {
						logger.Error("ws ui: reload frames %d: %v", f.ConnectionID, err)
						return
					}
					fyne.Do(func() {
						allFrames = loaded
						filteredFrames = filterFrame(frameFilterEntry.Text)
						frameTable.Refresh()
					})
				}()
			})
		}
	}()

	// ── refresh button (manual reload) ────────────────────────────────────────

	refreshBtn := widget.NewButtonWithIcon("Refresh", AppIcon("history"), func() {
		go func() {
			loaded, err := projectStore.AllWebSocketConnections()
			if err != nil {
				logger.Error("ws ui: load connections: %v", err)
				return
			}
			fyne.Do(func() {
				allConns = loaded
				filteredConns = filterConn(connFilterEntry.Text)
				connTable.Refresh()
				if selectedConnID > 0 {
					loadFrames(selectedConnID)
				}
			})
		}()
	})

	// ── initial load ──────────────────────────────────────────────────────────

	go func() {
		loaded, err := projectStore.AllWebSocketConnections()
		if err != nil {
			logger.Error("ws ui: initial load: %v", err)
			return
		}
		fyne.Do(func() {
			allConns = loaded
			filteredConns = filterConn("")
			connTable.Refresh()
		})
	}()

	// ── layout ────────────────────────────────────────────────────────────────

	connFilterBar := container.NewBorder(nil, nil, nil, refreshBtn, connFilterEntry)
	connPane := container.NewBorder(
		container.NewVBox(newBoldLabel("Connections"), connFilterBar),
		nil, nil, nil,
		connTable.Build(),
	)

	frameFilterBar := container.NewBorder(nil, nil, newBoldLabel("Frames"), nil, frameFilterEntry)
	framePane := container.NewBorder(
		frameFilterBar,
		nil, nil, nil,
		frameTable.Build(),
	)

	payloadPane := container.NewBorder(
		newBoldLabel("Payload"),
		nil, nil, nil,
		payloadView.Build(),
	)

	bottomSplit := container.NewHSplit(framePane, payloadPane)
	bottomSplit.SetOffset(0.4)

	mainSplit := container.NewVSplit(connPane, bottomSplit)
	mainSplit.SetOffset(0.4)

	return mainSplit
}

func opcodeLabel(op store.WebSocketOpcode) string {
	switch op {
	case store.WebSocketText:
		return "Text"
	case store.WebSocketBinary:
		return "Binary"
	case store.WebSocketPing:
		return "Ping"
	case store.WebSocketPong:
		return "Pong"
	case store.WebSocketClose:
		return "Close"
	}
	return fmt.Sprintf("0x%x", int(op))
}
