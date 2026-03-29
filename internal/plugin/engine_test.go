package plugin_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/plugin"
	"github.com/shiv/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func pluginDir(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0644))
	}
	return dir
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp("", "shiv-plugin-engine-*.shiv")
	require.NoError(t, err)
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	st, err := store.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

// newEngineWithBus creates an Engine using a caller-supplied bus so tests can
// register custom observers (e.g. to capture log events).
func newEngineWithBus(t *testing.T, st *store.Store, bus *events.Bus, scripts map[string]string) *plugin.Engine {
	t.Helper()
	dir := pluginDir(t, scripts)
	e, err := plugin.NewEngine(dir, st, bus)
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })
	return e
}

// newEngine creates an Engine with a no-op bus — sufficient for tests that do
// not need to observe log or enable events.
func newEngine(t *testing.T, st *store.Store, scripts map[string]string) *plugin.Engine {
	t.Helper()
	return newEngineWithBus(t, st, events.NewBus(), scripts)
}

func requestEvent(t *testing.T, method, rawURL string, body []byte) events.RequestEvent {
	t.Helper()
	return events.RequestEvent{
		Request: httptest.NewRequest(method, rawURL, nil),
		Body:    body,
	}
}

func responseEvent() events.ResponseEvent {
	return events.ResponseEvent{
		Timestamp:   time.Now(),
		Host:        "example.com",
		Proto:       "HTTP/1.1",
		Method:      "GET",
		URL:         "http://example.com/path",
		ReqHeaders:  http.Header{"X-Req": []string{"req-val"}},
		ReqBody:     []byte("req body"),
		StatusCode:  200,
		RespHeaders: http.Header{"X-Resp": []string{"resp-val"}},
		RespBody:    []byte("resp body"),
		DurationMs:  55,
		TLS:         true,
	}
}

func wsFrameEvent(connID uint64, payload []byte) events.WebSocketFrameEvent {
	return events.WebSocketFrameEvent{
		ConnectionID: connID,
		Timestamp:    time.Now(),
		Direction:    events.WebSocketClient,
		Opcode:       events.WebSocketText,
		Payload:      payload,
	}
}

// ── NewEngine ─────────────────────────────────────────────────────────────────

func TestNewEngine_EmptyDir_ReturnsEngine(t *testing.T) {
	st := newTestStore(t)
	e, err := plugin.NewEngine(t.TempDir(), st, events.NewBus())
	require.NoError(t, err)
	assert.NotNil(t, e)
	e.Close()
}

func TestNewEngine_NonExistentDir_ReturnsEngine(t *testing.T) {
	st := newTestStore(t)
	e, err := plugin.NewEngine("/does/not/exist", st, events.NewBus())
	require.NoError(t, err)
	assert.NotNil(t, e)
	e.Close()
}

func TestNewEngine_InvalidScript_SkippedEngineStillCreated(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"bad.lua":  `%%% not lua`,
		"good.lua": `x = 1`,
	})
	assert.NotPanics(t, func() {
		e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	})
}

func TestNewEngine_RegistersPluginsInStore(t *testing.T) {
	st := newTestStore(t)
	newEngine(t, st, map[string]string{
		"a.lua": `x = 1`,
		"b.lua": `y = 2`,
	})

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestNewEngine_RestoresEnabledStateFromStore(t *testing.T) {
	st := newTestStore(t)
	dir := pluginDir(t, map[string]string{
		"test.lua": `function on_request(r) r.drop = true; return r end`,
	})

	// First load — plugin is enabled by default and drops requests.
	e1, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	result := e1.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.True(t, result.Drop)
	e1.Close()

	// Disable via store directly.
	st.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "test.lua", Enabled: false})

	// Second load — must restore disabled state from store.
	e2, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e2.Close() })
	result = e2.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.False(t, result.Drop, "plugin disabled in store must not fire on_request")
}

func TestNewEngine_ScanRemovesStaleRecords(t *testing.T) {
	st := newTestStore(t)

	// Manually register a plugin that won't be on disk.
	require.NoError(t, st.RegisterPlugin("ghost.lua", "/ghost.lua"))

	dir := pluginDir(t, map[string]string{"real.lua": `x = 1`})
	_, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "real.lua", entries[0].Name)
}

// ── HandleRequest — no hook ───────────────────────────────────────────────────

func TestHandleRequest_NoPlugins_PassThrough(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, nil)
	ev := requestEvent(t, "GET", "http://example.com/path", []byte("body"))
	result := e.HandleRequest(ev)
	assert.False(t, result.Drop)
	assert.Equal(t, ev.Request, result.Request)
	assert.Equal(t, ev.Body, result.Body)
}

func TestHandleRequest_PluginWithNoOnRequest_PassThrough(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{"noop.lua": `-- no hooks`})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.False(t, result.Drop)
}

func TestHandleRequest_NilRequest_PassThrough(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"hook.lua": `function on_request(r) r.drop = true; return r end`,
	})
	result := e.HandleRequest(events.RequestEvent{Request: nil, Body: nil})
	assert.False(t, result.Drop)
}

// ── HandleRequest — drop ──────────────────────────────────────────────────────

func TestHandleRequest_Drop_ReturnsDrop(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"drop.lua": `function on_request(r) r.drop = true; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.True(t, result.Drop)
}

func TestHandleRequest_FirstPluginDrops_SecondNotCalled(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_drop.lua":  `function on_request(r) r.drop = true; return r end`,
		"2_panic.lua": `function on_request(r) error("must not be called") end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.True(t, result.Drop)
}

// ── HandleRequest — modifications ─────────────────────────────────────────────

func TestHandleRequest_ModifiesBody(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"body.lua": `function on_request(r) r.body = "injected"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "POST", "http://example.com", []byte("original")))
	assert.False(t, result.Drop)
	assert.Equal(t, []byte("injected"), result.Body)
}

func TestHandleRequest_ModifiesMethod(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"method.lua": `function on_request(r) r.method = "POST"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.Equal(t, "POST", result.Request.Method)
}

func TestHandleRequest_ModifiesURL(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"url.lua": `function on_request(r) r.url = "http://example.com/modified"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com/original", nil))
	assert.Equal(t, "/modified", result.Request.URL.Path)
}

func TestHandleRequest_ModifiesHeaders(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"headers.lua": `function on_request(r) r.headers["X-Plugin"] = "injected"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.Equal(t, "injected", result.Request.Header.Get("X-Plugin"))
}

func TestHandleRequest_ChainedPlugins_ModificationsAccumulate(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_first.lua":  `function on_request(r) r.body = r.body .. "-first"; return r end`,
		"2_second.lua": `function on_request(r) r.body = r.body .. "-second"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("start")))
	assert.Equal(t, []byte("start-first-second"), result.Body)
}

func TestHandleRequest_LuaError_PluginSkipped_ChainContinues(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_error.lua": `function on_request(r) error("oops") end`,
		"2_ok.lua":    `function on_request(r) r.body = "reached"; return r end`,
	})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("original")))
	assert.False(t, result.Drop)
	assert.Equal(t, []byte("reached"), result.Body)
}

// ── HandleRequest — enabled flag ──────────────────────────────────────────────

func TestHandleRequest_DisabledPlugin_HookNotCalled(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"drop.lua": `function on_request(r) r.drop = true; return r end`,
	})

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "drop.lua", Enabled: false})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.False(t, result.Drop, "disabled plugin must not fire on_request")
}

func TestHandleRequest_ReenabledPlugin_HookCalledAgain(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"drop.lua": `function on_request(r) r.drop = true; return r end`,
	})

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "drop.lua", Enabled: false})
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.False(t, result.Drop)

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "drop.lua", Enabled: true})
	result = e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.True(t, result.Drop, "re-enabled plugin must fire on_request again")
}

func TestHandleRequest_DisabledPluginInChain_OthersStillRun(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_disabled.lua": `function on_request(r) r.body = r.body .. "-disabled"; return r end`,
		"2_enabled.lua":  `function on_request(r) r.body = r.body .. "-enabled"; return r end`,
	})

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "1_disabled.lua", Enabled: false})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("start")))
	assert.Equal(t, []byte("start-enabled"), result.Body)
}

// ── ObservePluginEnabled ──────────────────────────────────────────────────────

func TestObservePluginEnabled_UnknownPlugin_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, nil)
	assert.NotPanics(t, func() {
		e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "ghost.lua", Enabled: false})
	})
}

func TestObservePluginEnabled_TakesEffectImmediately(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"drop.lua": `function on_request(r) r.drop = true; return r end`,
	})

	// Enabled by default — drops.
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.True(t, result.Drop)

	// Disable — next call must pass through.
	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "drop.lua", Enabled: false})
	result = e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	assert.False(t, result.Drop)
}

// ── ObserveResponse ───────────────────────────────────────────────────────────

func TestObserveResponse_NoPlugins_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, nil)
	assert.NotPanics(t, func() { e.ObserveResponse(responseEvent()) })
}

func TestObserveResponse_ReceivesCorrectFields(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"observe.lua": `
			function on_response(r)
				db.loot_add(r.host .. "|" .. r.method .. "|" .. tostring(r.status) .. "|" .. tostring(r.tls), "Info")
			end
		`,
	})

	ev := responseEvent()
	e.ObserveResponse(ev)
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, fmt.Sprintf("%s|%s|%d|%v", ev.Host, ev.Method, ev.StatusCode, ev.TLS), loot[0].Title)
}

func TestObserveResponse_MultiplePluginsAllCalled(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"a.lua": `function on_response(r) db.loot_add("a", "Info") end`,
		"b.lua": `function on_response(r) db.loot_add("b", "Info") end`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)
	loot, err := st.AllLoot()
	require.NoError(t, err)
	assert.Len(t, loot, 2)
}

func TestObserveResponse_LuaError_OtherPluginsContinue(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_error.lua": `function on_response(r) error("fail") end`,
		"2_ok.lua":    `function on_response(r) db.loot_add("ok", "Info") end`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)
	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "ok", loot[0].Title)
}

func TestObserveResponse_DisabledPlugin_HookNotCalled(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"obs.lua": `function on_response(r) db.loot_add("called", "Info") end`,
	})

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "obs.lua", Enabled: false})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	assert.Empty(t, loot, "disabled plugin must not fire on_response")
}

// ── ObserveWebSocketFrame ─────────────────────────────────────────────────────

func TestObserveWebSocketFrame_NoPlugins_ReturnsOriginalPayload(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, nil)
	ev := wsFrameEvent(1, []byte("hello"))
	result := e.ObserveWebSocketFrame(ev)
	assert.Equal(t, []byte("hello"), result.Payload)
}

func TestObserveWebSocketFrame_PluginWithNoHook_ReturnsOriginalPayload(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{"noop.lua": `-- no hooks`})
	result := e.ObserveWebSocketFrame(wsFrameEvent(1, []byte("original")))
	assert.Equal(t, []byte("original"), result.Payload)
}

func TestObserveWebSocketFrame_PluginModifiesPayload(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"ws.lua": `
			function on_websocket_frame(f)
				f.payload = f.payload .. "-modified"
				return f
			end
		`,
	})
	result := e.ObserveWebSocketFrame(wsFrameEvent(1, []byte("hello")))
	assert.Equal(t, []byte("hello-modified"), result.Payload)
}

func TestObserveWebSocketFrame_ChainedPlugins_ModificationsAccumulate(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_first.lua":  `function on_websocket_frame(f) f.payload = f.payload .. "-first"; return f end`,
		"2_second.lua": `function on_websocket_frame(f) f.payload = f.payload .. "-second"; return f end`,
	})
	result := e.ObserveWebSocketFrame(wsFrameEvent(1, []byte("start")))
	assert.Equal(t, []byte("start-first-second"), result.Payload)
}

func TestObserveWebSocketFrame_ReceivesCorrectFields(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"ws.lua": `
			function on_websocket_frame(f)
				db.loot_add(
					tostring(f.connection_id) .. "|" .. tostring(f.direction) .. "|" .. tostring(f.opcode),
					"Info"
				)
				return f
			end
		`,
	})

	ev := events.WebSocketFrameEvent{
		ConnectionID: 42,
		Timestamp:    time.Now(),
		Direction:    events.WebSocketServer,
		Opcode:       events.WebSocketBinary,
		Payload:      []byte("data"),
	}
	e.ObserveWebSocketFrame(ev)
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "42|1|2", loot[0].Title)
}

func TestObserveWebSocketFrame_DisabledPlugin_HookNotCalled(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"ws.lua": `function on_websocket_frame(f) f.payload = "modified"; return f end`,
	})

	e.ObservePluginEnabled(events.SetPluginEnabledEvent{Name: "ws.lua", Enabled: false})
	result := e.ObserveWebSocketFrame(wsFrameEvent(1, []byte("original")))
	assert.Equal(t, []byte("original"), result.Payload, "disabled plugin must not modify frame")
}

func TestObserveWebSocketFrame_LuaError_ReturnsLastGoodPayload(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_ok.lua":    `function on_websocket_frame(f) f.payload = "good"; return f end`,
		"2_error.lua": `function on_websocket_frame(f) error("fail") end`,
	})
	result := e.ObserveWebSocketFrame(wsFrameEvent(1, []byte("original")))
	assert.Equal(t, []byte("good"), result.Payload)
}

// ── ObserveLoadPlugin ─────────────────────────────────────────────────────────

func TestObserveLoadPlugin_LoadsAndRegistersPlugin(t *testing.T) {
	st := newTestStore(t)
	destDir := t.TempDir()
	e, err := plugin.NewEngine(destDir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	// Write source file to a separate temp dir.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "new.lua")
	require.NoError(t, os.WriteFile(srcPath, []byte(`
		function on_request(r)
			r.body = "from-new-plugin"
			return r
		end
	`), 0644))

	e.ObserveLoadPlugin(events.LoadPluginEvent{SourcePath: srcPath})

	// Plugin must be active immediately.
	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("original")))
	assert.Equal(t, []byte("from-new-plugin"), result.Body)
}

func TestObserveLoadPlugin_CopiesFileToPluginsDir(t *testing.T) {
	st := newTestStore(t)
	destDir := t.TempDir()
	e, err := plugin.NewEngine(destDir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	srcPath := filepath.Join(t.TempDir(), "copied.lua")
	require.NoError(t, os.WriteFile(srcPath, []byte(`x = 1`), 0644))

	e.ObserveLoadPlugin(events.LoadPluginEvent{SourcePath: srcPath})

	_, err = os.Stat(filepath.Join(destDir, "copied.lua"))
	assert.NoError(t, err, "file must be copied to plugins directory")
}

func TestObserveLoadPlugin_RegistersInStore(t *testing.T) {
	st := newTestStore(t)
	destDir := t.TempDir()
	e, err := plugin.NewEngine(destDir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	srcPath := filepath.Join(t.TempDir(), "registered.lua")
	require.NoError(t, os.WriteFile(srcPath, []byte(`x = 1`), 0644))

	e.ObserveLoadPlugin(events.LoadPluginEvent{SourcePath: srcPath})
	time.Sleep(50 * time.Millisecond)

	entries, err := st.AllPlugins()
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "registered.lua")
}

func TestObserveLoadPlugin_BadSourcePath_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e, err := plugin.NewEngine(t.TempDir(), st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	assert.NotPanics(t, func() {
		e.ObserveLoadPlugin(events.LoadPluginEvent{SourcePath: "/does/not/exist.lua"})
	})
}

func TestObserveLoadPlugin_InvalidLua_NoPanic(t *testing.T) {
	st := newTestStore(t)
	destDir := t.TempDir()
	e, err := plugin.NewEngine(destDir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	srcPath := filepath.Join(t.TempDir(), "bad.lua")
	require.NoError(t, os.WriteFile(srcPath, []byte(`%%% invalid lua`), 0644))

	assert.NotPanics(t, func() {
		e.ObserveLoadPlugin(events.LoadPluginEvent{SourcePath: srcPath})
	})
}

// ── log() routing ─────────────────────────────────────────────────────────────

func TestLog_RoutedThroughBus(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()

	var mu sync.Mutex
	var logLines []string
	bus.Register(events.PluginLogObserverFunc(func(e events.PluginLogEvent) {
		mu.Lock()
		logLines = append(logLines, e.Name+": "+e.Message)
		mu.Unlock()
	}))

	newEngineWithBus(t, st, bus, map[string]string{
		"logger.lua": `
			function on_request(r)
				log("intercepted request")
				return r
			end
		`,
	})

	newEngineWithBus(t, st, bus, map[string]string{
		"logger.lua": `
			function on_request(r)
				log("intercepted request")
				return r
			end
		`,
	}).HandleRequest(requestEvent(t, "GET", "http://example.com", nil))

	mu.Lock()
	found := false
	for _, l := range logLines {
		if l == "logger.lua: intercepted request" {
			found = true
			break
		}
	}
	mu.Unlock()
	assert.True(t, found, "log() call must emit PluginLogEvent on the bus")
}

func TestLog_OnLoad_DrainedAfterNewEngine(t *testing.T) {
	st := newTestStore(t)
	bus := events.NewBus()

	var mu sync.Mutex
	var logLines []string
	bus.Register(events.PluginLogObserverFunc(func(e events.PluginLogEvent) {
		mu.Lock()
		logLines = append(logLines, e.Message)
		mu.Unlock()
	}))

	dir := pluginDir(t, map[string]string{
		"startup.lua": `
			function on_load()
				log("loaded ok")
			end
		`,
	})
	e, err := plugin.NewEngine(dir, st, bus)
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })

	mu.Lock()
	lines := append([]string{}, logLines...)
	mu.Unlock()
	assert.Contains(t, lines, "loaded ok", "on_load log() calls must be emitted before NewEngine returns")
}

// ── Engine.Close ──────────────────────────────────────────────────────────────

func TestClose_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e, err := plugin.NewEngine(t.TempDir(), st, events.NewBus())
	require.NoError(t, err)
	assert.NotPanics(t, func() { e.Close() })
}

// ── API: log ──────────────────────────────────────────────────────────────────

func TestAPI_Log_IsAvailable(t *testing.T) {
	st := newTestStore(t)
	dir := pluginDir(t, map[string]string{
		"log.lua": `function on_load() log("hello from plugin") end`,
	})
	e, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	e.Close()
}

// ── API: http.get ─────────────────────────────────────────────────────────────

func TestAPI_HTTPGet_ReturnsStatusAndBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "pong")
	}))
	defer upstream.Close()

	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"get.lua": fmt.Sprintf(`
			function on_response(r)
				local resp = http.get("%s")
				if resp then db.loot_add(tostring(resp.status) .. "|" .. resp.body, "Info") end
			end
		`, upstream.URL),
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(100 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "200|pong", loot[0].Title)
}

func TestAPI_HTTPGet_BadURL_ReturnsNilAndError(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"get_fail.lua": `
			function on_response(r)
				local resp, err = http.get("http://127.0.0.1:1/")
				if resp == nil and err ~= nil then db.loot_add("error_handled", "Info") end
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(200 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "error_handled", loot[0].Title)
}

// ── API: http.post ────────────────────────────────────────────────────────────

func TestAPI_HTTPPost_SendsBodyAndReturnsResponse(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "created")
	}))
	defer upstream.Close()

	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"post.lua": fmt.Sprintf(`
			function on_response(r)
				local resp = http.post("%s", '{"key":"val"}', "application/json")
				if resp then db.loot_add(tostring(resp.status) .. "|" .. resp.body, "Info") end
			end
		`, upstream.URL),
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(100 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "201|created", loot[0].Title)
	assert.Equal(t, `{"key":"val"}`, receivedBody)
}

// ── API: http.request ─────────────────────────────────────────────────────────

func TestAPI_HTTPRequest_SendsCustomMethodAndHeaders(t *testing.T) {
	var receivedMethod, receivedHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"request.lua": fmt.Sprintf(`
			function on_response(r)
				local resp = http.request("DELETE", "%s", "", {["X-Custom"] = "plugin-value"})
				if resp then db.loot_add(tostring(resp.status), "Info") end
			end
		`, upstream.URL),
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, "DELETE", receivedMethod)
	assert.Equal(t, "plugin-value", receivedHeader)
}

// ── API: db.history ───────────────────────────────────────────────────────────

func TestAPI_DBHistory_ReturnsTransactions(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.Log(store.Transaction{
		Timestamp: time.Now(), Host: "target.com", Method: "GET",
		URL: "http://target.com/", Proto: "HTTP/1.1", StatusCode: 200, InScope: true,
	}))
	time.Sleep(50 * time.Millisecond)

	e := newEngine(t, st, map[string]string{
		"history.lua": `
			function on_response(r)
				local txs = db.history(10)
				if txs then db.loot_add(tostring(#txs), "Info") end
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "1", loot[0].Title)
}

func TestAPI_DBHistory_TransactionFieldsExposed(t *testing.T) {
	st := newTestStore(t)
	require.NoError(t, st.Log(store.Transaction{
		Timestamp: time.Now(), Host: "check.com", Method: "POST",
		URL: "http://check.com/api", Proto: "HTTP/1.1", StatusCode: 201, InScope: true,
	}))
	time.Sleep(50 * time.Millisecond)

	e := newEngine(t, st, map[string]string{
		"fields.lua": `
			function on_response(r)
				local txs = db.history(1)
				if txs and #txs > 0 then
					local tx = txs[1]
					db.loot_add(tx.host .. "|" .. tx.method .. "|" .. tostring(tx.status), "Info")
				end
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "check.com|POST|201", loot[0].Title)
}

// ── API: db.history_filter ────────────────────────────────────────────────────

func TestAPI_DBHistoryFilter_FiltersByHost(t *testing.T) {
	st := newTestStore(t)
	for _, host := range []string{"alpha.com", "beta.com", "alpha.com"} {
		require.NoError(t, st.Log(store.Transaction{
			Timestamp: time.Now(), Host: host, Method: "GET",
			URL: "http://" + host + "/", Proto: "HTTP/1.1", StatusCode: 200, InScope: true,
		}))
	}
	time.Sleep(50 * time.Millisecond)

	e := newEngine(t, st, map[string]string{
		"filter.lua": `
			function on_response(r)
				local txs = db.history_filter("alpha.com")
				db.loot_add(tostring(#txs), "Info")
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "2", loot[0].Title)
}

// ── API: db.scope ─────────────────────────────────────────────────────────────

func TestAPI_DBScope_AddGetRemove(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"scope.lua": `
			function on_response(r)
				db.scope_add("plugin-scope.com")
				local entries = db.scope_get()
				db.loot_add("count:" .. tostring(#entries), "Info")
				if #entries > 0 then db.scope_remove(entries[1].id) end
				local after = db.scope_get()
				db.loot_add("after:" .. tostring(#after), "Info")
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 2)
	titles := map[string]bool{}
	for _, l := range loot {
		titles[l.Title] = true
	}
	assert.True(t, titles["count:1"])
	assert.True(t, titles["after:0"])
}

// ── API: db.loot ──────────────────────────────────────────────────────────────

func TestAPI_DBLoot_AllFieldsStored(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"loot.lua": `
			function on_response(r)
				db.loot_add("my finding", "Critical", "detailed notes", "raw req", "raw resp")
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	assert.Equal(t, "my finding", loot[0].Title)
	assert.Equal(t, "Critical", loot[0].Severity)
	assert.Equal(t, "detailed notes", loot[0].Notes)
	assert.Equal(t, "raw req", loot[0].RawRequest)
	assert.Equal(t, "raw resp", loot[0].RawResponse)
}

func TestAPI_DBLoot_AddGetDelete(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"loot.lua": `
			function on_response(r)
				local id = db.loot_add("finding", "High", "notes", "req", "resp")
				local entries = db.loot_get()
				db.loot_add("count:" .. tostring(#entries), "Info")
				db.loot_delete(id)
				local after = db.loot_get()
				db.loot_add("after:" .. tostring(#after), "Info")
			end
		`,
	})
	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	titles := map[string]bool{}
	for _, l := range loot {
		titles[l.Title] = true
	}
	assert.True(t, titles["count:1"])
	assert.True(t, titles["after:1"])
}

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestEngine_ConcurrentHandleRequest_Safe(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"concurrent.lua": `function on_request(r) r.body = r.body .. "-ok"; return r end`,
	})
	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("body")))
			assert.False(t, result.Drop)
		}()
	}
	wg.Wait()
}

func TestEngine_ConcurrentObserveResponse_Safe(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{"obs.lua": `function on_response(r) end`})
	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			e.ObserveResponse(responseEvent())
		}()
	}
	wg.Wait()
}
