package store

import "fmt"

type IntruderConfig struct {
	DelayMs         int
	StopOnStatus    int
	MaxRedirects    int
	FollowRedirects string // "never", "always", "in-scope"
	TimeoutMs       int
	RawRequest      string
	Payloads        string
}

func DefaultIntruderConfig() IntruderConfig {
	return IntruderConfig{
		DelayMs:         0,
		StopOnStatus:    0,
		MaxRedirects:    10,
		FollowRedirects: "never",
		TimeoutMs:       30000,
		RawRequest:      "",
		Payloads:        "",
	}
}

func (s *Store) LoadIntruderConfig() IntruderConfig {
	config := DefaultIntruderConfig()
	err := s.db.QueryRow(`
		SELECT delay_ms, stop_on_status, max_redirects, follow_redirects, timeout_ms, raw_request, payloads
		FROM intruder_config WHERE id = 1`).Scan(
		&config.DelayMs,
		&config.StopOnStatus,
		&config.MaxRedirects,
		&config.FollowRedirects,
		&config.TimeoutMs,
		&config.RawRequest,
		&config.Payloads,
	)
	if err != nil {
		return config
	}
	return config
}

func (s *Store) SaveIntruderConfig(config IntruderConfig) error {
	return s.write(func() error {
		_, err := s.db.Exec(`
			INSERT INTO intruder_config (id, delay_ms, stop_on_status, max_redirects, follow_redirects, timeout_ms, raw_request, payloads)
			VALUES (1, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				delay_ms = excluded.delay_ms,
				stop_on_status = excluded.stop_on_status,
				max_redirects = excluded.max_redirects,
				follow_redirects = excluded.follow_redirects,
				timeout_ms = excluded.timeout_ms,
				raw_request = excluded.raw_request,
				payloads = excluded.payloads`,
			config.DelayMs,
			config.StopOnStatus,
			config.MaxRedirects,
			config.FollowRedirects,
			config.TimeoutMs,
			config.RawRequest,
			config.Payloads,
		)
		if err != nil {
			return fmt.Errorf("store: save intruder config: %w", err)
		}
		return nil
	})
}
