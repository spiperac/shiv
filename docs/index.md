# Shiv

Shiv is a desktop MITM proxy for HTTP/HTTPS traffic inspection and web security testing. It runs natively on Linux, macOS, and Windows — written in Go with a Fyne UI, storing everything in a local SQLite database.

The proxy listens on `127.0.0.1:9090` and handles plain HTTP, HTTPS via MITM (using a locally generated CA), HTTP/2, and WebSockets. All traffic passing through is recorded to disk and available in the History tab for the lifetime of the project.

## Features at a glance

- Full traffic capture with filtering, search, and per-row annotations
- Request intercept with in-flight editing
- Repeater for manual request replay, with per-tab send history and WebSocket support
- Intruder for automated payload injection
- Loot tab for tracking findings, with Markdown export
- Decoder utility covering Base64, URL encoding, Hex, HTML entities, JSON, JWT, and common hashes
- Scope control to limit which hosts are actively intercepted
- Lua plugin API for scripting against the request/response pipeline

## Where to start

If you are setting Shiv up for the first time, go to [Getting Started](getting-started.md).
