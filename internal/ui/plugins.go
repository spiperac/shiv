package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/shiv/internal/events"
	"github.com/shiv/internal/logger"
	"github.com/shiv/internal/store"
	"github.com/shiv/internal/ui/widgets"
)

type pluginsTab struct {
	projectStore *store.Store
	bus          *events.Bus
	win          fyne.Window
}

func newPluginsTab(projectStore *store.Store, bus *events.Bus, win fyne.Window) *pluginsTab {
	return &pluginsTab{
		projectStore: projectStore,
		bus:          bus,
		win:          win,
	}
}

func (t *pluginsTab) build() fyne.CanvasObject {
	var plugins []store.PluginEntry
	selectedIdx := -1

	logView := widgets.NewTextView()
	logView.SetWindow(t.win)

	toggleBtn := widget.NewButton("Disable", nil)
	toggleBtn.Disable()

	var pluginList *widget.List

	refreshLogs := func() {
		if selectedIdx < 0 || selectedIdx >= len(plugins) {
			logView.SetText("")
			return
		}
		lines := t.projectStore.PluginLogs(plugins[selectedIdx].Name)
		logView.SetText(strings.Join(lines, "\n"))
	}

	refreshAll := func() {
		entries, err := t.projectStore.AllPlugins()
		if err != nil {
			logger.Error("plugins: load: %v", err)
			return
		}
		plugins = entries
		pluginList.Refresh()

		// Keep toggle button label in sync after external state change.
		if selectedIdx >= 0 && selectedIdx < len(plugins) {
			if plugins[selectedIdx].Enabled {
				toggleBtn.SetText("Disable")
			} else {
				toggleBtn.SetText("Enable")
			}
			toggleBtn.Enable()
		}

		refreshLogs()
	}

	pluginList = widget.NewList(
		func() int { return len(plugins) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel(""),
				widget.NewLabel(""),
			)
		},
		func(id widget.ListItemID, item fyne.CanvasObject) {
			if id >= len(plugins) {
				return
			}
			p := plugins[id]
			box := item.(*fyne.Container)
			status := box.Objects[0].(*widget.Label)
			name := box.Objects[1].(*widget.Label)
			if p.Enabled {
				status.SetText("✓")
				status.Importance = widget.SuccessImportance
			} else {
				status.SetText("✗")
				status.Importance = widget.DangerImportance
			}
			name.SetText(p.Name)
		},
	)

	pluginList.OnSelected = func(id widget.ListItemID) {
		selectedIdx = id
		if id >= len(plugins) {
			toggleBtn.Disable()
			return
		}
		if plugins[id].Enabled {
			toggleBtn.SetText("Disable")
		} else {
			toggleBtn.SetText("Enable")
		}
		toggleBtn.Enable()
		refreshLogs()
	}

	pluginList.OnUnselected = func(_ widget.ListItemID) {
		selectedIdx = -1
		toggleBtn.Disable()
		logView.SetText("")
	}

	toggleBtn.OnTapped = func() {
		if selectedIdx < 0 || selectedIdx >= len(plugins) {
			return
		}
		p := plugins[selectedIdx]
		t.bus.EmitSetPluginEnabled(events.SetPluginEnabledEvent{
			Name:    p.Name,
			Enabled: !p.Enabled,
		})
	}

	addBtn := widget.NewButton("Add", func() {
		fd := dialog.NewFileOpen(func(uc fyne.URIReadCloser, err error) {
			if err != nil || uc == nil {
				return
			}
			uc.Close()
			t.bus.EmitLoadPlugin(events.LoadPluginEvent{
				SourcePath: uc.URI().Path(),
			})
		}, t.win)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".lua"}))
		fd.Show()
	})

	// Listen for plugin state and log changes from the store.
	go func() {
		for range t.projectStore.PluginEntries {
			fyne.Do(refreshAll)
		}
	}()

	// Initial population.
	refreshAll()

	toolbar := container.New(
		layout.NewCustomPaddedLayout(8, 8, 0, 0),
		container.NewHBox(addBtn, toggleBtn),
	)

	leftPane := container.NewBorder(
		newBoldLabel("Plugins"),
		toolbar,
		nil, nil,
		pluginList,
	)

	rightPane := container.NewBorder(
		newBoldLabel("Log"),
		nil, nil, nil,
		logView.Build(),
	)

	split := container.NewHSplit(leftPane, rightPane)
	split.SetOffset(0.3)

	return split
}
