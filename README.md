# Shiv

<img src="assets/logo.png" width="64"/>

Shiv is a desktop MITM proxy for HTTP/HTTPS traffic inspection and web security testing. It runs as a native application on Linux, macOS, and Windows — built in Go with a Fyne UI and SQLite for persistent storage.

## What it does

Shiv sits between your browser and the internet, letting you see and modify every request and response. The proxy listens on `127.0.0.1:9090` and handles plain HTTP, HTTPS (via a locally generated CA), HTTP/2, and WebSockets.

**History** captures all traffic passing through the proxy with full request and response bodies. You can filter by host, method, status code, content type, and search the body content. Rows can be annotated with comments and color highlights.

**Intercept** pauses requests or responses in-flight so you can edit them before they continue.

**Repeater** lets you take any captured request, edit it, and resend it as many times as you want. It maintains a send history per tab and has a separate mode for WebSocket connections.

**Intruder** runs automated payload substitution across a request. Mark injection points, load a wordlist, and let it run.

**Decoder** is a quick utility for encoding and hashing: URL encode/decode, Base64, Hex, HTML entities, JSON pretty-print, JWT inspection, MD5/SHA1/SHA256.

**Loot** is a findings tracker where you can save notable requests along with notes and export everything as Markdown.

**Scope** controls which hosts the proxy actively intercepts. Everything else passes through unmodified.

**Lua plugins** can hook into the request/response pipeline to modify, drop, or log traffic programmatically.

## Installation

Download the latest release for your platform from the [releases page](https://github.com/spiperac/shiv/releases).

On Linux, an AppImage is available. On macOS, a universal binary (arm64 + amd64) is packaged as a `.app`. On Windows, a standalone `.exe` is provided.

To build from source you need Go 1.22 and the platform graphics dependencies (on Linux: `libgl1-mesa-dev` and `xorg-dev`):

```
go build -o shiv .
./shiv
```

Or with Make:

```
make build
make install   # installs to ~/.local/bin and registers a .desktop entry
```

## Setup

The first time you run Shiv, it generates a local CA certificate. Go to Settings and click "Install CA Certificate" to trust it. Then configure your browser to use `127.0.0.1:9090` as its HTTP proxy. Shiv intercepts HTTPS by issuing certificates signed by that CA on the fly.

## Running

```
./shiv
./shiv -verbose
```

## Building

The Nix flake in the repo provides a reproducible dev environment with all native dependencies. Inside `nix develop`, all standard Go tooling works as expected.

For packaged builds targeting release:

```
make appimage   # Linux AppImage
fyne package    # platform-native package via fyne CLI
```

## License

MIT
