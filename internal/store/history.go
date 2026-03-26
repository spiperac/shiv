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

func (s *Store) GetTransaction(id uint64) (*Transaction, error) {
	var transaction Transaction
	var timestampStr, reqHeaders, respHeaders string
	var tlsFlag, scopeFlag int
	err := s.db.QueryRow(`
		SELECT id, timestamp, host, method, url,
		       req_headers, req_body, status_code, resp_headers,
		       resp_body, duration_ms, tls, in_scope
		FROM history WHERE id = ?`, id,
	).Scan(
		&transaction.ID, &timestampStr, &transaction.Host, &transaction.Method, &transaction.URL,
		&reqHeaders, &transaction.ReqBody, &transaction.StatusCode, &respHeaders,
		&transaction.RespBody, &transaction.DurationMs, &tlsFlag, &scopeFlag,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get transaction %d: %w", id, err)
	}
	transaction.Timestamp, _ = time.Parse(time.RFC3339, timestampStr)
	transaction.TLS = tlsFlag == 1
	transaction.InScope = scopeFlag == 1
	if err := json.Unmarshal([]byte(reqHeaders), &transaction.ReqHeaders); err != nil {
		transaction.ReqHeaders = http.Header{}
	}
	if err := json.Unmarshal([]byte(respHeaders), &transaction.RespHeaders); err != nil {
		transaction.RespHeaders = http.Header{}
	}
	return &transaction, nil
}

// Log writes a completed transaction to the history table and pushes it to Updates.
func (s *Store) Log(t Transaction) error {
	// Dedup check outside write lock.
	var existingID uint64
	err := s.db.QueryRow(`
		SELECT id FROM history
		WHERE method = ? AND host = ? AND url = ? AND status_code = ?
		LIMIT 1`,
		t.Method, t.Host, t.URL, t.StatusCode,
	).Scan(&existingID)

	if err == nil {
		// Row exists — update ALL fields so the new request body, headers,
		// response body, and duration are persisted. Previously only timestamp
		// was written here, which caused stale request bodies to be shown.
		return s.write(func() error {
			reqH, err := json.Marshal(t.ReqHeaders)
			if err != nil {
				return fmt.Errorf("store: marshal req headers: %w", err)
			}
			respH, err := json.Marshal(t.RespHeaders)
			if err != nil {
				return fmt.Errorf("store: marshal resp headers: %w", err)
			}
			_, err = s.db.Exec(`
				UPDATE history SET
					timestamp    = ?,
					req_headers  = ?,
					req_body     = ?,
					resp_headers = ?,
					resp_body    = ?,
					duration_ms  = ?
				WHERE id = ?`,
				t.Timestamp.UTC().Format(time.RFC3339),
				string(reqH), t.ReqBody,
				string(respH), t.RespBody,
				t.DurationMs,
				existingID,
			)
			if err != nil {
				return fmt.Errorf("store: update transaction: %w", err)
			}
			t.ID = existingID
			select {
			case s.Updates <- t:
			default:
			}
			return nil
		})
	}

	return s.write(func() error {
		reqH, err := json.Marshal(t.ReqHeaders)
		if err != nil {
			return fmt.Errorf("store: marshal req headers: %w", err)
		}
		respH, err := json.Marshal(t.RespHeaders)
		if err != nil {
			return fmt.Errorf("store: marshal resp headers: %w", err)
		}

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
		       req_headers, '' as req_body, status_code, resp_headers,
		       '' as resp_body, duration_ms, tls, in_scope
		FROM history
		ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("store: query history: %w", err)
	}
	defer rows.Close()
	return scanTransactions(rows)
}

func scanTransactions(rows interface {
	Next() bool
	Scan(...any) error
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
