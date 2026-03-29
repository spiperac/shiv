package plugin

import (
	"net/http"
	"strings"

	lua "github.com/yuin/gopher-lua"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// Engine loads all Lua plugins from a directory and implements
// events.RequestMiddleware and events.ResponseObserver so it can be
// registered directly on the bus.
type Engine struct {
	plugins []*Plugin
}

// NewEngine loads all *.lua files from dir and returns a ready Engine.
// Plugins that fail to load are skipped.
func NewEngine(dir string, st *store.Store) (*Engine, error) {
	plugins, err := loadDir(dir, st)
	if err != nil {
		return nil, err
	}
	return &Engine{plugins: plugins}, nil
}

// HandleRequest implements events.RequestMiddleware.
// Each plugin's on_request(req) hook is called in load order under the
// plugin's mutex — Lua VMs are not goroutine-safe.
// A plugin may modify method, url, headers, body, or set drop=true.
// The first drop short-circuits all remaining plugins.
func (e *Engine) HandleRequest(ev events.RequestEvent) events.RequestResult {
	req := ev.Request
	body := ev.Body

	if req == nil || req.URL == nil {
		return events.RequestResult{Drop: false, Request: req, Body: body}
	}

	for _, p := range e.plugins {
		if !p.has("on_request") {
			continue
		}

		// Build the request table under the plugin mutex.
		p.mu.Lock()
		tbl := p.L.NewTable()
		p.L.SetField(tbl, "method", lua.LString(req.Method))
		p.L.SetField(tbl, "url", lua.LString(req.URL.String()))
		p.L.SetField(tbl, "body", lua.LString(body))
		p.L.SetField(tbl, "drop", lua.LFalse)

		hTbl := p.L.NewTable()
		for k, vals := range req.Header {
			p.L.SetField(hTbl, k, lua.LString(strings.Join(vals, ", ")))
		}
		p.L.SetField(tbl, "headers", hTbl)
		p.mu.Unlock()

		ret, err := p.callReturnsTable("on_request", tbl)
		if err != nil {
			logger.Error("plugin %s: on_request: %v", p.name, err)
			continue
		}
		if ret == nil {
			continue
		}

		// Read back modifications under the plugin mutex.
		p.mu.Lock()
		drop := false
		if d, ok := ret.RawGetString("drop").(lua.LBool); ok {
			drop = bool(d)
		}
		p.mu.Unlock()

		if drop {
			return events.RequestResult{Drop: true, Request: req, Body: body}
		}

		p.mu.Lock()
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
		p.mu.Unlock()
	}

	return events.RequestResult{Drop: false, Request: req, Body: body}
}

// ObserveResponse implements events.ResponseObserver.
// Each plugin's on_response(resp) hook is called in load order under the
// plugin's mutex. The hook is read-only — return value is ignored.
func (e *Engine) ObserveResponse(ev events.ResponseEvent) {
	for _, p := range e.plugins {
		if !p.has("on_response") {
			continue
		}

		// Build the response table under the plugin mutex.
		p.mu.Lock()
		tbl := p.L.NewTable()
		p.L.SetField(tbl, "host", lua.LString(ev.Host))
		p.L.SetField(tbl, "method", lua.LString(ev.Method))
		p.L.SetField(tbl, "url", lua.LString(ev.URL))
		p.L.SetField(tbl, "proto", lua.LString(ev.Proto))
		p.L.SetField(tbl, "status", lua.LNumber(ev.StatusCode))
		p.L.SetField(tbl, "duration_ms", lua.LNumber(ev.DurationMs))
		p.L.SetField(tbl, "tls", lua.LBool(ev.TLS))
		p.L.SetField(tbl, "req_body", lua.LString(ev.ReqBody))
		p.L.SetField(tbl, "resp_body", lua.LString(ev.RespBody))

		reqH := p.L.NewTable()
		for k, vals := range ev.ReqHeaders {
			p.L.SetField(reqH, k, lua.LString(strings.Join(vals, ", ")))
		}
		p.L.SetField(tbl, "req_headers", reqH)

		respH := p.L.NewTable()
		for k, vals := range ev.RespHeaders {
			p.L.SetField(respH, k, lua.LString(strings.Join(vals, ", ")))
		}
		p.L.SetField(tbl, "resp_headers", respH)
		p.mu.Unlock()

		if err := p.callNoReturn("on_response", tbl); err != nil {
			logger.Error("plugin %s: on_response: %v", p.name, err)
		}
	}
}

// Close shuts down all plugin VMs.
func (e *Engine) Close() {
	for _, p := range e.plugins {
		p.close()
	}
}
