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
)

func showInspectorDialog(tx store.Transaction, win fyne.Window) {
	tabs := container.NewAppTabs()

	// Cookies tab
	cookies := parseCookies(tx.RespHeaders)
	cookieContent := buildKVTable(cookies)
	tabs.Append(container.NewTabItem("Cookies", cookieContent))

	// Headers tab
	headers := buildHeadersList(tx.RespHeaders)
	tabs.Append(container.NewTabItem("Headers", headers))

	// JSON tab if applicable
	ct := tx.RespHeaders.Get("Content-Type")
	if strings.Contains(ct, "application/json") && len(tx.RespBody) > 0 {
		jsonEntry := newReadOnlyEntry()
		jsonEntry.SetText(string(prettyJSON(tx.RespBody)))
		tabs.Append(container.NewTabItem("JSON", container.NewScroll(jsonEntry)))
	}

	d := dialog.NewCustom("Inspector", "Close", tabs, win)
	d.Resize(fyne.NewSize(600, 400))
	d.Show()
}

func parseCookies(headers http.Header) [][2]string {
	var result [][2]string
	for _, line := range headers["Set-Cookie"] {
		// only take the name=value part before first ;
		parts := strings.SplitN(line, ";", 2)
		kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
		if len(kv) == 2 {
			result = append(result, [2]string{kv[0], kv[1]})
		} else if len(kv) == 1 {
			result = append(result, [2]string{kv[0], ""})
		}
		// also parse attributes
		if len(parts) > 1 {
			for _, attr := range strings.Split(parts[1], ";") {
				attr = strings.TrimSpace(attr)
				if attr == "" {
					continue
				}
				av := strings.SplitN(attr, "=", 2)
				if len(av) == 2 {
					result = append(result, [2]string{"  " + av[0], av[1]})
				} else {
					result = append(result, [2]string{"  " + av[0], ""})
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
