# Intercept

The Intercept tab pauses HTTP requests or responses in-flight, letting you read and modify them before they continue to their destination.

## Enabling intercept

The toggle at the top of the tab turns interception on or off. When on, every request matching the current scope will pause and wait for you to act on it. Requests from out-of-scope hosts pass through without stopping.

## Editing a paused request

When a request is paused, its raw text appears in the editor. You can change anything — the method, path, headers, or body. The editor shows the raw HTTP format, so changes are applied exactly as written.

Once you are done editing, click **Forward** to send the request on to the server. Click **Drop** to discard it entirely — the browser will receive a connection error.

## Response intercept

You can also intercept responses on their way back to the browser. Enable response intercept with the second toggle. The workflow is the same: edit the raw response and forward or drop it.

## Notes

Intercept operates on HTTP/1.1 traffic. WebSocket frames are not paused here; the proxy forwards them bidirectionally in real time. If you need to inspect WebSocket traffic, use the WebSocket tab in the main window.

Requests pile up in a queue while intercept is enabled. If you navigate away from the tab while a request is paused, it will remain paused until you return and act on it. If you close the application, paused requests are dropped.
