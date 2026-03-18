package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Transaction is a matched HTTP request/response pair.
type Transaction struct {
	ID          uint64
	Timestamp   time.Time
	Host        string
	Method      string
	URL         string
	ReqHeaders  http.Header
	ReqBody     []byte
	StatusCode  int
	RespHeaders http.Header
	RespBody    []byte
	DurationMs  int64
	TLS         bool
	InScope     bool
}

// Log writes a completed transaction to the history table and pushes it to Updates.
func (s *Store) Log(t Transaction) error {
	return s.write(func() error {
		reqH, err := json.Marshal(t.ReqHeaders)
		if err != nil {
			return fmt.Errorf("store: marshal req headers: %w", err)
		}
		respH, err := json.Marshal(t.RespHeaders)
		if err != nil {
			return fmt.Errorf("store: marshal resp headers: %w", err)
		}

		// Check if identical request already exists.
		var existingID uint64
		err = s.db.QueryRow(`
			SELECT id FROM history
			WHERE method = ? AND host = ? AND url = ? AND status_code = ?
			LIMIT 1`,
			t.Method, t.Host, t.URL, t.StatusCode,
		).Scan(&existingID)

		if err == nil {
			// Already exists — update timestamp only and push to UI to move it to top.
			_, err = s.db.Exec(`
				UPDATE history SET timestamp = ? WHERE id = ?`,
				t.Timestamp.UTC().Format(time.RFC3339), existingID,
			)
			if err != nil {
				return fmt.Errorf("store: update timestamp: %w", err)
			}
			t.ID = existingID
			select {
			case s.Updates <- t:
			default:
			}
			return nil
		}

		// New request — insert.
		res, err := s.db.Exec(`
			INSERT INTO history
				(timestamp, host, method, url, req_headers, req_body,
				 status_code, resp_headers, resp_body, duration_ms, tls, in_scope)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.Timestamp.UTC().Format(time.RFC3339),
			t.Host, t.Method, t.URL,
			string(reqH), t.ReqBody,
			t.StatusCode, string(respH), t.RespBody,
			t.DurationMs,
			boolToInt(t.TLS),
			boolToInt(t.InScope),
		)
		if err != nil {
			return fmt.Errorf("store: log transaction: %w", err)
		}
		id, _ := res.LastInsertId()
		t.ID = uint64(id)

		select {
		case s.Updates <- t:
		default:
		}
		return nil
	})
}

// AllTransactions returns all transactions ordered by id descending.
func (s *Store) AllTransactions() ([]Transaction, error) {
	rows, err := s.db.Query(`
		SELECT id, timestamp, host, method, url,
		       req_headers, req_body, status_code, resp_headers,
		       resp_body, duration_ms, tls, in_scope
		FROM history
		ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: query history: %w", err)
	}
	defer rows.Close()
	return scanTransactions(rows)
}

func scanTransactions(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]Transaction, error) {
	var txs []Transaction
	for rows.Next() {
		var tx Transaction
		var ts, reqH, respH string
		var tlsInt, scopeInt int
		if err := rows.Scan(
			&tx.ID, &ts, &tx.Host, &tx.Method, &tx.URL,
			&reqH, &tx.ReqBody, &tx.StatusCode, &respH,
			&tx.RespBody, &tx.DurationMs, &tlsInt, &scopeInt,
		); err != nil {
			return nil, fmt.Errorf("store: scan transaction: %w", err)
		}
		tx.Timestamp, _ = time.Parse(time.RFC3339, ts)
		tx.TLS = tlsInt == 1
		tx.InScope = scopeInt == 1
		if err := json.Unmarshal([]byte(reqH), &tx.ReqHeaders); err != nil {
			tx.ReqHeaders = http.Header{}
		}
		if err := json.Unmarshal([]byte(respH), &tx.RespHeaders); err != nil {
			tx.RespHeaders = http.Header{}
		}
		txs = append(txs, tx)
	}
	return txs, rows.Err()
}

// ClearHistory deletes all rows from the history table.
func (s *Store) ClearHistory() error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM history`)
		if err != nil {
			return fmt.Errorf("store: clear history: %w", err)
		}
		return nil
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
