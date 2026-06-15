package store

import "fmt"

// Annotation holds optional comment and highlight colour for a history row.
type Annotation struct {
	HistoryID uint64
	Comment   string
	Color     string // empty means no highlight; one of: "red", "orange", "yellow", "green", "blue", "purple"
}

// SetAnnotation creates or replaces the annotation for the given history row.
func (s *Store) SetAnnotation(a Annotation) error {
	return s.write(func() error {
		_, err := s.db.Exec(`
			INSERT INTO annotations (history_id, comment, color)
			VALUES (?, ?, ?)
			ON CONFLICT(history_id) DO UPDATE SET
				comment = excluded.comment,
				color   = excluded.color`,
			a.HistoryID, a.Comment, a.Color,
		)
		if err != nil {
			return fmt.Errorf("store: set annotation %d: %w", a.HistoryID, err)
		}
		return nil
	})
}

// DeleteAnnotation removes the annotation for the given history row.
func (s *Store) DeleteAnnotation(historyID uint64) error {
	return s.write(func() error {
		_, err := s.db.Exec(`DELETE FROM annotations WHERE history_id = ?`, historyID)
		if err != nil {
			return fmt.Errorf("store: delete annotation %d: %w", historyID, err)
		}
		return nil
	})
}

// AllAnnotations returns every annotation row. Called once at startup to
// populate the in-memory annotation map.
func (s *Store) AllAnnotations() ([]Annotation, error) {
	rows, err := s.db.Query(`SELECT history_id, comment, color FROM annotations`)
	if err != nil {
		return nil, fmt.Errorf("store: all annotations: %w", err)
	}
	defer rows.Close()
	var out []Annotation
	for rows.Next() {
		var a Annotation
		if err := rows.Scan(&a.HistoryID, &a.Comment, &a.Color); err != nil {
			return nil, fmt.Errorf("store: scan annotation: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
