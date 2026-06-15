# History

The History tab shows every HTTP request and response that has passed through the proxy. Entries are persisted to the project database, so they survive restarts.

## Browsing traffic

The left panel shows a site map tree. Clicking a host filters the table to that host only. The **All** button at the top of the site map clears the filter and shows all traffic. Right-clicking a host in the site map gives options to delete all traffic from that host or remove it from history entirely.

The main table shows one row per transaction. Columns include method, URL path, status code, content type, response size, and duration. Clicking a row loads the full request and response in the detail pane on the right.

## Filtering

The filter bar above the table lets you narrow results by:

- **Method** — GET, POST, etc., or All
- **Status** — a specific code (e.g. 200), a range (e.g. 200-299), or left blank for all
- **Content type** — text, json, html, etc.
- **Search** — searches request and response bodies, URLs, and headers
- **Hide static** — hides common static asset extensions (images, fonts, scripts, stylesheets)
- **In scope only** — restricts the view to hosts that are in your defined scope

## Request and response detail

Selecting a row shows the raw request on the left and the raw response on the right. The Inspector button opens a structured view of headers and a formatted body. Copy buttons are available on each pane. The response can be saved to disk via the Save button.

## Annotations

Right-clicking a row in the history table opens a context menu with annotation options. You can add a text comment to a row or apply a colour highlight (red, orange, green, blue, purple). Annotations are stored in the database and visible as background colours in the table and a comment column. Clearing an annotation removes both the comment and the colour.

## Sending to other tools

From the right-click context menu you can send any captured request to the Repeater for manual editing and replay, or to the Intruder for payload injection.
