package ui

import (
	"net/http"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

func showInspectorDialog(tx store.Transaction, win fyne.Window) {
	tabs := container.NewAppTabs()

	tabs.Append(container.NewTabItem("Headers", buildHeadersList(tx.RespHeaders)))

	cookies := parseCookies(tx.RespHeaders)
	tabs.Append(container.NewTabItem("Cookies", buildKVTable(cookies)))

	contentType := tx.RespHeaders.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && len(tx.RespBody) > 0 {
		body := tx.RespBody
		if idx := strings.Index(string(body), "\r\n\r\n"); idx >= 0 {
			body = body[idx+4:]
		} else if idx := strings.Index(string(body), "\n\n"); idx >= 0 {
			body = body[idx+2:]
		}
		jsonEntry := widgets.NewTextView()
		jsonEntry.SetText(string(PrettyJSON(body)))
		tabs.Append(container.NewTabItem("JSON", jsonEntry.Build()))
	}

	d := dialog.NewCustom("Inspector", "Close", tabs, win)
	d.Resize(fyne.NewSize(600, 400))
	closeOnEscape(win, d.Dismiss)
	d.Show()
}

func parseCookies(headers http.Header) [][2]string {
	var result [][2]string
	for _, line := range headers["Set-Cookie"] {
		parts := strings.SplitN(line, ";", 2)
		cookieParts := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
		if len(cookieParts) == 2 {
			result = append(result, [2]string{cookieParts[0], cookieParts[1]})
		} else if len(cookieParts) == 1 {
			result = append(result, [2]string{cookieParts[0], ""})
		}
		if len(parts) > 1 {
			for attr := range strings.SplitSeq(parts[1], ";") {
				attr = strings.TrimSpace(attr)
				if attr == "" {
					continue
				}
				attrParts := strings.SplitN(attr, "=", 2)
				if len(attrParts) == 2 {
					result = append(result, [2]string{"  " + attrParts[0], attrParts[1]})
				} else {
					result = append(result, [2]string{"  " + attrParts[0], ""})
				}
			}
		}
	}
	return result
}

func buildHeadersList(headers http.Header) fyne.CanvasObject {
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs [][2]string
	for _, k := range keys {
		for _, v := range headers[k] {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	return buildKVTable(pairs)
}

func buildKVTable(pairs [][2]string) fyne.CanvasObject {
	if len(pairs) == 0 {
		l := widget.NewLabel("None")
		l.Importance = widget.LowImportance
		return container.NewCenter(l)
	}
	t := widget.NewTable(
		func() (int, int) { return len(pairs), 2 },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			l := obj.(*widget.Label)
			if id.Col == 0 {
				l.TextStyle = fyne.TextStyle{Bold: true}
				l.SetText(pairs[id.Row][0])
			} else {
				l.TextStyle = fyne.TextStyle{}
				l.SetText(pairs[id.Row][1])
			}
		},
	)
	t.SetColumnWidth(0, 200)
	t.SetColumnWidth(1, 350)
	return t
}
