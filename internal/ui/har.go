package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
)

// exportHAR exports all transactions matching the current filter to a HAR file.
// It paginates through the DB so all rows are exported, not just the visible page.
func (h *historyTab) exportHAR() {
	if len(h.displayed) == 0 {
		dialog.ShowInformation("Export HAR", "No transactions to export.", h.win)
		return
	}

	f := h.currentFilter()

	go func() {
		// Paginate through all matching rows from DB.
		var all []store.Transaction
		var cursor uint64
		for {
			page, err := h.projectStore.TransactionsPage(cursor, f)
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("HAR export failed: %w", err), h.win)
				})
				return
			}
			if len(page) == 0 {
				break
			}
			all = append(all, page...)
			if len(page) < store.PageSize {
				break
			}
			cursor = page[len(page)-1].ID
		}

		if len(all) == 0 {
			fyne.Do(func() {
				dialog.ShowInformation("Export HAR", "No transactions to export.", h.win)
			})
			return
		}

		// Fetch full bodies — page rows omit bodies for performance.
		full := make([]store.Transaction, 0, len(all))
		for _, tx := range all {
			fullTx, err := h.projectStore.GetTransaction(tx.ID)
			if err != nil {
				logger.Error("har: get transaction %d: %v", tx.ID, err)
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
