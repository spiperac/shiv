package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Transaction is a matched HTTP request/response pair.
// Host is always a pure hostname — never host:port.
// Port holds the destination port as an integer.
type Transaction struct {
	ID          uint64
	Timestamp   time.Time
	Host        string
	Port        int
	Method      string
	URL         string
	Proto       string // "HTTP/2" or "HTTP/1.1"
	ReqHeaders  http.Header
	ReqBody     []byte
	StatusCode  int
	RespHeaders http.Header
	RespBody    []byte
	DurationMs  int64
	TLS         bool
	InScope     bool
}

// TransactionFilter holds filter criteria for paginated history queries.
// Zero values mean "no filter" for that field.
type TransactionFilter struct {
	Search       string // matched against host + url + method + status (case-insensitive)
	Host         string // exact host match (from site map selection)
	PathPrefix   string // URL path prefix filter (from site map selection)
	ShowOutScope bool   // if false, only in_scope=1 rows are returned
}

// PageSize is the number of rows returned per page.
const PageSize = 100

// SiteMapEntry is a minimal host+url pair used to build the site map at
// startup. Only these two fields are needed — no headers, no bodies.
type SiteMapEntry struct {
	Host string
	URL  string
}

// SiteMapEntries returns the host and url for every row in history.
// Used once at startup to populate the site map with all historical
// paths before the first page of transactions is displayed.
func (s *Store) SiteMapEntries() ([]SiteMapEntry, error) {
	rows, err := s.db.Query(`SELECT host, url FROM history ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: site map entries: %w", err)
	}
	defer rows.Close()
	var entries []SiteMapEntry
	for rows.Next() {
		var e SiteMapEntry
		if err := rows.Scan(&e.Host, &e.URL); err != nil {
			return nil, fmt.Errorf("store: scan site map entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) GetTransaction(id uint64) (*Transaction, error) {
	var transaction Transaction
	var timestampStr, reqHeaders, respHeaders string
	var tlsFlag, scopeFlag int
	err := s.db.QueryRow(`
		SELECT id, timestamp, host, port, method, url, proto,
		       req_headers, req_body, status_code, resp_headers,
		       resp_body, duration_ms, tls, in_scope
		FROM history WHERE id = ?`, id,
	).Scan(
		&transaction.ID, &timestampStr, &transaction.Host, &transaction.Port,
		&transaction.Method, &transaction.URL, &transaction.Proto,
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

// Log writes every transaction as a unique row — no deduplication.
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
		proto := t.Proto
		if proto == "" {
			proto = "HTTP/1.1"
		}
		res, err := s.db.Exec(`
			INSERT INTO history
				(timestamp, host, port, method, url, proto, req_headers, req_body,
				 status_code, resp_headers, resp_body, duration_ms, tls, in_scope)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.Timestamp.UTC().Format(time.RFC3339),
			t.Host, t.Port, t.Method, t.URL, proto,
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
		t.Proto = proto
		select {
		case s.Updates <- t:
		default:
		}
		return nil
	})
}

// TransactionsPage returns up to PageSize transactions with id < beforeID,
// ordered by id descending (newest first), applying filter criteria.
// Pass beforeID=0 to start from the newest row.
// Bodies are omitted; use GetTransaction for the full row.
func (s *Store) TransactionsPage(beforeID uint64, filter TransactionFilter) ([]Transaction, error) {
	var conditions []string
	var args []any

	if beforeID > 0 {
		conditions = append(conditions, "id < ?")
		args = append(args, beforeID)
	}

	if !filter.ShowOutScope {
		conditions = append(conditions, "in_scope = 1")
	}

	if filter.Host != "" {
		conditions = append(conditions, "host = ?")
		args = append(args, filter.Host)
	}

	if filter.PathPrefix != "" {
		conditions = append(conditions, "(url LIKE ? OR url LIKE ? OR url LIKE ?)")
		prefix := filter.PathPrefix
		args = append(args,
			"%"+prefix,
			"%"+prefix+"?%",
			"%"+prefix+"/%",
		)
	}

	if filter.Search != "" {
		terms := strings.Fields(strings.ToLower(filter.Search))
		for _, term := range terms {
			conditions = append(conditions, "(LOWER(host) LIKE ? OR LOWER(url) LIKE ? OR LOWER(method) LIKE ? OR CAST(status_code AS TEXT) LIKE ?)")
			like := "%" + term + "%"
			args = append(args, like, like, like, like)
		}
	}

	query := `
		SELECT id, timestamp, host, port, method, url, proto,
		       req_headers, '' as req_body, status_code, resp_headers,
		       '' as resp_body, duration_ms, tls, in_scope
		FROM history`

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", PageSize)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query history page: %w", err)
	}
	defer rows.Close()
	return scanTransactions(rows)
}

// TransactionsSince returns transactions with id > afterID, newest first,
// capped at 200. Bodies are omitted. Used by pollMissed.
func (s *Store) TransactionsSince(afterID uint64) ([]Transaction, error) {
	rows, err := s.db.Query(`
		SELECT id, timestamp, host, port, method, url, proto,
		       req_headers, '' as req_body, status_code, resp_headers,
		       '' as resp_body, duration_ms, tls, in_scope
		FROM history
		WHERE id > ?
		ORDER BY id DESC LIMIT 200`, afterID)
	if err != nil {
		return nil, fmt.Errorf("store: query history since %d: %w", afterID, err)
	}
	defer rows.Close()
	return scanTransactions(rows)
}

// AllTransactions returns all transactions ordered by id descending, capped at
// 500 rows. Used primarily in tests; production UI uses TransactionsPage.
func (s *Store) AllTransactions() ([]Transaction, error) {
	rows, err := s.db.Query(`
		SELECT id, timestamp, host, port, method, url, proto,
		       req_headers, req_body, status_code, resp_headers,
		       resp_body, duration_ms, tls, in_scope
		FROM history
		ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("store: query all transactions: %w", err)
	}
	defer rows.Close()
	return scanTransactions(rows)
}

// ClearHistory deletes all rows from the history table.
func (s *Store) ClearHistory() error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM history`)
		if err != nil {
			return fmt.Errorf("store: clear history: %w", err)
		}
		_, err = s.db.Exec(`VACUUM`)
		if err != nil {
			return fmt.Errorf("store: VACUUM history: %w", err)
		}

		return nil
	})
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
			&tx.ID, &ts, &tx.Host, &tx.Port, &tx.Method, &tx.URL, &tx.Proto,
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// DeleteTransactionsByHost deletes all history rows for the given host.
func (s *Store) DeleteTransactionsByHost(host string) error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM history WHERE host = ?`, host)
		if err != nil {
			return fmt.Errorf("store: delete transactions for host %s: %w", host, err)
		}
		return nil
	})
}
