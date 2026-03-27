package ui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/shiv/assets"
)

type vagueTheme struct {
	dark bool
}

func NewVagueTheme(dark bool) fyne.Theme {
	return &vagueTheme{dark: dark}
}

func (t *vagueTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.dark {
		return darkColor(name, variant)
	}
	return lightColor(name, variant)
}

func (t *vagueTheme) Font(style fyne.TextStyle) fyne.Resource {
	defer func() {
		if r := recover(); r != nil {
			// fallback to default theme in case of missing font
			return
		}
	}()
	switch {
	case style.Bold && style.Italic:
		return fyne.NewStaticResource("JetBrainsMono-BoldItalic.ttf", assets.FontJetBrainsMonoBold)
	case style.Bold:
		return fyne.NewStaticResource("JetBrainsMono-Bold.ttf", assets.FontJetBrainsMonoBold)
	case style.Italic:
		return fyne.NewStaticResource("JetBrainsMono-Italic.ttf", assets.FontJetBrainsMonoItalic)
	default:
		return fyne.NewStaticResource("JetBrainsMono-Regular.ttf", assets.FontJetBrainsMonoRegular)
	}
	return theme.DefaultTheme().Font(style)
}

func (t *vagueTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func AppIcon(name string) fyne.Resource {
	switch name {
	case "delete":
		return fyne.NewStaticResource("delete.png", assets.IconDelete)
	case "save":
		return fyne.NewStaticResource("save.png", assets.IconSave)
	case "history":
		return fyne.NewStaticResource("history.png", assets.IconHistory)
	case "repeater":
		return fyne.NewStaticResource("boomerang.png", assets.IconRepeater)
	case "inspector":
		return fyne.NewStaticResource("inspector.png", assets.IconInspector)
	case "intruder":
		return fyne.NewStaticResource("intruder.png", assets.IconIntruder)
	case "intercept":
		return fyne.NewStaticResource("intercept.png", assets.IconIntercept)
	case "loot":
		return fyne.NewStaticResource("loot.png", assets.IconLoot)
	case "project":
		return fyne.NewStaticResource("project.png", assets.IconProject)
	case "scope":
		return fyne.NewStaticResource("scope.png", assets.IconScope)
	case "off-button":
		return fyne.NewStaticResource("off-button.png", assets.IconOffButton)
	case "on-button":
		return fyne.NewStaticResource("on-button.png", assets.IconOnButton)
	case "settings":
		return fyne.NewStaticResource("settings.png", assets.IconSettings)
	case "web":
		return fyne.NewStaticResource("web.png", assets.IconWeb)
	case "web1":
		return fyne.NewStaticResource("web1.png", assets.IconWeb1)
	case "document":
		return fyne.NewStaticResource("document.png", assets.IconDocument)
	case "folder":
		return fyne.NewStaticResource("folder.png", assets.IconFolder)
	case "media":
		return fyne.NewStaticResource("media.png", assets.IconMedia)
	}
	return theme.DefaultTheme().Icon(theme.IconNameQuestion)
}

func (t *vagueTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameInlineIcon:
		return 28
	}
	return theme.DefaultTheme().Size(name)
}

func hex(h string) color.NRGBA {
	var r, g, b uint8
	fmt.Sscanf(h, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}
}

func darkColor(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return hex("#141415")
	case theme.ColorNameButton:
		return hex("#1c1c24")
	case theme.ColorNameDisabledButton:
		return hex("#252530")
	case theme.ColorNameDisabled:
		return hex("#606079")
	case theme.ColorNameError:
		return hex("#d8647e")
	case theme.ColorNameFocus:
		return hex("#6e94b2")
	case theme.ColorNameForeground:
		return hex("#cdcdcd")
	case theme.ColorNameHeaderBackground:
		return hex("#1c1c24")
	case theme.ColorNameHover:
		return hex("#252530")
	case theme.ColorNameHyperlink:
		return hex("#7e98e8")
	case theme.ColorNameInputBackground:
		return hex("#1c1c24")
	case theme.ColorNameInputBorder:
		return hex("#878787")
	case theme.ColorNameMenuBackground:
		return hex("#1c1c24")
	case theme.ColorNameOverlayBackground:
		return hex("#1c1c24")
	case theme.ColorNamePlaceHolder:
		return hex("#606079")
	case theme.ColorNamePressed:
		return hex("#333738")
	case theme.ColorNamePrimary:
		return hex("#6e94b2")
	case theme.ColorNameScrollBar:
		return hex("#606079")
	case theme.ColorNameSelection:
		return hex("#333738")
	case theme.ColorNameSeparator:
		return hex("#252530")
	case theme.ColorNameShadow:
		return hex("#141415")
	case theme.ColorNameSuccess:
		return hex("#7fa563")
	case theme.ColorNameWarning:
		return hex("#f3be7c")
	}
	return theme.DefaultTheme().Color(name, variant)
}

func lightColor(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return hex("#f5f5f0")
	case theme.ColorNameButton:
		return hex("#ebebde")
	case theme.ColorNameDisabledButton:
		return hex("#e8e8d8")
	case theme.ColorNameDisabled:
		return hex("#8a8a9e")
	case theme.ColorNameError:
		return hex("#c8435e")
	case theme.ColorNameFocus:
		return hex("#4a6a8a")
	case theme.ColorNameForeground:
		return hex("#2e2e2e")
	case theme.ColorNameHeaderBackground:
		return hex("#ebebde")
	case theme.ColorNameHover:
		return hex("#e8e8d8")
	case theme.ColorNameHyperlink:
		return hex("#4a6ec8")
	case theme.ColorNameInputBackground:
		return hex("#ebebde")
	case theme.ColorNameInputBorder:
		return hex("#a0a0a0")
	case theme.ColorNameMenuBackground:
		return hex("#ebebde")
	case theme.ColorNameOverlayBackground:
		return hex("#ebebde")
	case theme.ColorNamePlaceHolder:
		return hex("#8a8a9e")
	case theme.ColorNamePressed:
		return hex("#d8d8da")
	case theme.ColorNamePrimary:
		return hex("#4a6a8a")
	case theme.ColorNameScrollBar:
		return hex("#8a8a9e")
	case theme.ColorNameSelection:
		return hex("#d8d8da")
	case theme.ColorNameSeparator:
		return hex("#e8e8d8")
	case theme.ColorNameShadow:
		return hex("#f5f5f0")
	case theme.ColorNameSuccess:
		return hex("#5a8543")
	case theme.ColorNameWarning:
		return hex("#d89a3c")
	}
	return theme.DefaultTheme().Color(name, variant)
}
