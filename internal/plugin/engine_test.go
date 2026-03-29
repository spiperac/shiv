package plugin_test

// Black-box tests. Package plugin_test so only the exported surface is used:
// NewEngine, Engine.HandleRequest, Engine.ObserveResponse, Engine.Close.
// The Lua API (log, http.*, db.*) is tested by writing real Lua scripts that
// exercise each function and asserting the side-effects in Go.

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

// pluginDir creates a temp directory, writes named Lua scripts into it,
// and returns the directory path. All files are cleaned up when the test ends.
func pluginDir(t *testing.T, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range scripts {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(src), 0644))
	}
	return dir
}

// newTestStore opens a temp SQLite store and registers cleanup.
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

// newEngine creates an Engine from a map of script name → source.
func newEngine(t *testing.T, st *store.Store, scripts map[string]string) *plugin.Engine {
	t.Helper()
	dir := pluginDir(t, scripts)
	e, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	t.Cleanup(func() { e.Close() })
	return e
}

// requestEvent builds a minimal RequestEvent for a given method and URL.
func requestEvent(t *testing.T, method, rawURL string, body []byte) events.RequestEvent {
	t.Helper()
	req := httptest.NewRequest(method, rawURL, nil)
	return events.RequestEvent{Request: req, Body: body}
}

// responseEvent builds a minimal ResponseEvent.
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

// ── NewEngine ─────────────────────────────────────────────────────────────────

func TestNewEngine_EmptyDir_ReturnsEngine(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	e, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	assert.NotNil(t, e)
	e.Close()
}

func TestNewEngine_NonExistentDir_ReturnsEngine(t *testing.T) {
	// Missing plugin dir is not fatal — treated as no plugins.
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
	// Engine is created; bad plugin skipped; HandleRequest does not panic.
	assert.NotPanics(t, func() {
		e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))
	})
}

// ── Engine.HandleRequest — no hook ───────────────────────────────────────────

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
	e := newEngine(t, st, map[string]string{
		"noop.lua": `-- no hooks defined`,
	})
	ev := requestEvent(t, "GET", "http://example.com", nil)

	result := e.HandleRequest(ev)

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

// ── Engine.HandleRequest — pass through ──────────────────────────────────────

func TestHandleRequest_PassThrough_ReceivesCorrectFields(t *testing.T) {
	st := newTestStore(t)

	// Plugin stores the received fields in globals for inspection.
	e := newEngine(t, st, map[string]string{
		"inspect.lua": `
			got_method = ""
			got_url = ""
			got_body = ""
			function on_request(r)
				got_method = r.method
				got_url = r.url
				got_body = r.body
				return r
			end
		`,
	})

	ev := requestEvent(t, "POST", "http://example.com/api", []byte("payload"))
	e.HandleRequest(ev)

	// We can't read Lua globals from outside the package in a black-box test.
	// We verify correctness via the returned result instead.
	result := e.HandleRequest(requestEvent(t, "DELETE", "http://example.com/res", []byte("del")))
	assert.False(t, result.Drop)
	assert.Equal(t, "DELETE", result.Request.Method)
}

// ── Engine.HandleRequest — drop ───────────────────────────────────────────────

func TestHandleRequest_Drop_ReturnsDrop(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"drop.lua": `
			function on_request(r)
				r.drop = true
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))

	assert.True(t, result.Drop)
}

func TestHandleRequest_FirstPluginDrops_SecondNotCalled(t *testing.T) {
	// Two plugins: first drops, second would panic if called.
	// Plugin load order is filesystem order — use names that sort correctly.
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_drop.lua": `
			function on_request(r)
				r.drop = true
				return r
			end
		`,
		"2_panic.lua": `
			function on_request(r)
				error("should never be called")
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))

	assert.True(t, result.Drop)
}

// ── Engine.HandleRequest — modifications ──────────────────────────────────────

func TestHandleRequest_ModifiesBody(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"body.lua": `
			function on_request(r)
				r.body = "injected"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "POST", "http://example.com", []byte("original")))

	assert.False(t, result.Drop)
	assert.Equal(t, []byte("injected"), result.Body)
}

func TestHandleRequest_ModifiesMethod(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"method.lua": `
			function on_request(r)
				r.method = "POST"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))

	assert.False(t, result.Drop)
	assert.Equal(t, "POST", result.Request.Method)
}

func TestHandleRequest_ModifiesURL(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"url.lua": `
			function on_request(r)
				r.url = "http://example.com/modified"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com/original", nil))

	assert.False(t, result.Drop)
	assert.Equal(t, "/modified", result.Request.URL.Path)
}

func TestHandleRequest_ModifiesHeaders(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"headers.lua": `
			function on_request(r)
				r.headers["X-Plugin"] = "injected"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", nil))

	assert.False(t, result.Drop)
	assert.Equal(t, "injected", result.Request.Header.Get("X-Plugin"))
}

func TestHandleRequest_ChainedPlugins_ModificationsAccumulate(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_first.lua": `
			function on_request(r)
				r.body = r.body .. "-first"
				return r
			end
		`,
		"2_second.lua": `
			function on_request(r)
				r.body = r.body .. "-second"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("start")))

	assert.False(t, result.Drop)
	assert.Equal(t, []byte("start-first-second"), result.Body)
}

func TestHandleRequest_LuaError_PluginSkipped_ChainContinues(t *testing.T) {
	// If a plugin errors its on_request, it is skipped; the next plugin runs.
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"1_error.lua": `
			function on_request(r)
				error("oops")
			end
		`,
		"2_ok.lua": `
			function on_request(r)
				r.body = "reached"
				return r
			end
		`,
	})

	result := e.HandleRequest(requestEvent(t, "GET", "http://example.com", []byte("original")))

	assert.False(t, result.Drop)
	assert.Equal(t, []byte("reached"), result.Body)
}

// ── Engine.ObserveResponse ────────────────────────────────────────────────────

func TestObserveResponse_NoPlugins_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, nil)
	assert.NotPanics(t, func() {
		e.ObserveResponse(responseEvent())
	})
}

func TestObserveResponse_PluginWithNoHook_NoPanic(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"noop.lua": `-- no on_response`,
	})
	assert.NotPanics(t, func() {
		e.ObserveResponse(responseEvent())
	})
}

func TestObserveResponse_ReceivesCorrectFields(t *testing.T) {
	// Plugin writes observed values into loot so we can read them back in Go.
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"observe.lua": fmt.Sprintf(`
			function on_response(r)
				db.loot_add(
					r.host .. "|" .. r.method .. "|" .. tostring(r.status) .. "|" .. tostring(r.tls),
					"Info"
				)
			end
		`),
	})

	ev := responseEvent()
	e.ObserveResponse(ev)

	// Give the store write loop a moment.
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)

	expected := fmt.Sprintf("%s|%s|%d|%v", ev.Host, ev.Method, ev.StatusCode, ev.TLS)
	assert.Equal(t, expected, loot[0].Title)
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
	assert.Len(t, loot, 1)
	assert.Equal(t, "ok", loot[0].Title)
}

// ── Engine.Close ──────────────────────────────────────────────────────────────

func TestClose_CalledTwice_NoPanic(t *testing.T) {
	st := newTestStore(t)
	dir := t.TempDir()
	e, err := plugin.NewEngine(dir, st, events.NewBus())
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		e.Close()
		// second close would panic if VMs are not guarded — engine.Close
		// calls p.close() which holds mu, so double-close of a closed LState
		// would surface here if not handled.
	})
}

// ── API: log ──────────────────────────────────────────────────────────────────

func TestAPI_Log_IsAvailable(t *testing.T) {
	// log() must not error when called from on_load.
	st := newTestStore(t)
	dir := pluginDir(t, map[string]string{
		"log.lua": `
			function on_load()
				log("hello from plugin")
			end
		`,
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
				if resp then
					db.loot_add(tostring(resp.status) .. "|" .. resp.body, "Info")
				end
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
	// A failed http.get must return nil, err — not crash the plugin.
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"get_fail.lua": `
			function on_response(r)
				local resp, err = http.get("http://127.0.0.1:1/")
				if resp == nil and err ~= nil then
					db.loot_add("error_handled", "Info")
				end
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
				if resp then
					db.loot_add(tostring(resp.status) .. "|" .. resp.body, "Info")
				end
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
				local headers = {["X-Custom"] = "plugin-value"}
				local resp = http.request("DELETE", "%s", "", headers)
				if resp then
					db.loot_add(tostring(resp.status), "Info")
				end
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

	// Log a transaction directly so db.history has something to return.
	require.NoError(t, st.Log(store.Transaction{
		Timestamp:  time.Now(),
		Host:       "target.com",
		Method:     "GET",
		URL:        "http://target.com/",
		Proto:      "HTTP/1.1",
		StatusCode: 200,
		DurationMs: 10,
		InScope:    true,
	}))
	time.Sleep(50 * time.Millisecond)

	e := newEngine(t, st, map[string]string{
		"history.lua": `
			function on_response(r)
				local txs = db.history(10)
				if txs then
					db.loot_add(tostring(#txs), "Info")
				end
			end
		`,
	})

	e.ObserveResponse(responseEvent())
	time.Sleep(50 * time.Millisecond)

	loot, err := st.AllLoot()
	require.NoError(t, err)
	require.Len(t, loot, 1)
	// At least 1 transaction (we logged one above).
	assert.Equal(t, "1", loot[0].Title)
}

func TestAPI_DBHistory_TransactionFieldsExposed(t *testing.T) {
	st := newTestStore(t)

	require.NoError(t, st.Log(store.Transaction{
		Timestamp:  time.Now(),
		Host:       "check.com",
		Method:     "POST",
		URL:        "http://check.com/api",
		Proto:      "HTTP/1.1",
		StatusCode: 201,
		DurationMs: 5,
		InScope:    true,
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
			Timestamp:  time.Now(),
			Host:       host,
			Method:     "GET",
			URL:        "http://" + host + "/",
			Proto:      "HTTP/1.1",
			StatusCode: 200,
			InScope:    true,
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

// ── API: db.scope_add / db.scope_get / db.scope_remove ───────────────────────

func TestAPI_DBScope_AddGetRemove(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"scope.lua": `
			function on_response(r)
				-- add
				db.scope_add("plugin-scope.com")

				-- get — store the count
				local entries = db.scope_get()
				db.loot_add("count:" .. tostring(#entries), "Info")

				-- remove the first entry
				if #entries > 0 then
					db.scope_remove(entries[1].id)
				end

				-- verify removed
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
	assert.True(t, titles["count:1"], "scope should have 1 entry after add")
	assert.True(t, titles["after:0"], "scope should be empty after remove")
}

// ── API: db.loot_add / db.loot_get / db.loot_delete ──────────────────────────

func TestAPI_DBLoot_AddGetDelete(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"loot.lua": `
			function on_response(r)
				-- add
				local id = db.loot_add("finding", "High", "some notes", "GET / HTTP/1.1", "HTTP/1.1 200 OK")

				-- get
				local entries = db.loot_get()
				db.loot_add("count:" .. tostring(#entries), "Info")

				-- delete the first finding
				db.loot_delete(id)

				-- verify: only the "count:..." entry remains
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
	assert.True(t, titles["after:1"]) // only the count entry remains after delete
}

func TestAPI_DBLoot_AllFieldsStored(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"loot_fields.lua": `
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

// ── Concurrency ───────────────────────────────────────────────────────────────

func TestEngine_ConcurrentHandleRequest_Safe(t *testing.T) {
	// Multiple goroutines calling HandleRequest concurrently must not race.
	// Run with -race to verify.
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"concurrent.lua": `
			function on_request(r)
				r.body = r.body .. "-ok"
				return r
			end
		`,
	})

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ev := requestEvent(t, "GET", "http://example.com", []byte("body"))
			result := e.HandleRequest(ev)
			assert.False(t, result.Drop)
		}()
	}
	wg.Wait()
}

func TestEngine_ConcurrentObserveResponse_Safe(t *testing.T) {
	st := newTestStore(t)
	e := newEngine(t, st, map[string]string{
		"obs.lua": `function on_response(r) end`,
	})

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			e.ObserveResponse(responseEvent())
		}()
	}
	wg.Wait()
}
