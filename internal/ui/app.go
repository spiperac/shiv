package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/shiv/internal/store"
)

// ShowMainWindow opens the main Shiv window. Closes the launch window after
// setting itself as master so the app stays alive.
func ShowMainWindow(app fyne.App, st *store.Store, launchWin fyne.Window) {
	w := app.NewWindow("Shiv")
	w.Resize(fyne.NewSize(1280, 800))
	w.SetMaster()

	historyTab := newHistoryTab(st, w)
	interceptTab := newInterceptTab(st)

	tabs := container.NewAppTabs(
		container.NewTabItem("History", historyTab),
		container.NewTabItem("Intercept", interceptTab),
		container.NewTabItem("Repeater", placeholderTab("Repeater — coming soon")),
		container.NewTabItem("Loot", placeholderTab("Loot — coming soon")),
		container.NewTabItem("Settings", placeholderTab("Settings — coming soon")),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	w.SetContent(tabs)
	w.Show()

	// Close launch window only after main window is shown and set as master.
	launchWin.Close()
}

func placeholderTab(msg string) fyne.CanvasObject {
	return container.NewCenter(newLabel(msg))
}
