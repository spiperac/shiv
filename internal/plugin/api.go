package plugin

import (
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// registerAPI registers all Lua API namespaces on the given VM.
// Called once per plugin at load time.
func registerAPI(L *lua.LState, st *store.Store) {
	registerLog(L)
	registerHTTPClient(L)
	registerDB(L, st)
}

// ── log ───────────────────────────────────────────────────────────────────────

func registerLog(L *lua.LState) {
	L.SetGlobal("log", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		logger.Info("[plugin] %s", msg)
		return 0
	}))
}

// ── http client ───────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 15 * time.Second}

func registerHTTPClient(L *lua.LState) {
	tbl := L.NewTable()

	// http.get(url) → {status, headers, body} or nil, err
	L.SetField(tbl, "get", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		resp, err := httpClient.Get(url)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer resp.Body.Close()
		return pushResponse(L, resp)
	}))

	// http.post(url, body, content_type) → {status, headers, body} or nil, err
	L.SetField(tbl, "post", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		body := L.CheckString(2)
		ct := L.OptString(3, "application/json")
		resp, err := httpClient.Post(url, ct, strings.NewReader(body))
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer resp.Body.Close()
		return pushResponse(L, resp)
	}))

	// http.request(method, url, body, headers_table) → {status, headers, body} or nil, err
	L.SetField(tbl, "request", L.NewFunction(func(L *lua.LState) int {
		method := L.CheckString(1)
		url := L.CheckString(2)
		body := L.OptString(3, "")
		headersTbl := L.OptTable(4, nil)

		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		if headersTbl != nil {
			headersTbl.ForEach(func(k, v lua.LValue) {
				req.Header.Set(k.String(), v.String())
			})
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		defer resp.Body.Close()
		return pushResponse(L, resp)
	}))

	L.SetGlobal("http", tbl)
}

func pushResponse(L *lua.LState, resp *http.Response) int {
	body, _ := io.ReadAll(resp.Body)

	tbl := L.NewTable()
	L.SetField(tbl, "status", lua.LNumber(resp.StatusCode))
	L.SetField(tbl, "body", lua.LString(body))

	headers := L.NewTable()
	for k, vals := range resp.Header {
		L.SetField(headers, k, lua.LString(strings.Join(vals, ", ")))
	}
	L.SetField(tbl, "headers", headers)

	L.Push(tbl)
	return 1
}

// ── db ────────────────────────────────────────────────────────────────────────

func registerDB(L *lua.LState, st *store.Store) {
	tbl := L.NewTable()

	// db.history(limit) → array of transaction tables
	L.SetField(tbl, "history", L.NewFunction(func(L *lua.LState) int {
		limit := L.OptInt(1, 100)
		txs, err := st.TransactionsPage(0, store.TransactionFilter{ShowOutScope: true})
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if limit > 0 && len(txs) > limit {
			txs = txs[:limit]
		}
		arr := L.NewTable()
		for i, tx := range txs {
			L.RawSetInt(arr, i+1, transactionToTable(L, tx))
		}
		L.Push(arr)
		return 1
	}))

	// db.history_filter(host, method, status) → array of transaction tables
	L.SetField(tbl, "history_filter", L.NewFunction(func(L *lua.LState) int {
		host := L.OptString(1, "")
		method := L.OptString(2, "")
		_ = L.OptInt(3, 0) // status filtering done client-side below

		filter := store.TransactionFilter{
			Host:         host,
			ShowOutScope: true,
		}
		if method != "" {
			filter.Search = method
		}

		txs, err := st.TransactionsPage(0, filter)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		arr := L.NewTable()
		idx := 1
		for _, tx := range txs {
			L.RawSetInt(arr, idx, transactionToTable(L, tx))
			idx++
		}
		L.Push(arr)
		return 1
	}))

	// db.scope_get() → array of {id, host}
	L.SetField(tbl, "scope_get", L.NewFunction(func(L *lua.LState) int {
		entries, err := st.AllScopeEntries()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		arr := L.NewTable()
		for i, e := range entries {
			row := L.NewTable()
			L.SetField(row, "id", lua.LNumber(e.ID))
			L.SetField(row, "host", lua.LString(e.Host))
			L.RawSetInt(arr, i+1, row)
		}
		L.Push(arr)
		return 1
	}))

	// db.scope_add(host) → true or nil, err
	L.SetField(tbl, "scope_add", L.NewFunction(func(L *lua.LState) int {
		host := L.CheckString(1)
		if err := st.AddScopeEntry(host); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// db.scope_remove(id) → true or nil, err
	L.SetField(tbl, "scope_remove", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckInt64(1)
		if err := st.DeleteScopeEntry(id); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	// db.loot_get() → array of loot tables
	L.SetField(tbl, "loot_get", L.NewFunction(func(L *lua.LState) int {
		entries, err := st.AllLoot()
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		arr := L.NewTable()
		for i, e := range entries {
			row := L.NewTable()
			L.SetField(row, "id", lua.LNumber(e.ID))
			L.SetField(row, "title", lua.LString(e.Title))
			L.SetField(row, "severity", lua.LString(e.Severity))
			L.SetField(row, "notes", lua.LString(e.Notes))
			L.SetField(row, "raw_request", lua.LString(e.RawRequest))
			L.SetField(row, "raw_response", lua.LString(e.RawResponse))
			L.RawSetInt(arr, i+1, row)
		}
		L.Push(arr)
		return 1
	}))

	// db.loot_add(title, severity, notes, raw_request, raw_response) → id or nil, err
	L.SetField(tbl, "loot_add", L.NewFunction(func(L *lua.LState) int {
		title := L.CheckString(1)
		severity := L.CheckString(2)
		notes := L.OptString(3, "")
		rawReq := L.OptString(4, "")
		rawResp := L.OptString(5, "")
		id, err := st.AddLoot(store.LootEntry{
			Title:       title,
			Severity:    severity,
			Notes:       notes,
			RawRequest:  rawReq,
			RawResponse: rawResp,
		})
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LNumber(id))
		return 1
	}))

	// db.loot_delete(id) → true or nil, err
	L.SetField(tbl, "loot_delete", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckInt64(1)
		if err := st.DeleteLoot(id); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		return 1
	}))

	L.SetGlobal("db", tbl)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func transactionToTable(L *lua.LState, tx store.Transaction) *lua.LTable {
	tbl := L.NewTable()
	L.SetField(tbl, "id", lua.LNumber(tx.ID))
	L.SetField(tbl, "host", lua.LString(tx.Host))
	L.SetField(tbl, "method", lua.LString(tx.Method))
	L.SetField(tbl, "url", lua.LString(tx.URL))
	L.SetField(tbl, "proto", lua.LString(tx.Proto))
	L.SetField(tbl, "status", lua.LNumber(tx.StatusCode))
	L.SetField(tbl, "duration_ms", lua.LNumber(tx.DurationMs))
	L.SetField(tbl, "tls", lua.LBool(tx.TLS))
	L.SetField(tbl, "in_scope", lua.LBool(tx.InScope))
	L.SetField(tbl, "req_body", lua.LString(tx.ReqBody))
	L.SetField(tbl, "resp_body", lua.LString(tx.RespBody))
	L.SetField(tbl, "timestamp", lua.LString(tx.Timestamp.UTC().Format(time.RFC3339)))

	reqH := L.NewTable()
	for k, vals := range tx.ReqHeaders {
		L.SetField(reqH, k, lua.LString(strings.Join(vals, ", ")))
	}
	L.SetField(tbl, "req_headers", reqH)

	respH := L.NewTable()
	for k, vals := range tx.RespHeaders {
		L.SetField(respH, k, lua.LString(strings.Join(vals, ", ")))
	}
	L.SetField(tbl, "resp_headers", respH)

	return tbl
}
