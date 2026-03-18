package store

import (
	"fmt"
	"strings"
)

// ScopeEntry represents a single scope target.
type ScopeEntry struct {
	ID   int64
	Host string
}

// AllScopeEntries returns all scope entries.
func (s *Store) AllScopeEntries() ([]ScopeEntry, error) {
	rows, err := s.db.Query(`SELECT id, host FROM scope ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: query scope: %w", err)
	}
	defer rows.Close()

	var entries []ScopeEntry
	for rows.Next() {
		var e ScopeEntry
		if err := rows.Scan(&e.ID, &e.Host); err != nil {
			return nil, fmt.Errorf("store: scan scope: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// AddScopeEntry adds a new host to scope.
func (s *Store) AddScopeEntry(host string) error {
	return s.write(func() error {
		_, err := s.db.Exec(`INSERT INTO scope (host) VALUES (?)`, host)
		if err != nil {
			return fmt.Errorf("store: add scope entry: %w", err)
		}
		return nil
	})
}

// DeleteScopeEntry removes a scope entry by ID.
func (s *Store) DeleteScopeEntry(id int64) error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM scope WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("store: delete scope entry: %w", err)
		}
		return nil
	})
}

// InScope returns true if the given host matches any scope entry.
// example.com matches example.com and all subdomains.
func (s *Store) InScope(host string) bool {
	entries, err := s.AllScopeEntries()
	if err != nil {
		return false
	}
	if len(entries) == 0 {
		return true // no scope defined, everything is in scope
	}
	// strip port if present
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	for _, e := range entries {
		if host == e.Host || strings.HasSuffix(host, "."+e.Host) {
			return true
		}
	}
	return false
}
