package plugin

// White-box tests. Package plugin (not plugin_test) so unexported types and
// functions — load, loadDir, Plugin.has, Plugin.callWithBuilder,
// Plugin.callNoReturn — are directly accessible.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"

	"github.com/shiv/internal/store"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// writeLua writes a Lua script to a temp file and returns its path.
// The file is removed when the test ends.
func writeLua(t *testing.T, src string) string {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-plugin-*.lua")
	require.NoError(t, err)
	_, err = f.WriteString(src)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

// newTestStore opens a temp SQLite store for plugin tests.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-plugin-store-*.shiv")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	st, err := store.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

// loadPlugin is a convenience wrapper around load that also registers cleanup.
func loadPlugin(t *testing.T, src string, st *store.Store) *Plugin {
	t.Helper()
	path := writeLua(t, src)
	p, err := load(path, st)
	require.NoError(t, err)
	t.Cleanup(func() { p.close() })
	return p
}

// ── load ──────────────────────────────────────────────────────────────────────

func TestLoad_ValidScript_ReturnsPlugin(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `-- empty script`, st)
	assert.NotNil(t, p)
}

func TestLoad_InvalidScript_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	path := writeLua(t, `this is not valid lua %%%`)
	_, err := load(path, st)
	assert.Error(t, err)
}

func TestLoad_NonExistentFile_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	_, err := load("/does/not/exist.lua", st)
	assert.Error(t, err)
}

func TestLoad_CallsOnLoad(t *testing.T) {
	st := newTestStore(t)
	// on_load sets a global that we can check afterwards.
	p := loadPlugin(t, `
		loaded = false
		function on_load()
			loaded = true
		end
	`, st)

	p.mu.Lock()
	v := p.L.GetGlobal("loaded")
	p.mu.Unlock()

	assert.Equal(t, lua.LTrue, v)
}

func TestLoad_OnLoadError_PluginStillLoaded(t *testing.T) {
	st := newTestStore(t)
	// on_load errors are logged but must not prevent the plugin from loading.
	p, err := func() (*Plugin, error) {
		path := writeLua(t, `
			function on_load()
				error("intentional on_load failure")
			end
		`)
		return load(path, st)
	}()
	// load itself should succeed despite on_load failing.
	require.NoError(t, err)
	assert.NotNil(t, p)
	p.close()
}

func TestLoad_SetsName(t *testing.T) {
	st := newTestStore(t)
	path := writeLua(t, `-- empty`)
	p, err := load(path, st)
	require.NoError(t, err)
	defer p.close()
	assert.Equal(t, filepath.Base(path), p.name)
}

// ── has ───────────────────────────────────────────────────────────────────────

func TestHas_DefinedFunction_ReturnsTrue(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `function on_request(r) return r end`, st)
	assert.True(t, p.has("on_request"))
}

func TestHas_UndefinedFunction_ReturnsFalse(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `-- empty`, st)
	assert.False(t, p.has("on_request"))
}

func TestHas_NilGlobal_ReturnsFalse(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `on_request = nil`, st)
	assert.False(t, p.has("on_request"))
}

// ── callNoReturn ──────────────────────────────────────────────────────────────

func TestCallNoReturn_UndefinedFunction_ReturnsNil(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `-- empty`, st)
	err := p.callNoReturn("no_such_fn", lua.LNil)
	assert.NoError(t, err)
}

func TestCallNoReturn_FunctionExecutes(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		called = false
		function my_fn()
			called = true
		end
	`, st)

	err := p.callNoReturn("my_fn", lua.LNil)
	require.NoError(t, err)

	p.mu.Lock()
	v := p.L.GetGlobal("called")
	p.mu.Unlock()
	assert.Equal(t, lua.LTrue, v)
}

func TestCallNoReturn_FunctionReceivesArg(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		received = nil
		function my_fn(arg)
			received = arg
		end
	`, st)

	p.mu.Lock()
	arg := p.L.NewTable()
	p.L.SetField(arg, "key", lua.LString("value"))
	p.mu.Unlock()

	err := p.callNoReturn("my_fn", arg)
	require.NoError(t, err)

	p.mu.Lock()
	tbl, ok := p.L.GetGlobal("received").(*lua.LTable)
	p.mu.Unlock()
	require.True(t, ok)

	p.mu.Lock()
	v := tbl.RawGetString("key")
	p.mu.Unlock()
	assert.Equal(t, lua.LString("value"), v)
}

func TestCallNoReturn_LuaError_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		function bad_fn()
			error("intentional error")
		end
	`, st)

	err := p.callNoReturn("bad_fn", lua.LNil)
	assert.Error(t, err)
}

func TestCallNoReturn_Timeout_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	// Infinite loop — must be killed by the context timeout.
	p := loadPlugin(t, `
		function infinite()
			while true do end
		end
	`, st)

	start := time.Now()
	err := p.callNoReturn("infinite", lua.LNil)
	elapsed := time.Since(start)

	assert.Error(t, err)
	// Should terminate within a reasonable margin of callTimeout (500ms).
	assert.Less(t, elapsed, callTimeout+300*time.Millisecond)
}

// ── callWithBuilder ───────────────────────────────────────────────────────────

func TestCallWithBuilder_UndefinedFunction_ReturnsNilNoError(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `-- empty`, st)

	ret, err := p.callWithBuilder("no_such_fn", func(L *lua.LState) *lua.LTable {
		return L.NewTable()
	})

	assert.NoError(t, err)
	assert.Nil(t, ret)
}

func TestCallWithBuilder_BuilderRunsUnderLock(t *testing.T) {
	// Verify the builder is called (indirectly — if it panics the test fails).
	st := newTestStore(t)
	p := loadPlugin(t, `function fn(t) return t end`, st)

	builderCalled := false
	ret, err := p.callWithBuilder("fn", func(L *lua.LState) *lua.LTable {
		builderCalled = true
		tbl := L.NewTable()
		L.SetField(tbl, "x", lua.LString("hello"))
		return tbl
	})

	require.NoError(t, err)
	assert.True(t, builderCalled)
	assert.NotNil(t, ret)
}

func TestCallWithBuilder_FunctionCanModifyTable(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		function transform(t)
			t.result = "modified"
			return t
		end
	`, st)

	ret, err := p.callWithBuilder("transform", func(L *lua.LState) *lua.LTable {
		tbl := L.NewTable()
		L.SetField(tbl, "result", lua.LString("original"))
		return tbl
	})

	require.NoError(t, err)
	require.NotNil(t, ret)

	p.mu.Lock()
	v := ret.RawGetString("result")
	p.mu.Unlock()
	assert.Equal(t, lua.LString("modified"), v)
}

func TestCallWithBuilder_FunctionReturnsNil_ReturnsNilTable(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `function fn(t) return nil end`, st)

	ret, err := p.callWithBuilder("fn", func(L *lua.LState) *lua.LTable {
		return L.NewTable()
	})

	assert.NoError(t, err)
	assert.Nil(t, ret)
}

func TestCallWithBuilder_LuaError_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		function bad(t)
			error("builder error")
		end
	`, st)

	_, err := p.callWithBuilder("bad", func(L *lua.LState) *lua.LTable {
		return L.NewTable()
	})

	assert.Error(t, err)
}

func TestCallWithBuilder_Timeout_ReturnsError(t *testing.T) {
	st := newTestStore(t)
	p := loadPlugin(t, `
		function slow(t)
			while true do end
		end
	`, st)

	start := time.Now()
	_, err := p.callWithBuilder("slow", func(L *lua.LState) *lua.LTable {
		return L.NewTable()
	})
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Less(t, elapsed, callTimeout+300*time.Millisecond)
}

// ── loadDir ───────────────────────────────────────────────────────────────────

func TestLoadDir_NonExistentDir_ReturnsNilSlice(t *testing.T) {
	st := newTestStore(t)
	plugins, err := loadDir("/does/not/exist", st)
	assert.NoError(t, err)
	assert.Nil(t, plugins)
}

func TestLoadDir_EmptyDir_ReturnsEmpty(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	plugins, err := loadDir(dir, st)
	assert.NoError(t, err)
	assert.Empty(t, plugins)
}

func TestLoadDir_LoadsLuaFiles(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.lua"), []byte(`x = 1`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.lua"), []byte(`y = 2`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte(`not lua`), 0644))

	plugins, err := loadDir(dir, st)
	require.NoError(t, err)
	assert.Len(t, plugins, 2)
	for _, p := range plugins {
		p.close()
	}
}

func TestLoadDir_SkipsDirectories(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()

	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir.lua"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.lua"), []byte(`z = 3`), 0644))

	plugins, err := loadDir(dir, st)
	require.NoError(t, err)
	assert.Len(t, plugins, 1)
	for _, p := range plugins {
		p.close()
	}
}

func TestLoadDir_InvalidLuaSkipped_OthersLoaded(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.lua"), []byte(`g = 1`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.lua"), []byte(`%%% invalid`), 0644))

	plugins, err := loadDir(dir, st)
	require.NoError(t, err)
	// bad.lua skipped, good.lua loaded
	assert.Len(t, plugins, 1)
	assert.Equal(t, "good.lua", plugins[0].name)
	for _, p := range plugins {
		p.close()
	}
}
