# Shiv

<img src="assets/logo.png" width="64"/> 

A desktop HTTP/HTTPS interception proxy for web security testing.

## Features

- HTTP/HTTPS interception with MITM
- Request intercept & edit
- History with filtering and scope
- Repeater with raw request editing
- Loot/findings tracker with markdown export
- Auto-generated local CA with browser trust store installation

## Setup

Install the CA certificate via Settings → Install CA Certificate, then configure your browser to use `127.0.0.1:9090` as HTTP proxy.

## Usage

```
./shiv
./shiv -verbose
```

## Requirements

- Go 1.22+
- Linux/macOS/Windows
```

### 
