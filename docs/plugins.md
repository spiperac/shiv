# Plugins

Shiv supports Lua plugins that hook into the request and response pipeline. Plugins are loaded from the `plugins/` subdirectory of your Shiv data directory. Any `.lua` file placed there is loaded automatically on startup.

You can also load a plugin at runtime from the Plugins tab without restarting.

## Plugin lifecycle

When a plugin is loaded, its top-level code runs first. If the plugin defines an `on_load` function, it is called immediately after. Plugins can be enabled or disabled from the Plugins tab without reloading the application.

Each plugin runs in its own isolated Lua VM. Plugins do not share state with each other.

Hook calls have a 500ms timeout. If a hook takes longer than that it is interrupted and an error is logged.

## Hooks

### on_load()

Called once when the plugin is first loaded. Use it for initialization.

```lua
function on_load()
    log("my plugin loaded")
end
```

### on_request(req)

Called for every HTTP request before it is forwarded to the server. The `req` table contains:

| Field | Type | Description |
|---|---|---|
| `method` | string | HTTP method |
| `url` | string | Full URL |
| `headers` | table | Request headers |
| `body` | string | Request body |
| `drop` | boolean | Set to `true` to drop the request |

Return the modified table to apply changes. Fields you do not modify are left as-is.

```lua
function on_request(req)
    -- add a header to every request
    req.headers["X-Proxy"] = "shiv"
    return req
end
```

To drop a request:

```lua
function on_request(req)
    if req.url:find("analytics") then
        req.drop = true
        return req
    end
    return req
end
```

### on_response(resp)

Called for every HTTP response after it is received from the server. The `resp` table contains:

| Field | Type | Description |
|---|---|---|
| `host` | string | Bare hostname |
| `method` | string | Request method |
| `url` | string | Request URL |
| `proto` | string | Protocol (HTTP/1.1, HTTP/2) |
| `status` | number | HTTP status code |
| `duration_ms` | number | Round-trip time in milliseconds |
| `tls` | boolean | Whether the connection was TLS |
| `req_headers` | table | Request headers |
| `req_body` | string | Request body |
| `resp_headers` | table | Response headers |
| `resp_body` | string | Response body |

The return value of `on_response` is ignored. This hook is read-only.

```lua
function on_response(resp)
    if resp.status == 500 then
        log("server error on " .. resp.url)
        db.loot_add("500 Error", "Low", resp.url, "", "")
    end
end
```

### on_websocket_frame(frame)

Called for every WebSocket frame in either direction. The `frame` table contains:

| Field | Type | Description |
|---|---|---|
| `connection_id` | number | Unique ID for the WebSocket connection |
| `direction` | number | 0 = client to server, 1 = server to client |
| `opcode` | number | WebSocket opcode (1 = text, 2 = binary, etc.) |
| `payload` | string | Frame payload |

Return the modified table to change the payload. Setting `frame.payload` to a different value rewrites the frame on the wire.

```lua
function on_websocket_frame(frame)
    log("frame: " .. frame.payload)
    return frame
end
```

## Built-in API

### log(message)

Writes a line to the plugin log visible in the Plugins tab.

```lua
log("something happened")
```

### http

Makes outbound HTTP requests from within a plugin. Note that these requests do not go through the Shiv proxy — they go directly to the target.

```lua
-- GET
local resp, err = http.get("https://example.com")
if err then
    log("error: " .. err)
else
    log("status: " .. resp.status)
    log("body: " .. resp.body)
end

-- POST
local resp, err = http.post("https://example.com/api", '{"key":"value"}', "application/json")

-- Custom request
local resp, err = http.request("PUT", "https://example.com/resource", "body", {
    ["Authorization"] = "Bearer token"
})
```

All three functions return a response table with `status` (number), `body` (string), and `headers` (table), or `nil` plus an error string on failure.

### db

Provides read and write access to the project database.

```lua
-- Read history (up to 100 most recent)
local txs, err = db.history(100)
for i, tx in ipairs(txs) do
    log(tx.method .. " " .. tx.url .. " -> " .. tx.status)
end

-- Filter history by host
local txs, err = db.history_filter("example.com", "", 0)

-- Scope management
local entries, err = db.scope_get()
db.scope_add("example.com")
db.scope_remove(entry_id)

-- Loot management
local entries, err = db.loot_get()
local id, err = db.loot_add("Title", "High", "Notes", raw_request, raw_response)
db.loot_delete(id)
```

Each `db` function returns its result on success, or `nil` plus an error string on failure.

## Example plugin

This plugin logs any response with a `Set-Cookie` header and automatically saves it to Loot if the cookie is missing the `HttpOnly` flag:

```lua
function on_load()
    log("cookie checker loaded")
end

function on_response(resp)
    local cookie = resp.resp_headers["Set-Cookie"]
    if not cookie then return end

    if not cookie:find("HttpOnly") then
        local notes = "Set-Cookie header missing HttpOnly flag:\n" .. cookie
        db.loot_add("Missing HttpOnly", "Low", notes, "", "")
        log("flagged missing HttpOnly on " .. resp.host)
    end
end
```
