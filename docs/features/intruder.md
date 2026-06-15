# Intruder

The Intruder runs automated requests against a target by substituting payloads into marked positions in a raw HTTP request.

## Setup

Paste a raw HTTP request into the editor. Mark the positions you want to fuzz by wrapping them with `§` characters. For example:

```
POST /login HTTP/1.1
Host: example.com
Content-Type: application/x-www-form-urlencoded

username=§admin§&password=§password§
```

Each `§`-wrapped section is an injection point. Intruder substitutes each payload into all marked positions simultaneously (sniper mode — one payload list, all positions get the same value on each request).

## Payloads

Paste a wordlist into the payload box, one entry per line. When you click **Start**, Intruder sends one request per payload entry, replacing all marked positions with the current payload.

## Results

Results appear in the table as requests complete. Columns show the payload, status code, response length, and duration. Clicking a row shows the full response in the detail pane.

You can sort by any column. Responses that stand out by length or status code are usually the interesting ones.

Completed results can be sent to the Repeater for closer inspection via the right-click context menu.

## Controls

**Start** begins the attack. **Stop** halts it mid-run. Running a new attack clears the previous results.
