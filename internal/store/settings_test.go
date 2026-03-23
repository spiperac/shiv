package store_test

import (
	"os"
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSettings(t *testing.T) {
	settings := store.DefaultSettings()
	assert.Equal(t, "127.0.0.1", settings.ProxyHost)
	assert.Equal(t, 9090, settings.ProxyPort)
	assert.True(t, settings.ProxyEnabled)
	assert.True(t, settings.DarkTheme)
}

func TestSaveAndLoadSettings(t *testing.T) {
	tmp, err := os.CreateTemp("", "shiv-settings-*.json")
	require.NoError(t, err)
	tmp.Close()
	store.SettingsPathOverride = tmp.Name()
	t.Cleanup(func() {
		store.SettingsPathOverride = ""
		os.Remove(tmp.Name())
	})

	settings := store.Settings{
		DarkTheme:    false,
		ProxyHost:    "0.0.0.0",
		ProxyPort:    8080,
		ProxyEnabled: false,
	}
	require.NoError(t, store.SaveSettings(settings))

	loaded := store.LoadSettings()
	assert.Equal(t, "0.0.0.0", loaded.ProxyHost)
	assert.Equal(t, 8080, loaded.ProxyPort)
	assert.False(t, loaded.ProxyEnabled)
	assert.False(t, loaded.DarkTheme)
}

func TestLoadSettings_FallsBackToDefaults(t *testing.T) {
	tmp, err := os.CreateTemp("", "shiv-settings-*.json")
	require.NoError(t, err)
	tmp.Close()
	os.Remove(tmp.Name())
	store.SettingsPathOverride = tmp.Name()
	t.Cleanup(func() {
		store.SettingsPathOverride = ""
	})

	settings := store.LoadSettings()
	assert.Equal(t, store.DefaultSettings(), settings)
}

func TestSaveSettings_Upsert(t *testing.T) {
	tmp, err := os.CreateTemp("", "shiv-settings-*.json")
	require.NoError(t, err)
	tmp.Close()
	store.SettingsPathOverride = tmp.Name()
	t.Cleanup(func() {
		store.SettingsPathOverride = ""
		os.Remove(tmp.Name())
	})

	require.NoError(t, store.SaveSettings(store.Settings{ProxyHost: "127.0.0.1", ProxyPort: 9090, ProxyEnabled: true, DarkTheme: true}))
	require.NoError(t, store.SaveSettings(store.Settings{ProxyHost: "0.0.0.0", ProxyPort: 1234, ProxyEnabled: false, DarkTheme: false}))

	loaded := store.LoadSettings()
	assert.Equal(t, "0.0.0.0", loaded.ProxyHost)
	assert.Equal(t, 1234, loaded.ProxyPort)
	assert.False(t, loaded.ProxyEnabled)
	assert.False(t, loaded.DarkTheme)
}
