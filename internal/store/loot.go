package store

import (
	"fmt"
	"time"
)

type LootEntry struct {
	ID        int64
	Title     string
	Severity  string
	Notes     string
	HistoryID *uint64
	CreatedAt time.Time
}

func (s *Store) AllLoot() ([]LootEntry, error) {
	rows, err := s.db.Query(`
		SELECT id, title, severity, notes, history_id, created_at
		FROM loot ORDER BY 
		CASE severity
			WHEN 'Critical' THEN 1
			WHEN 'High' THEN 2
			WHEN 'Medium' THEN 3
			WHEN 'Low' THEN 4
			WHEN 'Info' THEN 5
			ELSE 6
		END ASC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("store: query loot: %w", err)
	}
	defer rows.Close()

	var entries []LootEntry
	for rows.Next() {
		var e LootEntry
		var histID *int64
		var ts string
		if err := rows.Scan(&e.ID, &e.Title, &e.Severity, &e.Notes, &histID, &ts); err != nil {
			return nil, fmt.Errorf("store: scan loot: %w", err)
		}
		if histID != nil {
			id := uint64(*histID)
			e.HistoryID = &id
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *Store) AddLoot(e LootEntry) (int64, error) {
	var id int64
	err := s.write(func() error {
		var histID interface{}
		if e.HistoryID != nil {
			histID = *e.HistoryID
		}
		res, err := s.db.Exec(`
			INSERT INTO loot (title, severity, notes, history_id, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			e.Title, e.Severity, e.Notes, histID,
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			return fmt.Errorf("store: add loot: %w", err)
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

func (s *Store) DeleteLoot(id int64) error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM loot WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("store: delete loot: %w", err)
		}
		return nil
	})
}
