package ui

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/shiv/assets"
)

var logoBytes []byte

const maxRecentProjects = 10

type RecentProject struct {
	Path     string    `json:"path"`
	LastOpen time.Time `json:"last_open"`
}

func ShowLaunchScreen(app fyne.App, onSelect func(projectPath string, w fyne.Window)) {
	w := app.NewWindow("Shiv")
	w.Resize(fyne.NewSize(560, 420))
	w.CenterOnScreen()
	w.SetFixedSize(true)

	recents, _ := loadRecents()

	list := widget.NewList(
		func() int { return len(recents) },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewIcon(theme.DocumentIcon()),
				container.NewVBox(
					widget.NewLabel("name"),
					widget.NewLabel("date"),
				),
			)
		},
		func(i widget.ListItemID, obj fyne.CanvasObject) {
			box := obj.(*fyne.Container).Objects[1].(*fyne.Container)
			box.Objects[0].(*widget.Label).SetText(filepath.Base(recents[i].Path))
			box.Objects[1].(*widget.Label).SetText(recents[i].LastOpen.Format("2006-01-02 15:04"))
			box.Objects[1].(*widget.Label).Importance = widget.LowImportance
		},
	)

	list.OnSelected = func(i widget.ListItemID) {
		path := recents[i].Path
		if _, err := os.Stat(path); err != nil {
			dialog.ShowError(err, w)
			list.UnselectAll()
			return
		}
		saveRecent(path)
		onSelect(path, w)
	}

	newBtn := widget.NewButtonWithIcon("New Project", theme.DocumentCreateIcon(), func() {
		d := dialog.NewFileSave(func(uc fyne.URIWriteCloser, err error) {
			if err != nil || uc == nil {
				return
			}
			uc.Close()
			path := uc.URI().Path()
			saveRecent(path)
			onSelect(path, w)
		}, w)
		d.SetFileName("untitled.shiv")
		d.SetFilter(storage.NewExtensionFileFilter([]string{".shiv"}))
		d.Show()
	})

	openBtn := widget.NewButtonWithIcon("Open Project", theme.FolderOpenIcon(), func() {
		d := dialog.NewFileOpen(func(uc fyne.URIReadCloser, err error) {
			if err != nil || uc == nil {
				return
			}
			uc.Close()
			path := uc.URI().Path()
			saveRecent(path)
			onSelect(path, w)
		}, w)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".shiv"}))
		d.Show()
	})
	newBtn.Importance = widget.HighImportance

	logo := canvas.NewImageFromResource(fyne.NewStaticResource("logo.png", assets.Logo))
	logo.FillMode = canvas.ImageFillContain
	logo.SetMinSize(fyne.NewSize(64, 64))

	subtitle := widget.NewLabel("HTTP/HTTPS Interception Proxy")
	subtitle.Importance = widget.LowImportance

	recentLabel := widget.NewLabel("Recent Projects")
	recentLabel.TextStyle = fyne.TextStyle{Bold: true}

	header := container.NewVBox(
		container.NewCenter(logo),
		container.NewCenter(subtitle),
		widget.NewSeparator(),
	)

	buttons := container.NewGridWithColumns(2, newBtn, openBtn)

	var content fyne.CanvasObject
	if len(recents) == 0 {
		empty := widget.NewLabel("No recent projects")
		empty.Importance = widget.LowImportance
		content = container.NewBorder(
			container.NewVBox(header, recentLabel),
			buttons,
			nil, nil,
			container.NewCenter(empty),
		)
	} else {
		content = container.NewBorder(
			container.NewVBox(header, recentLabel),
			buttons,
			nil, nil,
			list,
		)
	}

	w.SetContent(container.NewPadded(content))
	w.Show()
}

func recentsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "shiv", "recent_projects.json"), nil
}

func loadRecents() ([]RecentProject, error) {
	path, err := recentsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var recents []RecentProject
	if err := json.Unmarshal(data, &recents); err != nil {
		return nil, err
	}
	return recents, nil
}

func saveRecent(projectPath string) {
	recents, _ := loadRecents()

	filtered := recents[:0]
	for _, r := range recents {
		if r.Path != projectPath {
			filtered = append(filtered, r)
		}
	}

	updated := append([]RecentProject{{Path: projectPath, LastOpen: time.Now()}}, filtered...)

	if len(updated) > maxRecentProjects {
		updated = updated[:maxRecentProjects]
	}

	path, err := recentsPath()
	if err != nil {
		return
	}
	data, err := json.Marshal(updated)
	if err != nil {
		return
	}
	os.WriteFile(path, data, 0o644)
}
