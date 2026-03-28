package store

import (
	"fmt"
	"time"
)

// WebSocketDirection indicates which side sent the frame.
type WebSocketDirection int

const (
	WebSocketClient WebSocketDirection = 0 // browser → server
	WebSocketServer WebSocketDirection = 1 // server → browser
)

// WebSocketOpcode mirrors the gorilla/websocket message types.
type WebSocketOpcode int

const (
	WebSocketText   WebSocketOpcode = 1
	WebSocketBinary WebSocketOpcode = 2
	WebSocketPing   WebSocketOpcode = 9
	WebSocketPong   WebSocketOpcode = 10
	WebSocketClose  WebSocketOpcode = 8
)

// WebSocketFrame is a single frame captured during a WebSocket session.
type WebSocketFrame struct {
	ID           uint64
	ConnectionID uint64 // foreign key → websocket_connections.id
	Timestamp    time.Time
	Direction    WebSocketDirection
	Opcode       WebSocketOpcode
	Payload      []byte
}

// WebSocketConnection represents the initial HTTP upgrade that started a
// WebSocket session. It is tied to the history transaction of the upgrade
// request so the UI can link from history to the frame list.
type WebSocketConnection struct {
	ID         uint64
	HistoryID  uint64
	Host       string
	URL        string
	TLS        bool
	InScope    bool
	Timestamp  time.Time
	FrameCount int // populated on read, not stored
}

// LogWebSocketConnection inserts a new WebSocket connection record and returns
// its ID. Called once when the upgrade handshake completes.
func (s *Store) LogWebSocketConnection(conn WebSocketConnection) (uint64, error) {
	var id uint64
	err := s.write(func() error {
		res, err := s.db.Exec(`
			INSERT INTO websocket_connections (history_id, host, url, tls, in_scope, timestamp)
			VALUES (?, ?, ?, ?, ?, ?)`,
			conn.HistoryID,
			conn.Host,
			conn.URL,
			boolToInt(conn.TLS),
			boolToInt(conn.InScope),
			conn.Timestamp.UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("store: log websocket connection: %w", err)
		}
		lastID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("store: websocket connection last insert id: %w", err)
		}
		id = uint64(lastID)
		return nil
	})
	return id, err
}

// LogWebSocketFrame appends a single frame to an existing connection.
func (s *Store) LogWebSocketFrame(frame WebSocketFrame) error {
	return s.write(func() error {
		_, err := s.db.Exec(`
			INSERT INTO websocket_frames (connection_id, timestamp, direction, opcode, payload)
			VALUES (?, ?, ?, ?, ?)`,
			frame.ConnectionID,
			frame.Timestamp.UTC().Format(time.RFC3339),
			int(frame.Direction),
			int(frame.Opcode),
			frame.Payload,
		)
		if err != nil {
			return fmt.Errorf("store: log websocket frame: %w", err)
		}
		return nil
	})
}

// AllWebSocketConnections returns all WebSocket connections ordered by id
// descending, with frame counts.
func (s *Store) AllWebSocketConnections() ([]WebSocketConnection, error) {
	rows, err := s.db.Query(`
		SELECT c.id, c.history_id, c.host, c.url, c.tls, c.in_scope, c.timestamp,
		       COUNT(f.id) as frame_count
		FROM websocket_connections c
		LEFT JOIN websocket_frames f ON f.connection_id = c.id
		GROUP BY c.id
		ORDER BY c.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: query websocket connections: %w", err)
	}
	defer rows.Close()

	var conns []WebSocketConnection
	for rows.Next() {
		var c WebSocketConnection
		var ts string
		var tlsInt, scopeInt int
		if err := rows.Scan(&c.ID, &c.HistoryID, &c.Host, &c.URL, &tlsInt, &scopeInt, &ts, &c.FrameCount); err != nil {
			return nil, fmt.Errorf("store: scan websocket connection: %w", err)
		}
		c.Timestamp, _ = time.Parse(time.RFC3339, ts)
		c.TLS = tlsInt == 1
		c.InScope = scopeInt == 1
		conns = append(conns, c)
	}
	return conns, rows.Err()
}

// FramesForConnection returns all frames for a given connection ordered by id
// ascending (chronological).
func (s *Store) FramesForConnection(connectionID uint64) ([]WebSocketFrame, error) {
	rows, err := s.db.Query(`
		SELECT id, connection_id, timestamp, direction, opcode, payload
		FROM websocket_frames
		WHERE connection_id = ?
		ORDER BY id ASC`, connectionID)
	if err != nil {
		return nil, fmt.Errorf("store: query websocket frames: %w", err)
	}
	defer rows.Close()

	var frames []WebSocketFrame
	for rows.Next() {
		var f WebSocketFrame
		var ts string
		if err := rows.Scan(&f.ID, &f.ConnectionID, &ts, &f.Direction, &f.Opcode, &f.Payload); err != nil {
			return nil, fmt.Errorf("store: scan websocket frame: %w", err)
		}
		f.Timestamp, _ = time.Parse(time.RFC3339, ts)
		frames = append(frames, f)
	}
	return frames, rows.Err()
}
