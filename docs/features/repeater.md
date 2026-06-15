# Repeater

The Repeater lets you take a raw HTTP request, edit it, and send it as many times as you want. Each tab is independent and saved across sessions.

## HTTP tabs

Paste or edit a raw HTTP request in the left pane and click **Send**. The raw response appears in the right pane. The tab name updates automatically to reflect the method and path of the last sent request.

The host and TLS settings are inferred from the `Host` header and the request line scheme. If you paste a request captured from an HTTPS host that has no scheme in the request line (which is normal for HTTP/1.1), the tab uses the connection metadata from the original capture to determine whether to dial TLS.

**Clone** duplicates the current tab with the current request contents, so you can branch off a variation without losing your original.

The **Inspector** button (enabled after the first send) opens a structured view of the last request and response — headers parsed into a table and the body formatted if it is JSON.

The **Loot** button saves the last request and response to the Loot tab for documentation.

### Send history

Each tab keeps a ring buffer of the last 50 sends. The back and forward arrow buttons navigate through previous requests and responses within the tab, letting you compare results or replay an earlier version.

## WebSocket tabs

WebSocket tabs connect directly to a WebSocket endpoint. Enter the URL (`wss://` or `ws://`) and click **Connect**. Once connected the button changes to **Disconnect**.

The **Headers** button lets you add custom HTTP headers to the handshake request before connecting. This is useful for passing authentication tokens or setting origin headers.

Frames are shown in the top pane. Selecting a frame shows its full payload below. Sent frames are highlighted differently from received ones.

Type a message in the send bar at the bottom and click **Send** to push a text frame. The **Clear** button empties the frame list.

## Creating tabs

Tabs are created automatically when you send a request from the History tab via the right-click menu. You can also open a new tab manually using the + button in the tab bar. WebSocket tabs are opened from the WebSocket history tab via the right-click menu.

All tabs persist to the project database and reopen with their last request content when you restart Shiv.
