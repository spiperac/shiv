package plugin

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// Engine loads all Lua plugins from a directory and implements
// events.RequestMiddleware, events.ResponseObserver,
// events.WebSocketFrameObserver, and events.PluginEnabledObserver
// so it can be registered directly on the bus.
type Engine struct {
	plugins []*Plugin
	bus     *events.Bus
	dir     string
	st      *store.Store
}

// NewEngine loads all *.lua files from dir, registers each in the store,
// restores persisted enabled state, and returns a ready Engine.
// Plugins that fail to load are skipped.
func NewEngine(dir string, st *store.Store, bus *events.Bus) (*Engine, error) {
	plugins, err := loadDir(dir, st)
	if err != nil {
		return nil, err
	}

	e := &Engine{plugins: plugins, bus: bus, dir: dir, st: st}

	for _, p := range plugins {
		// Persist plugin record; preserves existing enabled state on conflict.
		if err := st.RegisterPlugin(p.name, p.path); err != nil {
			logger.Error("plugin: register %s: %v", p.name, err)
		}
		// Restore enabled state from previous session.
		if enabled, err := st.PluginEnabled(p.name); err == nil {
			p.enabled.Store(enabled)
		}
		// Drain any log() calls made during on_load so they reach the UI.
		e.emitLogs(p)
	}

	active := make([]string, 0, len(plugins))
	for _, p := range plugins {
		active = append(active, p.name)
	}
	if err := st.ScanPlugins(active); err != nil {
		logger.Error("plugin: scan: %v", err)
	}

	return e, nil
}

// emitLogs drains a plugin's log buffer and emits each line onto the bus.
// Called after every hook invocation, including on error, so no log line
// is ever lost.
func (e *Engine) emitLogs(p *Plugin) {
	for _, line := range p.drainLogs() {
		e.bus.EmitPluginLog(events.PluginLogEvent{Name: p.name, Message: line})
	}
}

// HandleRequest implements events.RequestMiddleware.
// Each plugin's on_request(req) hook is called in load order. Table
// construction and the Lua call happen in a single locked section via
// callWithBuilder — no VM access escapes the lock at any point.
// A plugin may modify method, url, headers, body, or set drop=true.
// The first drop short-circuits all remaining plugins.
func (e *Engine) HandleRequest(ev events.RequestEvent) events.RequestResult {
	req := ev.Request
	body := ev.Body

	if req == nil || req.URL == nil {
		return events.RequestResult{Drop: false, Request: req, Body: body}
	}

	for _, p := range e.plugins {
		if !p.enabled.Load() {
			continue
		}
		if !p.has("on_request") {
			continue
		}

		// Snapshot Go values before entering the lock so the builder closure
		// captures plain Go strings, not live request fields that could race.
		method := req.Method
		rawURL := req.URL.String()
		bodySnap := string(body)
		headers := req.Header

		ret, err := p.callWithBuilder("on_request", func(L *lua.LState) *lua.LTable {
			tbl := L.NewTable()
			L.SetField(tbl, "method", lua.LString(method))
			L.SetField(tbl, "url", lua.LString(rawURL))
			L.SetField(tbl, "body", lua.LString(bodySnap))
			L.SetField(tbl, "drop", lua.LFalse)

			hTbl := L.NewTable()
			for k, vals := range headers {
				L.SetField(hTbl, k, lua.LString(strings.Join(vals, ", ")))
			}
			L.SetField(tbl, "headers", hTbl)
			return tbl
		})
		e.emitLogs(p)
		if err != nil {
			logger.Error("plugin %s: on_request: %v", p.name, err)
			continue
		}
		if ret == nil {
			continue
		}

		// Read back modifications. ret is a Lua table owned by p.L —
		// mu must be held while reading it.
		p.mu.Lock()
		drop := false
		if d, ok := ret.RawGetString("drop").(lua.LBool); ok {
			drop = bool(d)
		}
		if !drop {
			if m, ok := ret.RawGetString("method").(lua.LString); ok && string(m) != "" {
				req.Method = string(m)
			}
			if u, ok := ret.RawGetString("url").(lua.LString); ok && string(u) != "" {
				if parsed, err := req.URL.Parse(string(u)); err == nil {
					req.URL = parsed
				}
			}
			if b, ok := ret.RawGetString("body").(lua.LString); ok {
				body = []byte(b)
			}
			if hTbl, ok := ret.RawGetString("headers").(*lua.LTable); ok {
				newHeaders := make(http.Header)
				hTbl.ForEach(func(k, v lua.LValue) {
					newHeaders.Set(k.String(), v.String())
				})
				req.Header = newHeaders
			}
		}
		p.mu.Unlock()

		if drop {
			return events.RequestResult{Drop: true, Request: req, Body: body}
		}
	}

	return events.RequestResult{Drop: false, Request: req, Body: body}
}

// ObserveResponse implements events.ResponseObserver.
// Each plugin's on_response(resp) hook is called in load order. Table
// construction and the Lua call happen in a single locked section via
// callWithBuilder — no VM access escapes the lock at any point.
// The hook is read-only — return value is ignored.
func (e *Engine) ObserveResponse(ev events.ResponseEvent) {
	// Snapshot all event fields once before the plugin loop. Each plugin
	// call captures these plain Go values in its builder closure — no live
	// event fields are accessed inside the lock.
	host := ev.Host
	method := ev.Method
	url := ev.URL
	proto := ev.Proto
	status := ev.StatusCode
	durationMs := ev.DurationMs
	tls := ev.TLS
	reqBody := string(ev.ReqBody)
	respBody := string(ev.RespBody)
	reqHeaders := ev.ReqHeaders
	respHeaders := ev.RespHeaders

	for _, p := range e.plugins {
		if !p.enabled.Load() {
			continue
		}
		if !p.has("on_response") {
			continue
		}

		_, err := p.callWithBuilder("on_response", func(L *lua.LState) *lua.LTable {
			tbl := L.NewTable()
			L.SetField(tbl, "host", lua.LString(host))
			L.SetField(tbl, "method", lua.LString(method))
			L.SetField(tbl, "url", lua.LString(url))
			L.SetField(tbl, "proto", lua.LString(proto))
			L.SetField(tbl, "status", lua.LNumber(status))
			L.SetField(tbl, "duration_ms", lua.LNumber(durationMs))
			L.SetField(tbl, "tls", lua.LBool(tls))
			L.SetField(tbl, "req_body", lua.LString(reqBody))
			L.SetField(tbl, "resp_body", lua.LString(respBody))

			rqH := L.NewTable()
			for k, vals := range reqHeaders {
				L.SetField(rqH, k, lua.LString(strings.Join(vals, ", ")))
			}
			L.SetField(tbl, "req_headers", rqH)

			rpH := L.NewTable()
			for k, vals := range respHeaders {
				L.SetField(rpH, k, lua.LString(strings.Join(vals, ", ")))
			}
			L.SetField(tbl, "resp_headers", rpH)
			return tbl
		})
		e.emitLogs(p)
		if err != nil {
			logger.Error("plugin %s: on_response: %v", p.name, err)
		}
	}
}

// ObserveWebSocketFrame implements events.WebSocketFrameObserver.
// Each plugin's on_websocket_frame(frame) hook is called in load order.
// The hook may modify frame.payload — the last modification wins.
// Plugins cannot drop frames.
func (e *Engine) ObserveWebSocketFrame(ev events.WebSocketFrameEvent) events.WebSocketFrameResult {
	payload := ev.Payload

	// Snapshot Go values before the plugin loop.
	connID := ev.ConnectionID
	direction := int(ev.Direction)
	opcode := int(ev.Opcode)
	payloadSnap := string(ev.Payload)

	for _, p := range e.plugins {
		if !p.enabled.Load() {
			continue
		}
		if !p.has("on_websocket_frame") {
			continue
		}

		ret, err := p.callWithBuilder("on_websocket_frame", func(L *lua.LState) *lua.LTable {
			tbl := L.NewTable()
			L.SetField(tbl, "connection_id", lua.LNumber(connID))
			L.SetField(tbl, "direction", lua.LNumber(direction))
			L.SetField(tbl, "opcode", lua.LNumber(opcode))
			L.SetField(tbl, "payload", lua.LString(payloadSnap))
			return tbl
		})
		e.emitLogs(p)
		if err != nil {
			logger.Error("plugin %s: on_websocket_frame: %v", p.name, err)
			continue
		}
		if ret == nil {
			continue
		}

		p.mu.Lock()
		if b, ok := ret.RawGetString("payload").(lua.LString); ok {
			payload = []byte(b)
			payloadSnap = string(payload)
		}
		p.mu.Unlock()
	}

	return events.WebSocketFrameResult{Payload: payload}
}

// ObservePluginEnabled implements events.PluginEnabledObserver.
// Toggles the in-memory enabled flag for the named plugin so the change
// takes effect immediately for all subsequent hook calls.
func (e *Engine) ObservePluginEnabled(ev events.SetPluginEnabledEvent) {
	for _, p := range e.plugins {
		if p.name == ev.Name {
			p.enabled.Store(ev.Enabled)
			return
		}
	}
}

// ObserveLoadPlugin implements events.LoadPluginObserver.
// Copies the selected file into the plugins directory, loads it, registers
// it in the store, and appends it to the engine's plugin slice so it
// participates in all subsequent hook calls.
// Safe without a mutex — the bus is synchronous so this and all hook
// iteration methods are always called sequentially.
func (e *Engine) ObserveLoadPlugin(ev events.LoadPluginEvent) {
	filename := filepath.Base(ev.SourcePath)
	destPath := filepath.Join(e.dir, filename)

	if err := copyFile(ev.SourcePath, destPath); err != nil {
		logger.Error("plugin: import %s: %v", filename, err)
		return
	}

	p, err := load(destPath, e.st)
	if err != nil {
		logger.Error("plugin: load %s: %v", filename, err)
		return
	}

	if err := e.st.RegisterPlugin(p.name, p.path); err != nil {
		logger.Error("plugin: register %s: %v", p.name, err)
	}

	e.emitLogs(p)
	e.plugins = append(e.plugins, p)
}

// copyFile copies src to dst, creating the destination directory if needed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return out.Sync()
}

// Close shuts down all plugin VMs.
func (e *Engine) Close() {
	for _, p := range e.plugins {
		p.close()
	}
}
