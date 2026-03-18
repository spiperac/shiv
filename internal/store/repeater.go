package store

import (
	"database/sql"
	"fmt"
)

// RepeaterTab represents a saved repeater tab.
type RepeaterTab struct {
	ID           int64
	Name         string
	Host         string
	Port         int
	TLS          bool
	RawRequest   string
	LastResponse string
	Position     int
}

// AllRepeaterTabs returns all saved repeater tabs ordered by position.
func (s *Store) AllRepeaterTabs() ([]RepeaterTab, error) {
	rows, err := s.db.Query(`
		SELECT id, name, host, port, tls, raw_request, COALESCE(last_response, ''), position
		FROM repeater_tabs
		ORDER BY position ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: query repeater tabs: %w", err)
	}
	defer rows.Close()

	var tabs []RepeaterTab
	for rows.Next() {
		var t RepeaterTab
		var tlsInt int
		if err := rows.Scan(&t.ID, &t.Name, &t.Host, &t.Port, &tlsInt, &t.RawRequest, &t.LastResponse, &t.Position); err != nil {
			return nil, fmt.Errorf("store: scan repeater tab: %w", err)
		}
		t.TLS = tlsInt == 1
		tabs = append(tabs, t)
	}
	return tabs, rows.Err()
}

// SaveRepeaterTab inserts a new repeater tab and returns its ID.
func (s *Store) SaveRepeaterTab(t RepeaterTab) (int64, error) {
	var id int64
	err := s.write(func() error {
		res, err := s.db.Exec(`
			INSERT INTO repeater_tabs (name, host, port, tls, raw_request, last_response, position)
			VALUES (?, ?, ?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM repeater_tabs))`,
			t.Name, t.Host, t.Port, boolToInt(t.TLS), t.RawRequest, t.LastResponse,
		)
		if err != nil {
			return fmt.Errorf("store: save repeater tab: %w", err)
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// UpdateRepeaterTab updates the request and response for an existing tab.
func (s *Store) UpdateRepeaterTab(id int64, rawRequest, lastResponse string) error {
	return s.write(func() error {
		_, err := s.db.Exec(`
			UPDATE repeater_tabs SET raw_request = ?, last_response = ? WHERE id = ?`,
			rawRequest, lastResponse, id,
		)
		if err != nil {
			return fmt.Errorf("store: update repeater tab: %w", err)
		}
		return nil
	})
}

// RenameRepeaterTab updates the name of a tab.
func (s *Store) RenameRepeaterTab(id int64, name string) error {
	return s.write(func() error {
		_, err := s.db.Exec(`UPDATE repeater_tabs SET name = ? WHERE id = ?`, name, id)
		if err != nil {
			return fmt.Errorf("store: rename repeater tab: %w", err)
		}
		return nil
	})
}

// DeleteRepeaterTab removes a repeater tab by ID.
func (s *Store) DeleteRepeaterTab(id int64) error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM repeater_tabs WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("store: delete repeater tab: %w", err)
		}
		return nil
	})
}

// repeaterTabExists checks if a tab with the given host+port+request already exists.
func (s *Store) repeaterTabByID(id int64) (*RepeaterTab, error) {
	var t RepeaterTab
	var tlsInt int
	err := s.db.QueryRow(`
		SELECT id, name, host, port, tls, raw_request, COALESCE(last_response, ''), position
		FROM repeater_tabs WHERE id = ?`, id,
	).Scan(&t.ID, &t.Name, &t.Host, &t.Port, &tlsInt, &t.RawRequest, &t.LastResponse, &t.Position)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.TLS = tlsInt == 1
	return &t, nil
}
