package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// ExportHAR exports the current history to a HAR file, respecting the
// current showOutScope state from the history tab.
// Wire this to a button in history.go: h.exportHAR()
func (h *historyTab) exportHAR() {
	h.mu.RLock()
	rows := make([]store.Transaction, len(h.rows))
	copy(rows, h.rows)
	showOut := h.showOutScope.Checked
	h.mu.RUnlock()

	// Apply the same scope filter the table uses.
	var txs []store.Transaction
	for _, tx := range rows {
		if !showOut && !tx.InScope {
			continue
		}
		txs = append(txs, tx)
	}

	if len(txs) == 0 {
		dialog.ShowInformation("Export HAR", "No transactions to export.", h.win)
		return
	}

	// Fetch full bodies for each transaction — history rows omit bodies
	// for performance, same as the repeater send-to pattern.
	go func() {
		full := make([]store.Transaction, 0, len(txs))
		for _, tx := range txs {
			fullTx, err := h.projectStore.GetTransaction(tx.ID)
			if err != nil {
				logger.Error("har: get transaction %d: %v", tx.ID, err)
				// Include the body-less version rather than silently dropping it.
				full = append(full, tx)
				continue
			}
			full = append(full, *fullTx)
		}

		data, err := store.ExportHAR(full)
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("HAR export failed: %w", err), h.win)
			})
			return
		}

		fyne.Do(func() {
			saveDialog := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
				if err != nil || wc == nil {
					return
				}
				defer wc.Close()
				if _, err := wc.Write(data); err != nil {
					logger.Error("har: write: %v", err)
					dialog.ShowError(err, h.win)
				}
			}, h.win)
			saveDialog.SetFileName(fmt.Sprintf("shiv-%s.har", time.Now().Format("2006-01-02-150405")))
			saveDialog.Show()
		})
	}()
}
