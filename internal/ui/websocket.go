package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/events"
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

func filterConnections(all []store.WebSocketConnection, query string) []store.WebSocketConnection {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]store.WebSocketConnection, len(all))
		copy(out, all)
		return out
	}
	terms := strings.Fields(q)
	var out []store.WebSocketConnection
	for _, c := range all {
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

func filterFrames(all []store.WebSocketFrame, query string) []store.WebSocketFrame {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]store.WebSocketFrame, len(all))
		copy(out, all)
		return out
	}
	terms := strings.Fields(q)
	var out []store.WebSocketFrame
	for _, f := range all {
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

func buildConnTable(win fyne.Window, filteredConns *[]store.WebSocketConnection) (*widgets.DataTable, fyne.CanvasObject) {
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
	connTable.RowCount = func() int { return len(*filteredConns) }
	connTable.RowID = func(row int) int64 {
		if row >= len(*filteredConns) {
			return 0
		}
		return int64((*filteredConns)[row].ID)
	}
	connTable.CellValue = func(row, col int) string {
		if row >= len(*filteredConns) {
			return ""
		}
		c := (*filteredConns)[row]
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
	return connTable, connTable.Build()
}

func buildFrameTable(win fyne.Window, filteredFrames *[]store.WebSocketFrame) (*widgets.DataTable, fyne.CanvasObject) {
	frameColumns := []widgets.DataTableColumn{
		{Header: "Dir", Width: 90},
		{Header: "Type", Width: 70},
		{Header: "Length", Width: 80},
		{Header: "Time", Width: 150},
	}
	frameTable := widgets.NewDataTable()
	frameTable.SetWindow(win)
	frameTable.Columns = frameColumns
	frameTable.RowCount = func() int { return len(*filteredFrames) }
	frameTable.RowID = func(row int) int64 {
		if row >= len(*filteredFrames) {
			return 0
		}
		return int64((*filteredFrames)[row].ID)
	}
	frameTable.CellValue = func(row, col int) string {
		if row >= len(*filteredFrames) {
			return ""
		}
		f := (*filteredFrames)[row]
		switch col {
		case 0:
			if f.Direction == events.WebSocketClient {
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
		if col != 0 || row >= len(*filteredFrames) {
			return widget.MediumImportance
		}
		if (*filteredFrames)[row].Direction == events.WebSocketClient {
			return widget.HighImportance
		}
		return widget.SuccessImportance
	}
	return frameTable, frameTable.Build()
}

func buildFrameDetail(win fyne.Window, filteredFrames *[]store.WebSocketFrame, frameTable *widgets.DataTable) (*widgets.TextView, fyne.CanvasObject) {
	payloadView := widgets.NewTextView()
	payloadView.SetWindow(win)
	payloadView.SetPlaceHolder("Select a frame to view payload...")

	frameTable.OnSelect = func(row int) {
		if row >= len(*filteredFrames) {
			return
		}
		payload := (*filteredFrames)[row].Payload
		if len(payload) == 0 {
			payloadView.SetText("(empty)")
		} else {
			payloadView.SetText(string(payload))
		}
	}

	payloadPane := container.NewBorder(
		newBoldLabel("Payload"),
		nil, nil, nil,
		payloadView.Build(),
	)
	return payloadView, payloadPane
}

func buildWebSocketContent(projectStore *store.Store, win fyne.Window, repeater *repeaterTab) fyne.CanvasObject {
	var allConns []store.WebSocketConnection
	var filteredConns []store.WebSocketConnection
	var allFrames []store.WebSocketFrame
	var filteredFrames []store.WebSocketFrame
	var selectedConnID uint64

	connFilterEntry := widget.NewEntry()
	connFilterEntry.SetPlaceHolder("Filter — host, url...")
	frameFilterEntry := widget.NewEntry()
	frameFilterEntry.SetPlaceHolder("Filter frames — payload, type...")

	refilterConns := func() { filteredConns = filterConnections(allConns, connFilterEntry.Text) }
	refilterFrames := func() { filteredFrames = filterFrames(allFrames, frameFilterEntry.Text) }

	connTable, connTableObj := buildConnTable(win, &filteredConns)
	frameTable, frameTableObj := buildFrameTable(win, &filteredFrames)
	payloadView, payloadPane := buildFrameDetail(win, &filteredFrames, frameTable)

	loadFrames := func(connID uint64) {
		go func() {
			loaded, err := projectStore.FramesForConnection(connID)
			if err != nil {
				logger.Error("ws ui: load frames %d: %v", connID, err)
				return
			}
			fyne.Do(func() {
				allFrames = loaded
				refilterFrames()
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
			{Label: "Send to Repeater", Action: func() {
				if repeater != nil {
					repeater.AddWSTab(c.Host, c.URL, c.TLS)
				}
			}},
			{Label: "Copy URL", Action: func() {
				fyne.CurrentApp().Clipboard().SetContent(c.URL)
			}},
		}
	}

	connFilterEntry.OnChanged = func(q string) {
		filteredConns = filterConnections(allConns, q)
		connTable.Refresh()
	}
	frameFilterEntry.OnChanged = func(q string) {
		filteredFrames = filterFrames(allFrames, q)
		frameTable.Refresh()
	}

	go func() {
		for conn := range projectStore.WebSocketConnections {
			c := conn
			fyne.Do(func() {
				for _, existing := range allConns {
					if existing.ID == c.ID {
						return
					}
				}
				allConns = append([]store.WebSocketConnection{c}, allConns...)
				refilterConns()
				connTable.Refresh()
			})
		}
	}()

	go func() {
		for frame := range projectStore.WebSocketFrames {
			f := frame
			fyne.Do(func() {
				for i := range allConns {
					if allConns[i].ID == f.ConnectionID {
						allConns[i].FrameCount++
						break
					}
				}
				refilterConns()
				connTable.Refresh()
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
						refilterFrames()
						frameTable.Refresh()
					})
				}()
			})
		}
	}()

	refreshBtn := widget.NewButtonWithIcon("Refresh", AppIcon("history"), func() {
		go func() {
			loaded, err := projectStore.AllWebSocketConnections()
			if err != nil {
				logger.Error("ws ui: load connections: %v", err)
				return
			}
			fyne.Do(func() {
				allConns = loaded
				refilterConns()
				connTable.Refresh()
				if selectedConnID > 0 {
					loadFrames(selectedConnID)
				}
			})
		}()
	})

	go func() {
		loaded, err := projectStore.AllWebSocketConnections()
		if err != nil {
			logger.Error("ws ui: initial load: %v", err)
			return
		}
		fyne.Do(func() {
			allConns = loaded
			filteredConns = filterConnections(allConns, "")
			connTable.Refresh()
		})
	}()

	connFilterBar := container.NewBorder(nil, nil, nil, refreshBtn, connFilterEntry)
	frameFilterBar := container.NewBorder(nil, nil, newBoldLabel("Frames"), nil, frameFilterEntry)

	connPane := container.NewBorder(
		container.NewVBox(newBoldLabel("Connections"), connFilterBar),
		nil, nil, nil, connTableObj,
	)
	framePane := container.NewBorder(frameFilterBar, nil, nil, nil, frameTableObj)

	bottomSplit := container.NewHSplit(framePane, payloadPane)
	bottomSplit.SetOffset(0.4)
	mainSplit := container.NewVSplit(connPane, bottomSplit)
	mainSplit.SetOffset(0.4)
	return mainSplit
}

func opcodeLabel(op events.WebSocketOpcode) string {
	switch op {
	case events.WebSocketText:
		return "Text"
	case events.WebSocketBinary:
		return "Binary"
	case events.WebSocketPing:
		return "Ping"
	case events.WebSocketPong:
		return "Pong"
	case events.WebSocketClose:
		return "Close"
	}
	return fmt.Sprintf("0x%x", int(op))
}
