# Getting Started

## Installation

Download the latest release for your platform from the [releases page](https://github.com/spiperac/shiv/releases).

- **Linux**: AppImage, run directly without installation
- **macOS**: universal `.app` bundle (arm64 + amd64), drag to Applications
- **Windows**: standalone `.exe`

To build from source instead, see [Building from Source](building.md).

## First run

On the first launch Shiv generates a local CA certificate and private key, stored under your OS user data directory. This CA is used to sign certificates for every HTTPS host the proxy intercepts.

## Trusting the CA certificate

Go to **Settings** and click **Install CA Certificate**. This copies the CA cert to your system trust store. On Linux it uses the system CA bundle. On macOS it adds the cert to the Keychain. On Windows it imports into the certificate store.

After installing, restart your browser to pick up the change.

If you prefer to install manually, the CA file path is shown in Settings. You can import it into Firefox's own certificate manager under Preferences > Privacy & Security > Certificates > View Certificates > Authorities > Import.

## Configuring the proxy

Point your browser (or any HTTP client) at `127.0.0.1:9090` as the HTTP proxy. Most browsers have a proxy setting under network or connection settings. On Linux you can also set `http_proxy` and `https_proxy` environment variables:

```
export http_proxy=http://127.0.0.1:9090
export https_proxy=http://127.0.0.1:9090
```

For tools like `curl`:

```
curl -x http://127.0.0.1:9090 https://example.com
```

## Verifying it works

With the proxy configured, open any HTTPS site in your browser. You should see it appear in the **History** tab within Shiv. If the browser shows a certificate error, the CA cert was not imported correctly or the browser needs to be restarted.
