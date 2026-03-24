package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
)

const (
	prefKeySendRequest = "shortcut_send_request"
	prefKeyCloseTab    = "shortcut_close_tab"
	prefKeyToRepeater  = "shortcut_to_repeater"

	defaultKeySendRequest fyne.KeyName = fyne.KeyS
	defaultKeyCloseTab    fyne.KeyName = fyne.KeyW
	defaultKeyToRepeater  fyne.KeyName = fyne.KeyR
)

type Keybinds struct {
	win      fyne.Window
	tabs     *container.AppTabs
	history  *historyTab
	repeater *repeaterTab
	active   []fyne.Shortcut

	KeySendRequest fyne.KeyName
	KeyCloseTab    fyne.KeyName
	KeyToRepeater  fyne.KeyName
}

func newKeybinds(win fyne.Window, tabs *container.AppTabs, history *historyTab, repeater *repeaterTab, prefs fyne.Preferences) *Keybinds {
	keybinds := &Keybinds{
		win:            win,
		tabs:           tabs,
		history:        history,
		repeater:       repeater,
		KeySendRequest: fyne.KeyName(prefs.StringWithFallback(prefKeySendRequest, string(defaultKeySendRequest))),
		KeyCloseTab:    fyne.KeyName(prefs.StringWithFallback(prefKeyCloseTab, string(defaultKeyCloseTab))),
		KeyToRepeater:  fyne.KeyName(prefs.StringWithFallback(prefKeyToRepeater, string(defaultKeyToRepeater))),
	}
	keybinds.Update()
	return keybinds
}

func (keybinds *Keybinds) Update() {
	for _, registeredShortcut := range keybinds.active {
		keybinds.win.Canvas().RemoveShortcut(registeredShortcut)
	}
	keybinds.active = nil

	isActiveTab := func(tabName string) bool {
		selectedTab := keybinds.tabs.Selected()
		return selectedTab != nil && selectedTab.Text == tabName
	}

	sendRequestShortcut := &desktop.CustomShortcut{KeyName: keybinds.KeySendRequest, Modifier: fyne.KeyModifierControl}
	keybinds.win.Canvas().AddShortcut(sendRequestShortcut, func(_ fyne.Shortcut) {
		if !isActiveTab("Repeater") {
			return
		}
		selectedTab := keybinds.repeater.tabs.Selected()
		if selectedTab == nil {
			return
		}
		if sendFn, ok := keybinds.repeater.sendFns[selectedTab]; ok {
			sendFn()
		}
	})
	keybinds.active = append(keybinds.active, sendRequestShortcut)

	closeTabShortcut := &desktop.CustomShortcut{KeyName: keybinds.KeyCloseTab, Modifier: fyne.KeyModifierControl}
	keybinds.win.Canvas().AddShortcut(closeTabShortcut, func(_ fyne.Shortcut) {
		if !isActiveTab("Repeater") {
			return
		}
		selectedTab := keybinds.repeater.tabs.Selected()
		if selectedTab == nil {
			return
		}
		keybinds.repeater.closeTab(selectedTab)
		keybinds.repeater.tabs.Remove(selectedTab)
	})
	keybinds.active = append(keybinds.active, closeTabShortcut)

	toRepeaterShortcut := &desktop.CustomShortcut{KeyName: keybinds.KeyToRepeater, Modifier: fyne.KeyModifierControl}
	keybinds.win.Canvas().AddShortcut(toRepeaterShortcut, func(_ fyne.Shortcut) {
		if isActiveTab("History") && keybinds.history.hasSelected {
			keybinds.history.sendToRepeater(keybinds.history.selectedTx)
		}
	})
	keybinds.active = append(keybinds.active, toRepeaterShortcut)
}
