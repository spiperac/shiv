# Loot

The Loot tab is a findings tracker. When you find something worth documenting during a test, you save it here with the relevant request and response attached.

## Adding an entry

Click **Add** to open the entry dialog. Fill in:

- **Title** — a short name for the finding
- **URL** — the affected endpoint
- **Severity** — Critical, High, Medium, Low, or Info
- **Notes** — a free-text field for your write-up, reproducing steps, or anything else. Supports Markdown.

You can also send a request directly from the History or Repeater tab using the Loot button. This pre-fills the raw request and response fields so the evidence is captured automatically.

## Managing entries

Entries are listed in a table with title, URL, and severity. Clicking a row expands the detail view showing notes and the raw request/response.

Right-clicking an entry gives options to edit it or delete it.

## Export

The **Export** button writes all loot entries to a Markdown file. The output includes each finding's title, severity, URL, notes, and the raw request and response in fenced code blocks. The format is clean enough to paste directly into a report or a Git repository.
