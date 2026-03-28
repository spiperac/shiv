package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	writeCh chan func() error
	done    chan struct{}

	Updates              chan Transaction
	Intercept            *InterceptGate
	WebSocketConnections chan WebSocketConnection
	WebSocketFrames      chan WebSocketFrame
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	projectStore := &Store{
		db:                   db,
		writeCh:              make(chan func() error, 256),
		done:                 make(chan struct{}),
		Updates:              make(chan Transaction, 256),
		Intercept:            NewInterceptGate(),
		WebSocketConnections: make(chan WebSocketConnection, 256),
		WebSocketFrames:      make(chan WebSocketFrame, 256),
	}
	if err := projectStore.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	go projectStore.writeLoop()
	return projectStore, nil
}

func (s *Store) Close() error {
	close(s.done)
	close(s.Updates)
	close(s.WebSocketConnections)
	close(s.WebSocketFrames)
	close(s.Intercept.queue)
	return s.db.Close()
}

func (s *Store) write(writeFunc func() error) error {
	errorChan := make(chan error, 1)
	select {
	case s.writeCh <- func() error {
		err := writeFunc()
		errorChan <- err
		return err
	}:
	case <-s.done:
		return fmt.Errorf("store: closed")
	}
	select {
	case err := <-errorChan:
		return err
	case <-s.done:
		return fmt.Errorf("store: closed")
	}
}

func (s *Store) writeLoop() {
	for {
		select {
		case writeFunc := <-s.writeCh:
			writeFunc()
		case <-s.done:
			for {
				select {
				case fn := <-s.writeCh:
					fn()
				default:
					return
				}
			}
		}
	}
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	// Add new columns to existing tables if they don't exist.
	// Errors are ignored — column may already exist.
	migrations := []string{
		`ALTER TABLE loot ADD COLUMN raw_request TEXT DEFAULT ''`,
		`ALTER TABLE loot ADD COLUMN raw_response TEXT DEFAULT ''`,
		`ALTER TABLE history ADD COLUMN proto TEXT NOT NULL DEFAULT 'HTTP/1.1'`,
		`ALTER TABLE repeater_tabs ADD COLUMN tab_type TEXT NOT NULL DEFAULT 'http'`,
	}
	for _, migration := range migrations {
		s.db.Exec(migration)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS history (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp    TEXT    NOT NULL,
	host         TEXT    NOT NULL,
	method       TEXT    NOT NULL,
	url          TEXT    NOT NULL,
	proto        TEXT    NOT NULL DEFAULT 'HTTP/1.1',
	req_headers  TEXT    NOT NULL,
	req_body     BLOB,
	status_code  INTEGER,
	resp_headers TEXT,
	resp_body    BLOB,
	duration_ms  INTEGER,
	tls          INTEGER DEFAULT 0,
	in_scope     INTEGER DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_history_host      ON history(host);
CREATE INDEX IF NOT EXISTS idx_history_timestamp ON history(timestamp);
CREATE INDEX IF NOT EXISTS idx_history_scope     ON history(in_scope);
CREATE INDEX IF NOT EXISTS idx_history_dedup ON history(method, host, url, status_code);
CREATE INDEX IF NOT EXISTS idx_history_host_id   ON history(host, id DESC);
CREATE INDEX IF NOT EXISTS idx_history_scope_id  ON history(in_scope, id DESC);

CREATE TABLE IF NOT EXISTS repeater_tabs (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT    NOT NULL DEFAULT 'Tab',
	host          TEXT    NOT NULL,
	port          INTEGER NOT NULL DEFAULT 80,
	tls           INTEGER DEFAULT 0,
	raw_request   TEXT    NOT NULL,
	last_response TEXT,
	position      INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS loot (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	title        TEXT    NOT NULL,
	severity     TEXT    NOT NULL,
	notes        TEXT,
	history_id   INTEGER REFERENCES history(id),
	raw_request  TEXT    DEFAULT '',
	raw_response TEXT    DEFAULT '',
	created_at   TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS scope (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	host TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS intruder_config (
	id               INTEGER PRIMARY KEY,
	delay_ms         INTEGER DEFAULT 0,
	stop_on_status   INTEGER DEFAULT 0,
	max_redirects    INTEGER DEFAULT 10,
	follow_redirects TEXT    DEFAULT 'never',
	timeout_ms       INTEGER DEFAULT 30000,
	raw_request      TEXT    DEFAULT '',
	payloads         TEXT    DEFAULT ''
);

CREATE TABLE IF NOT EXISTS websocket_connections (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	history_id INTEGER REFERENCES history(id),
	host       TEXT    NOT NULL,
	url        TEXT    NOT NULL,
	tls        INTEGER DEFAULT 0,
	in_scope   INTEGER DEFAULT 1,
	timestamp  TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ws_connections_host ON websocket_connections(host);
CREATE INDEX IF NOT EXISTS idx_ws_connections_scope ON websocket_connections(in_scope);

CREATE TABLE IF NOT EXISTS websocket_frames (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	connection_id INTEGER NOT NULL REFERENCES websocket_connections(id) ON DELETE CASCADE,
	timestamp     TEXT    NOT NULL,
	direction     INTEGER NOT NULL, -- 0 = client→server, 1 = server→client
	opcode        INTEGER NOT NULL,
	payload       BLOB
);

CREATE INDEX IF NOT EXISTS idx_ws_frames_connection ON websocket_frames(connection_id);
`
