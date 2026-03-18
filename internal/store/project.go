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

	Updates   chan Transaction
	Intercept *InterceptGate
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{
		db:        db,
		writeCh:   make(chan func() error, 256),
		done:      make(chan struct{}),
		Updates:   make(chan Transaction, 256),
		Intercept: NewInterceptGate(),
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	go s.writeLoop()
	return s, nil
}

func (s *Store) Close() error {
	close(s.done)
	close(s.Updates)
	close(s.Intercept.queue)
	return s.db.Close()
}

func (s *Store) write(fn func() error) error {
	errCh := make(chan error, 1)
	select {
	case s.writeCh <- func() error {
		err := fn()
		errCh <- err
		return err
	}:
	case <-s.done:
		return fmt.Errorf("store: closed")
	}
	select {
	case err := <-errCh:
		return err
	case <-s.done:
		return fmt.Errorf("store: closed")
	}
}

func (s *Store) writeLoop() {
	for {
		select {
		case fn := <-s.writeCh:
			fn()
		case <-s.done:
			// Drain remaining writes before exit.
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
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS targets (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	host     TEXT    NOT NULL,
	port     INTEGER,
	protocol TEXT    DEFAULT 'any',
	in_scope INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS history (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp    TEXT    NOT NULL,
	host         TEXT    NOT NULL,
	method       TEXT    NOT NULL,
	url          TEXT    NOT NULL,
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
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	title      TEXT    NOT NULL,
	severity   TEXT    NOT NULL,
	notes      TEXT,
	history_id INTEGER REFERENCES history(id),
	created_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS scope (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	host TEXT NOT NULL UNIQUE
);

`
