package store_test

import (
	"testing"

	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Meta ──────────────────────────────────────────────────────────────────────

func TestSetMeta_AndGetMeta(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.SetMeta("some.key", "some-value"))

	val, err := st.GetMeta("some.key")
	require.NoError(t, err)
	assert.Equal(t, "some-value", val)
}

func TestSetMeta_Upsert(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.SetMeta("key", "first"))
	require.NoError(t, st.SetMeta("key", "second"))

	val, err := st.GetMeta("key")
	require.NoError(t, err)
	assert.Equal(t, "second", val)
}

func TestGetMeta_NonExistentKey(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetMeta("does.not.exist")
	assert.Error(t, err)
}

// ── ProxySettings ─────────────────────────────────────────────────────────────

func TestDefaultProxySettings(t *testing.T) {
	ps := store.DefaultProxySettings()
	assert.Equal(t, "127.0.0.1", ps.Host)
	assert.Equal(t, 9090, ps.Port)
	assert.True(t, ps.Enabled)
}

func TestSaveAndLoadProxySettings(t *testing.T) {
	st := newTestStore(t)

	ps := store.ProxySettings{
		Host:    "0.0.0.0",
		Port:    8080,
		Enabled: true,
	}
	require.NoError(t, st.SaveProxySettings(ps))

	loaded := st.LoadProxySettings()
	assert.Equal(t, "0.0.0.0", loaded.Host)
	assert.Equal(t, 8080, loaded.Port)
	assert.True(t, loaded.Enabled)
}

func TestSaveProxySettings_DisabledFlag(t *testing.T) {
	st := newTestStore(t)

	ps := store.ProxySettings{Host: "127.0.0.1", Port: 9090, Enabled: false}
	require.NoError(t, st.SaveProxySettings(ps))

	loaded := st.LoadProxySettings()
	assert.False(t, loaded.Enabled)
}

func TestSaveProxySettings_Upsert(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.SaveProxySettings(store.ProxySettings{Host: "127.0.0.1", Port: 9090, Enabled: true}))
	require.NoError(t, st.SaveProxySettings(store.ProxySettings{Host: "0.0.0.0", Port: 1234, Enabled: false}))

	loaded := st.LoadProxySettings()
	assert.Equal(t, "0.0.0.0", loaded.Host)
	assert.Equal(t, 1234, loaded.Port)
	assert.False(t, loaded.Enabled)
}

func TestLoadProxySettings_FallsBackToDefaults(t *testing.T) {
	st := newTestStore(t)

	// nothing saved — should return defaults
	ps := st.LoadProxySettings()
	assert.Equal(t, store.DefaultProxySettings(), ps)
}
