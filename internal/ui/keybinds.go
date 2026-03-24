package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
)

type Keybinds struct {
	win      fyne.Window
	tabs     *container.AppTabs
	history  *historyTab
	repeater *repeaterTab
	active   []fyne.Shortcut

	KeySend       fyne.KeyName
	KeyCloseTab   fyne.KeyName
	KeyToRepeater fyne.KeyName
}

func newKeybinds(win fyne.Window, tabs *container.AppTabs, history *historyTab, repeater *repeaterTab) *Keybinds {
	k := &Keybinds{
		win:           win,
		tabs:          tabs,
		history:       history,
		repeater:      repeater,
		KeySend:       fyne.KeyS,
		KeyCloseTab:   fyne.KeyW,
		KeyToRepeater: fyne.KeyR,
	}
	k.Update()
	return k
}

func (k *Keybinds) Update() {
	for _, s := range k.active {
		k.win.Canvas().RemoveShortcut(s)
	}
	k.active = nil

	activeTab := func(name string) bool {
		selected := k.tabs.Selected()
		return selected != nil && selected.Text == name
	}

	send := &desktop.CustomShortcut{KeyName: k.KeySend, Modifier: fyne.KeyModifierControl}
	k.win.Canvas().AddShortcut(send, func(_ fyne.Shortcut) {
		if !activeTab("Repeater") {
			return
		}
		selected := k.repeater.tabs.Selected()
		if selected == nil {
			return
		}
		if fn, ok := k.repeater.sendFns[selected]; ok {
			fn()
		}
	})
	k.active = append(k.active, send)

	closeTab := &desktop.CustomShortcut{KeyName: k.KeyCloseTab, Modifier: fyne.KeyModifierControl}
	k.win.Canvas().AddShortcut(closeTab, func(_ fyne.Shortcut) {
		if !activeTab("Repeater") {
			return
		}
		selected := k.repeater.tabs.Selected()
		if selected == nil {
			return
		}
		k.repeater.closeTab(selected)
		k.repeater.tabs.Remove(selected)
	})
	k.active = append(k.active, closeTab)

	toRepeater := &desktop.CustomShortcut{KeyName: k.KeyToRepeater, Modifier: fyne.KeyModifierControl}
	k.win.Canvas().AddShortcut(toRepeater, func(_ fyne.Shortcut) {
		if activeTab("History") && k.history.hasSelected {
			k.history.sendToRepeater(k.history.selectedTx)
		}
	})
	k.active = append(k.active, toRepeater)
}
