package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

var SettingsPathOverride string

type Settings struct {
	DarkTheme    bool   `json:"dark_theme"`
	ProxyHost    string `json:"proxy_host"`
	ProxyPort    int    `json:"proxy_port"`
	ProxyEnabled bool   `json:"proxy_enabled"`
}

func DefaultSettings() Settings {
	return Settings{
		DarkTheme:    true,
		ProxyHost:    "127.0.0.1",
		ProxyPort:    9090,
		ProxyEnabled: true,
	}
}

func settingsPath() (string, error) {
	if SettingsPathOverride != "" {
		return SettingsPathOverride, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "shiv", "settings.json"), nil
}

func LoadSettings() Settings {
	defaultSettings := DefaultSettings()

	path, err := settingsPath()
	if err != nil {
		return defaultSettings
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return defaultSettings
	}

	if err := json.Unmarshal(data, &defaultSettings); err != nil {
		return defaultSettings
	}

	return defaultSettings
}

func SaveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o644)
}
