package store_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── RegisterPlugin ────────────────────────────────────────────────────────────

func TestRegisterPlugin_NewPlugin_DefaultsToEnabled(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "test.lua", entries[0].Name)
	assert.Equal(t, "/plugins/test.lua", entries[0].Path)
	assert.True(t, entries[0].Enabled)
}

func TestRegisterPlugin_ExistingPlugin_PreservesEnabledState(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	// Re-register with a new path — enabled state must be preserved.
	require.NoError(t, st.RegisterPlugin("test.lua", "/new/path/test.lua"))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "/new/path/test.lua", entries[0].Path)
	assert.False(t, entries[0].Enabled, "enabled state must survive re-registration")
}

func TestRegisterPlugin_NotifiesPluginEntries(t *testing.T) {
	st := newTestStore(t)

	// Drain any startup notifications.
	for len(st.PluginEntries) > 0 {
		<-st.PluginEntries
	}

	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))

	select {
	case <-st.PluginEntries:
	default:
		t.Fatal("PluginEntries channel was not notified after RegisterPlugin")
	}
}

func TestRegisterPlugin_MultiplePlugins(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.RegisterPlugin("a.lua", "/plugins/a.lua"))
	require.NoError(t, st.RegisterPlugin("b.lua", "/plugins/b.lua"))
	require.NoError(t, st.RegisterPlugin("c.lua", "/plugins/c.lua"))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}

// ── PluginEnabled ─────────────────────────────────────────────────────────────

func TestPluginEnabled_UnknownPlugin_ReturnsTrue(t *testing.T) {
	st := newTestStore(t)

	enabled, err := st.PluginEnabled("nonexistent.lua")
	// Error is expected (no row) but the safe default is true.
	assert.True(t, enabled)
	_ = err
}

func TestPluginEnabled_NewPlugin_ReturnsTrue(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))

	enabled, err := st.PluginEnabled("test.lua")
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestPluginEnabled_AfterDisable_ReturnsFalse(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	enabled, err := st.PluginEnabled("test.lua")
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestPluginEnabled_AfterReEnable_ReturnsTrue(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/plugins/test.lua"))
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: true})

	enabled, err := st.PluginEnabled("test.lua")
	require.NoError(t, err)
	assert.True(t, enabled)
}

// ── AllPlugins ────────────────────────────────────────────────────────────────

func TestAllPlugins_Empty(t *testing.T) {
	st := newTestStore(t)
	entries, err := st.AllPlugins()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestAllPlugins_OrderedByNameAsc(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("zebra.lua", "/z.lua"))
	require.NoError(t, st.RegisterPlugin("alpha.lua", "/a.lua"))
	require.NoError(t, st.RegisterPlugin("mango.lua", "/m.lua"))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 3)
	assert.Equal(t, "alpha.lua", entries[0].Name)
	assert.Equal(t, "mango.lua", entries[1].Name)
	assert.Equal(t, "zebra.lua", entries[2].Name)
}

func TestAllPlugins_EnabledFieldReflectsState(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("on.lua", "/on.lua"))
	require.NoError(t, st.RegisterPlugin("off.lua", "/off.lua"))
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "off.lua", Enabled: false})

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 2)

	byName := make(map[string]store.PluginEntry)
	for _, e := range entries {
		byName[e.Name] = e
	}
	assert.True(t, byName["on.lua"].Enabled)
	assert.False(t, byName["off.lua"].Enabled)
}

// ── ScanPlugins ───────────────────────────────────────────────────────────────

func TestScanPlugins_RemovesStaleRecords(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("keep.lua", "/keep.lua"))
	require.NoError(t, st.RegisterPlugin("gone.lua", "/gone.lua"))

	require.NoError(t, st.ScanPlugins([]string{"keep.lua"}))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "keep.lua", entries[0].Name)
}

func TestScanPlugins_EmptyActive_RemovesAll(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("a.lua", "/a.lua"))
	require.NoError(t, st.RegisterPlugin("b.lua", "/b.lua"))

	require.NoError(t, st.ScanPlugins([]string{}))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestScanPlugins_AllPresent_NothingRemoved(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("a.lua", "/a.lua"))
	require.NoError(t, st.RegisterPlugin("b.lua", "/b.lua"))

	require.NoError(t, st.ScanPlugins([]string{"a.lua", "b.lua"}))

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestScanPlugins_EmptyStore_NoPanic(t *testing.T) {
	st := newTestStore(t)
	assert.NoError(t, st.ScanPlugins([]string{"a.lua"}))
}

func TestScanPlugins_NotifiesPluginEntries(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("gone.lua", "/gone.lua"))

	for len(st.PluginEntries) > 0 {
		<-st.PluginEntries
	}

	require.NoError(t, st.ScanPlugins([]string{}))

	select {
	case <-st.PluginEntries:
	default:
		t.Fatal("PluginEntries channel was not notified after ScanPlugins")
	}
}

// ── PluginLogs ────────────────────────────────────────────────────────────────

func TestPluginLogs_UnknownPlugin_ReturnsNil(t *testing.T) {
	st := newTestStore(t)
	assert.Nil(t, st.PluginLogs("nonexistent.lua"))
}

func TestPluginLogs_ReturnsLinesInOrder(t *testing.T) {
	st := newTestStore(t)
	st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "line 1"})
	st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "line 2"})
	st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "line 3"})

	lines := st.PluginLogs("test.lua")
	require.Len(t, lines, 3)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
	assert.Equal(t, "line 3", lines[2])
}

func TestPluginLogs_ReturnsACopy(t *testing.T) {
	st := newTestStore(t)
	st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "original"})

	lines := st.PluginLogs("test.lua")
	lines[0] = "mutated"

	// Original must be unchanged.
	assert.Equal(t, "original", st.PluginLogs("test.lua")[0])
}

func TestPluginLogs_IsolatedPerPlugin(t *testing.T) {
	st := newTestStore(t)
	st.ObservePluginLog(events.PluginLogEvent{Name: "a.lua", Message: "from a"})
	st.ObservePluginLog(events.PluginLogEvent{Name: "b.lua", Message: "from b"})

	assert.Equal(t, []string{"from a"}, st.PluginLogs("a.lua"))
	assert.Equal(t, []string{"from b"}, st.PluginLogs("b.lua"))
}

func TestPluginLogs_RingBuffer_CapsAtMaxLines(t *testing.T) {
	st := newTestStore(t)
	// Write more than maxPluginLogLines (500) entries.
	for i := 0; i < 510; i++ {
		st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: fmt.Sprintf("line %d", i)})
	}

	lines := st.PluginLogs("test.lua")
	assert.Len(t, lines, 500)
	// The oldest 10 lines (0-9) were evicted; buffer starts at line 10.
	assert.Equal(t, "line 10", lines[0])
	assert.Equal(t, "line 509", lines[499])
}

// ── ObservePluginLog ──────────────────────────────────────────────────────────

func TestObservePluginLog_NotifiesPluginEntries(t *testing.T) {
	st := newTestStore(t)
	for len(st.PluginEntries) > 0 {
		<-st.PluginEntries
	}

	st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "hello"})

	select {
	case <-st.PluginEntries:
	default:
		t.Fatal("PluginEntries channel was not notified after ObservePluginLog")
	}
}

func TestObservePluginLog_ChannelNonBlocking_WhenFull(t *testing.T) {
	st := newTestStore(t)
	// Fill the channel to capacity.
	for i := 0; i < cap(st.PluginEntries); i++ {
		st.PluginEntries <- struct{}{}
	}
	// Must not block even when channel is full.
	assert.NotPanics(t, func() {
		st.ObservePluginLog(events.PluginLogEvent{Name: "test.lua", Message: "msg"})
	})
}

func TestObservePluginLog_ConcurrentWrites_Safe(t *testing.T) {
	st := newTestStore(t)
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			st.ObservePluginLog(events.PluginLogEvent{
				Name:    "test.lua",
				Message: fmt.Sprintf("msg %d", i),
			})
		}(i)
	}
	wg.Wait()

	lines := st.PluginLogs("test.lua")
	assert.Len(t, lines, goroutines)
}

// ── ObservePluginEnabled ──────────────────────────────────────────────────────

func TestObservePluginEnabled_PersistsDisabled(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/test.lua"))

	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	enabled, err := st.PluginEnabled("test.lua")
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestObservePluginEnabled_PersistsEnabled(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/test.lua"))
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: true})

	enabled, err := st.PluginEnabled("test.lua")
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestObservePluginEnabled_NotifiesPluginEntries(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.RegisterPlugin("test.lua", "/test.lua"))

	for len(st.PluginEntries) > 0 {
		<-st.PluginEntries
	}

	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	select {
	case <-st.PluginEntries:
	default:
		t.Fatal("PluginEntries channel was not notified after ObservePluginEnabled")
	}
}

func TestObservePluginEnabled_UnknownPlugin_NoError(t *testing.T) {
	st := newTestStore(t)
	// Toggling a plugin not in the DB must not panic or crash.
	assert.NotPanics(t, func() {
		st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "ghost.lua", Enabled: false})
	})
}
