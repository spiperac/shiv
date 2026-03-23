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
	contentType := tx.RespHeaders.Get("Content-Type")
	if strings.Contains(contentType, "application/json") && len(tx.RespBody) > 0 {
		jsonEntry := newReadOnlyEntry()
		jsonEntry.SetText(string(prettyJSON(tx.RespBody)))
		tabs.Append(container.NewTabItem("JSON", container.NewScroll(jsonEntry)))
	}

	inspectorDialog := dialog.NewCustom("Inspector", "Close", tabs, win)
	inspectorDialog.Resize(fyne.NewSize(600, 400))
	closeOnEscape(win, inspectorDialog.Dismiss)
	inspectorDialog.Show()
}

func parseCookies(headers http.Header) [][2]string {
	var result [][2]string
	for _, line := range headers["Set-Cookie"] {
		// only take the name=value part before first ;
		parts := strings.SplitN(line, ";", 2)
		cookieParts := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
		if len(cookieParts) == 2 {
			result = append(result, [2]string{cookieParts[0], cookieParts[1]})
		} else if len(cookieParts) == 1 {
			result = append(result, [2]string{cookieParts[0], ""})
		}
		// also parse attributes
		if len(parts) > 1 {
			for _, attr := range strings.Split(parts[1], ";") {
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
	for headerKey := range headers {
		keys = append(keys, headerKey)
	}
	sort.Strings(keys)

	var pairs [][2]string
	for _, headerKey := range keys {
		for _, headerValue := range headers[headerKey] {
			pairs = append(pairs, [2]string{headerKey, headerValue})
		}
	}
	return buildKVTable(pairs)
}

func buildKVTable(pairs [][2]string) fyne.CanvasObject {
	if len(pairs) == 0 {
		emptyLabel := widget.NewLabel("None")
		emptyLabel.Importance = widget.LowImportance
		return container.NewCenter(emptyLabel)
	}

	kvTable := widget.NewTable(
		func() (int, int) { return len(pairs), 2 },
		func() fyne.CanvasObject {
			label := widget.NewLabel("")
			label.Truncation = fyne.TextTruncateEllipsis
			return label
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
	kvTable.SetColumnWidth(0, 200)
	kvTable.SetColumnWidth(1, 350)
	return kvTable
}
