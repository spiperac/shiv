package ui

import (
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/gorilla/websocket"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

type wsConnState int

const (
	wsDisconnected wsConnState = iota
	wsConnecting
	wsConnected
)

type wsFrame struct {
	direction string
	payload   string
	ts        time.Time
}

type wsHeaderRow struct {
	key   string
	value string
}

type wsTabState struct {
	r             *repeaterTab
	tab           store.RepeaterTab
	tabID         int64
	wsConn        *websocket.Conn
	connState     wsConnState
	connID        uint64
	customHeaders []wsHeaderRow
	frames        []wsFrame
	frameTable    *widgets.DataTable
	payloadView   *widgets.TextView
	connectBtn    *widget.Button
	sendFrameBtn  *widget.Button
	urlEntry      *widget.Entry
}

func (s *wsTabState) disconnect() {
	if s.wsConn != nil {
		s.wsConn.Close()
		s.wsConn = nil
	}
	s.connState = wsDisconnected
	s.updateConnectBtn()
}

func (s *wsTabState) updateConnectBtn() {
	switch s.connState {
	case wsDisconnected:
		s.connectBtn.SetText("Connect")
		s.connectBtn.Importance = widget.HighImportance
		s.connectBtn.Enable()
		s.sendFrameBtn.Disable()
	case wsConnecting:
		s.connectBtn.SetText("Connecting...")
		s.connectBtn.Importance = widget.MediumImportance
		s.connectBtn.Disable()
		s.sendFrameBtn.Disable()
	case wsConnected:
		s.connectBtn.SetText("Disconnect")
		s.connectBtn.Importance = widget.DangerImportance
		s.connectBtn.Enable()
		s.sendFrameBtn.Enable()
	}
	s.connectBtn.Refresh()
}

func (s *wsTabState) startReadLoop(conn *websocket.Conn) {
	go func() {
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				fyne.Do(func() {
					if s.wsConn == conn {
						s.wsConn = nil
						s.connState = wsDisconnected
						s.updateConnectBtn()
						s.frames = append(s.frames, wsFrame{
							direction: "⚠ Closed",
							payload:   err.Error(),
							ts:        time.Now(),
						})
						s.frameTable.Refresh()
						s.frameTable.OnSelect(len(s.frames) - 1)
					}
				})
				return
			}
			finalPayload := payload
			if s.r.bus != nil {
				result := s.r.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
					ConnectionID: s.connID,
					Timestamp:    time.Now(),
					Direction:    events.WebSocketServer,
					Opcode:       events.WebSocketOpcode(msgType),
					Payload:      payload,
				})
				finalPayload = result.Payload
			}
			f := wsFrame{direction: "← Recv", payload: string(finalPayload), ts: time.Now()}
			fyne.Do(func() {
				s.frames = append(s.frames, f)
				s.frameTable.Refresh()
				s.frameTable.ScrollToRow(len(s.frames) - 1)
				s.frameTable.OnSelect(len(s.frames) - 1)
			})
		}
	}()
}

func (s *wsTabState) showHeadersDialog() {
	editHeaders := make([]wsHeaderRow, len(s.customHeaders))
	copy(editHeaders, s.customHeaders)

	var rows *fyne.Container
	var rebuildRows func()

	rebuildRows = func() {
		rows.RemoveAll()
		for i := range editHeaders {
			idx := i
			keyEntry := widget.NewEntry()
			keyEntry.SetPlaceHolder("Header-Name")
			keyEntry.SetText(editHeaders[idx].key)
			keyEntry.OnChanged = func(v string) { editHeaders[idx].key = v }

			valEntry := widget.NewEntry()
			valEntry.SetPlaceHolder("value")
			valEntry.SetText(editHeaders[idx].value)
			valEntry.OnChanged = func(v string) { editHeaders[idx].value = v }

			deleteBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				editHeaders = append(editHeaders[:idx], editHeaders[idx+1:]...)
				rebuildRows()
			})

			rows.Add(container.NewBorder(nil, nil, nil, deleteBtn,
				container.NewGridWithColumns(2, keyEntry, valEntry),
			))
		}
		rows.Refresh()
	}

	rows = container.NewVBox()
	rebuildRows()

	addBtn := widget.NewButtonWithIcon("Add Header", theme.ContentAddIcon(), func() {
		editHeaders = append(editHeaders, wsHeaderRow{})
		rebuildRows()
	})

	scrolled := container.NewVScroll(rows)
	scrolled.SetMinSize(fyne.NewSize(500, 240))

	body := container.NewBorder(nil, addBtn, nil, nil, scrolled)

	dialog.ShowCustomConfirm("Edit Request Headers", "Save", "Cancel", body, func(save bool) {
		if !save {
			return
		}
		s.customHeaders = s.customHeaders[:0]
		for _, h := range editHeaders {
			if strings.TrimSpace(h.key) != "" {
				s.customHeaders = append(s.customHeaders, h)
			}
		}
	}, s.r.win)
}

func (s *wsTabState) buildFrameTable() {
	frameColumns := []widgets.DataTableColumn{
		{Header: "Dir", Width: 80},
		{Header: "Time", Width: 120},
		{Header: "Payload", Width: 300},
	}

	s.frameTable = widgets.NewDataTable()
	s.frameTable.SetWindow(s.r.win)
	s.frameTable.Columns = frameColumns
	s.frameTable.RowCount = func() int { return len(s.frames) }
	s.frameTable.RowID = func(row int) int64 { return int64(row) }
	s.frameTable.CellValue = func(row, col int) string {
		if row >= len(s.frames) {
			return ""
		}
		f := s.frames[row]
		switch col {
		case 0:
			return f.direction
		case 1:
			return f.ts.Format("15:04:05.000")
		case 2:
			return f.payload
		}
		return ""
	}
	s.frameTable.CellStyle = func(row, col int) widget.Importance {
		if col != 0 || row >= len(s.frames) {
			return widget.MediumImportance
		}
		if s.frames[row].direction == "→ Sent" {
			return widget.HighImportance
		}
		return widget.SuccessImportance
	}
}

func (s *wsTabState) buildPayloadView() {
	s.payloadView = widgets.NewTextView()
	s.payloadView.SetWindow(s.r.win)
	s.payloadView.SetPlaceHolder("Select a frame to view full payload...")

	s.frameTable.OnSelect = func(row int) {
		if row >= len(s.frames) {
			return
		}
		p := s.frames[row].payload
		if p == "" {
			s.payloadView.SetText("(empty)")
		} else {
			s.payloadView.SetText(p)
		}
	}
}

func (s *wsTabState) buildConnectHandler() {
	s.connectBtn.OnTapped = func() {
		if s.connState == wsConnected {
			s.disconnect()
			return
		}
		rawURL := strings.TrimSpace(s.urlEntry.Text)
		if rawURL == "" {
			return
		}
		s.connState = wsConnecting
		s.updateConnectBtn()
		if err := s.r.projectStore.UpdateRepeaterTab(s.tabID, rawURL, ""); err != nil {
				logger.Error("repeater: update ws tab: %v", err)
			}

		reqHeader := http.Header{}
		for _, h := range s.customHeaders {
			if strings.TrimSpace(h.key) != "" {
				cleaned := strings.NewReplacer("\n", "", "\r", "").Replace(h.value)
				reqHeader.Set(h.key, cleaned)
			}
		}

		go func() {
			dialer := websocket.Dialer{
				TLSClientConfig:  &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				HandshakeTimeout: 10 * time.Second,
				NetDial: func(network, addr string) (net.Conn, error) {
					return net.DialTimeout(network, addr, 10*time.Second)
				},
			}
			conn, _, err := dialer.Dial(rawURL, reqHeader)
			fyne.Do(func() {
				if err != nil {
					s.connState = wsDisconnected
					s.updateConnectBtn()
					s.frames = append(s.frames, wsFrame{direction: "⚠ Error", payload: err.Error(), ts: time.Now()})
					s.frameTable.Refresh()
					s.frameTable.OnSelect(len(s.frames) - 1)
					return
				}
				s.wsConn = conn
				s.connState = wsConnected
				s.updateConnectBtn()
				if s.r.bus != nil {
					s.connID = s.r.bus.EmitWebSocketConnection(events.WebSocketConnectionEvent{
						Host:      rawURL,
						URL:       rawURL,
						TLS:       strings.HasPrefix(rawURL, "wss://"),
						Timestamp: time.Now(),
					})
				}
				s.frames = append(s.frames, wsFrame{direction: "✓ Connected", payload: rawURL, ts: time.Now()})
				s.frameTable.Refresh()
				s.frameTable.OnSelect(len(s.frames) - 1)
				s.startReadLoop(conn)
			})
		}()
	}
}

func (s *wsTabState) buildSendHandler(payloadEntry *widget.Entry) {
	s.sendFrameBtn.OnTapped = func() {
		if s.wsConn == nil || s.connState != wsConnected {
			return
		}
		payload := strings.ReplaceAll(strings.ReplaceAll(payloadEntry.Text, "\r\n", ""), "\n", "")
		if payload == "" {
			return
		}
		conn := s.wsConn
		go func() {
			finalPayload := []byte(payload)
			if s.r.bus != nil {
				result := s.r.bus.EmitWebSocketFrame(events.WebSocketFrameEvent{
					ConnectionID: s.connID,
					Timestamp:    time.Now(),
					Direction:    events.WebSocketClient,
					Opcode:       events.WebSocketText,
					Payload:      finalPayload,
				})
				finalPayload = result.Payload
			}
			err := conn.WriteMessage(websocket.TextMessage, finalPayload)
			logger.Info("ws: sending payload bytes: %q", finalPayload)
			fyne.Do(func() {
				if err != nil {
					s.frames = append(s.frames, wsFrame{direction: "⚠ Error", payload: "send failed: " + err.Error(), ts: time.Now()})
					s.frameTable.Refresh()
					s.frameTable.OnSelect(len(s.frames) - 1)
					return
				}
				s.frames = append(s.frames, wsFrame{direction: "→ Sent", payload: string(finalPayload), ts: time.Now()})
				s.frameTable.Refresh()
				s.frameTable.ScrollToRow(len(s.frames) - 1)
				s.frameTable.OnSelect(len(s.frames) - 1)
			})
		}()
	}
}

func (s *wsTabState) buildLayout(payloadEntry *widget.Entry, clearBtn, headersBtn *widget.Button) fyne.CanvasObject {
	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(s.connectBtn, clearBtn, headersBtn),
		nil,
		s.urlEntry,
	)

	sendBar := container.NewBorder(nil, nil, nil, s.sendFrameBtn, payloadEntry)

	framePane := container.NewBorder(paneHeader("Frames"), nil, nil, nil, s.frameTable.Build())
	payloadPane := container.NewBorder(paneHeader("Payload"), nil, nil, nil, s.payloadView.Build())

	frameSplit := container.NewVSplit(framePane, payloadPane)
	frameSplit.SetOffset(0.6)

	return container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(8, 0, 0, 0), toolbar),
		container.NewBorder(nil, nil, nil, nil, sendBar),
		nil, nil,
		frameSplit,
	)
}

func (r *repeaterTab) buildWSTabItem(tab store.RepeaterTab) *container.TabItem {
	s := &wsTabState{
		r:     r,
		tab:   tab,
		tabID: tab.ID,
	}

	s.urlEntry = widget.NewEntry()
	s.urlEntry.SetPlaceHolder("wss://host:port/path")
	if tab.RawRequest != "" {
		s.urlEntry.SetText(tab.RawRequest)
	}

	s.connectBtn = widget.NewButtonWithIcon("Connect", AppIcon("web"), nil)
	s.connectBtn.Importance = widget.HighImportance

	s.sendFrameBtn = widget.NewButtonWithIcon("Send", theme.MailSendIcon(), nil)
	s.sendFrameBtn.Disable()

	s.buildFrameTable()
	s.buildPayloadView()
	s.buildConnectHandler()

	payloadEntry := widget.NewMultiLineEntry()
	payloadEntry.SetPlaceHolder("Message to send...")
	payloadEntry.SetMinRowsVisible(3)

	s.buildSendHandler(payloadEntry)

	clearBtn := widget.NewButtonWithIcon("Clear", AppIcon("delete"), func() {
		s.frames = nil
		s.frameTable.Refresh()
		s.payloadView.SetText("")
	})

	headersBtn := widget.NewButtonWithIcon("Headers", theme.ListIcon(), func() {
		s.showHeadersDialog()
	})

	content := s.buildLayout(payloadEntry, clearBtn, headersBtn)
	tabItem := container.NewTabItem(tab.Name, content)
	r.tabIDs[tabItem] = s.tabID
	r.loadFns[tabItem] = func() {}

	return tabItem
}
