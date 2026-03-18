package store

import (
	"fmt"
	"strconv"
)

type ProxySettings struct {
	Host    string
	Port    int
	Enabled bool
}

func DefaultProxySettings() ProxySettings {
	return ProxySettings{
		Host:    "127.0.0.1",
		Port:    9090,
		Enabled: true,
	}
}
func (s *Store) GetMeta(key string) (string, error) {
	var val string
	err := s.write(func() error {
		return s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&val)
	})
	return val, err
}

func (s *Store) SetMeta(key, value string) error {
	return s.write(func() error {
		_, err := s.db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		if err != nil {
			return fmt.Errorf("store: set meta %s: %w", key, err)
		}
		return nil
	})
}

func (s *Store) LoadProxySettings() ProxySettings {
	def := DefaultProxySettings()

	if host, err := s.GetMeta("proxy.host"); err == nil {
		def.Host = host
	}
	if portStr, err := s.GetMeta("proxy.port"); err == nil {
		if port, err := strconv.Atoi(portStr); err == nil {
			def.Port = port
		}
	}
	if enabledStr, err := s.GetMeta("proxy.enabled"); err == nil {
		def.Enabled = enabledStr == "true"
	}

	return def
}

func (s *Store) SaveProxySettings(ps ProxySettings) error {
	if err := s.SetMeta("proxy.host", ps.Host); err != nil {
		return err
	}
	if err := s.SetMeta("proxy.port", strconv.Itoa(ps.Port)); err != nil {
		return err
	}
	enabled := "false"
	if ps.Enabled {
		enabled = "true"
	}
	return s.SetMeta("proxy.enabled", enabled)
}
