package store

import (
	"fmt"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
)

const maxPluginLogLines = 500

// PluginEntry represents a loaded plugin and its persistent enabled state.
type PluginEntry struct {
	Name    string
	Path    string
	Enabled bool
}

// RegisterPlugin records a plugin in the database. If the plugin is new,
// enabled defaults to true. If it already exists, only the path is updated —
// the existing enabled state is preserved. Notifies the UI via PluginEntries.
func (s *Store) RegisterPlugin(name, path string) error {
	if err := s.write(func() error {
		_, err := s.db.Exec(`
			INSERT INTO plugins (name, path, enabled) VALUES (?, ?, 1)
			ON CONFLICT(name) DO UPDATE SET path = excluded.path`,
			name, path,
		)
		if err != nil {
			return fmt.Errorf("store: register plugin %s: %w", name, err)
		}
		return nil
	}); err != nil {
		return err
	}

	select {
	case s.PluginEntries <- struct{}{}:
	default:
	}
	return nil
}

// Scans for plugins on the disk, removes them from DB if they are deleted from disk.
func (s *Store) ScanPlugins(active []string) error {
	set := make(map[string]struct{}, len(active))
	for _, name := range active {
		set[name] = struct{}{}
	}
	all, err := s.AllPlugins()
	if err != nil {
		return err
	}
	for _, e := range all {
		if _, ok := set[e.Name]; ok {
			continue
		}
		if err := s.write(func() error {
			_, err := s.db.Exec(`DELETE FROM plugins WHERE name = ?`, e.Name)
			return err
		}); err != nil {
			return fmt.Errorf("store: scan plugins remove %s: %w", e.Name, err)
		}
	}
	select {
	case s.PluginEntries <- struct{}{}:
	default:
	}
	return nil
}

// PluginEnabled returns the persisted enabled state for a plugin.
// Returns true if the plugin record is not found (safe default for new plugins).
func (s *Store) PluginEnabled(name string) (bool, error) {
	var enabled int
	err := s.db.QueryRow(`SELECT enabled FROM plugins WHERE name = ?`, name).Scan(&enabled)
	if err != nil {
		return true, err
	}
	return enabled == 1, nil
}

// AllPlugins returns all known plugins ordered by name.
func (s *Store) AllPlugins() ([]PluginEntry, error) {
	rows, err := s.db.Query(`SELECT name, path, enabled FROM plugins ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: query plugins: %w", err)
	}
	defer rows.Close()

	var entries []PluginEntry
	for rows.Next() {
		var e PluginEntry
		var enabledInt int
		if err := rows.Scan(&e.Name, &e.Path, &enabledInt); err != nil {
			return nil, fmt.Errorf("store: scan plugin: %w", err)
		}
		e.Enabled = enabledInt == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PluginLogs returns the in-memory log lines for a plugin, oldest first.
// Returns a copy to avoid races with the writer.
func (s *Store) PluginLogs(name string) []string {
	s.pluginLogsMu.RLock()
	defer s.pluginLogsMu.RUnlock()
	lines := s.pluginLogs[name]
	if len(lines) == 0 {
		return nil
	}
	result := make([]string, len(lines))
	copy(result, lines)
	return result
}

// ObservePluginLog implements events.PluginLogObserver.
// Appends the log line to the in-memory ring buffer and notifies the UI.
func (s *Store) ObservePluginLog(e events.PluginLogEvent) {
	s.pluginLogsMu.Lock()
	lines := s.pluginLogs[e.Name]
	lines = append(lines, e.Message)
	if len(lines) > maxPluginLogLines {
		lines = lines[len(lines)-maxPluginLogLines:]
	}
	s.pluginLogs[e.Name] = lines
	s.pluginLogsMu.Unlock()

	select {
	case s.PluginEntries <- struct{}{}:
	default:
	}
}

// ObservePluginEnabled implements events.PluginEnabledObserver.
// Persists the new enabled state to the database and notifies the UI.
func (s *Store) ObservePluginEnabled(e events.SetPluginEnabledEvent) {
	if err := s.write(func() error {
		_, err := s.db.Exec(`UPDATE plugins SET enabled = ? WHERE name = ?`,
			boolToInt(e.Enabled), e.Name,
		)
		if err != nil {
			return fmt.Errorf("store: set plugin enabled %s: %w", e.Name, err)
		}
		return nil
	}); err != nil {
		logger.Error("store: observe plugin enabled: %v", err)
		return
	}

	select {
	case s.PluginEntries <- struct{}{}:
	default:
	}
}
