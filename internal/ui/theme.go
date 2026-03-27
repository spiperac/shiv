package ui

import (
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"github.com/BurntSushi/toml"
	"github.com/shiv/assets"
)

// ── Theme file format ─────────────────────────────────────────────────────────

type themePalette struct {
	Background        string `toml:"background"`
	Button            string `toml:"button"`
	DisabledButton    string `toml:"disabled_button"`
	Disabled          string `toml:"disabled"`
	Error             string `toml:"error"`
	Focus             string `toml:"focus"`
	Foreground        string `toml:"foreground"`
	HeaderBackground  string `toml:"header_background"`
	Hover             string `toml:"hover"`
	Hyperlink         string `toml:"hyperlink"`
	InputBackground   string `toml:"input_background"`
	InputBorder       string `toml:"input_border"`
	MenuBackground    string `toml:"menu_background"`
	OverlayBackground string `toml:"overlay_background"`
	PlaceHolder       string `toml:"placeholder"`
	Pressed           string `toml:"pressed"`
	Primary           string `toml:"primary"`
	ScrollBar         string `toml:"scrollbar"`
	Selection         string `toml:"selection"`
	Separator         string `toml:"separator"`
	Shadow            string `toml:"shadow"`
	Success           string `toml:"success"`
	Warning           string `toml:"warning"`
}

type themeFile struct {
	Dark  *themePalette `toml:"dark"`
	Light *themePalette `toml:"light"`
}

// LoadedTheme is a parsed, validated theme ready for use.
type LoadedTheme struct {
	Name     string
	Dark     map[fyne.ThemeColorName]color.Color
	Light    map[fyne.ThemeColorName]color.Color
	HasBoth  bool
	Embedded bool
}

// ── Color name mapping ────────────────────────────────────────────────────────

var paletteKeys = map[fyne.ThemeColorName]string{
	theme.ColorNameBackground:        "background",
	theme.ColorNameButton:            "button",
	theme.ColorNameDisabledButton:    "disabled_button",
	theme.ColorNameDisabled:          "disabled",
	theme.ColorNameError:             "error",
	theme.ColorNameFocus:             "focus",
	theme.ColorNameForeground:        "foreground",
	theme.ColorNameHeaderBackground:  "header_background",
	theme.ColorNameHover:             "hover",
	theme.ColorNameHyperlink:         "hyperlink",
	theme.ColorNameInputBackground:   "input_background",
	theme.ColorNameInputBorder:       "input_border",
	theme.ColorNameMenuBackground:    "menu_background",
	theme.ColorNameOverlayBackground: "overlay_background",
	theme.ColorNamePlaceHolder:       "placeholder",
	theme.ColorNamePressed:           "pressed",
	theme.ColorNamePrimary:           "primary",
	theme.ColorNameScrollBar:         "scrollbar",
	theme.ColorNameSelection:         "selection",
	theme.ColorNameSeparator:         "separator",
	theme.ColorNameShadow:            "shadow",
	theme.ColorNameSuccess:           "success",
	theme.ColorNameWarning:           "warning",
}

var paletteKeysReverse map[string]fyne.ThemeColorName

func init() {
	paletteKeysReverse = make(map[string]fyne.ThemeColorName, len(paletteKeys))
	for k, v := range paletteKeys {
		paletteKeysReverse[v] = k
	}
}

// ── Theme scanning ────────────────────────────────────────────────────────────

const ThemeDefaultOption = "Default"

// ThemesDir returns the user themes directory, creating it if needed.
func ThemesDir(storageRoot string) string {
	dir := filepath.Join(storageRoot, "themes")
	os.MkdirAll(dir, 0755)
	return dir
}

// ScanEmbeddedThemes returns names of all embedded themes (without extension).
func ScanEmbeddedThemes() []string {
	entries, err := fs.ReadDir(assets.Themes, "themes")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	return names
}

// ScanUserThemes returns names of all user .toml themes in dir (without extension).
func ScanUserThemes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
		}
	}
	return names
}

// ScanAllThemes returns the full theme list for the dropdown:
// [Default, ...embedded..., ...user (skipping duplicates of embedded)...]
func ScanAllThemes(userThemesDir string) []string {
	embedded := ScanEmbeddedThemes()
	embeddedSet := make(map[string]bool, len(embedded))
	for _, n := range embedded {
		embeddedSet[n] = true
	}
	options := make([]string, 0, 1+len(embedded)+8)
	options = append(options, ThemeDefaultOption)
	options = append(options, embedded...)
	for _, n := range ScanUserThemes(userThemesDir) {
		if !embeddedSet[n] {
			options = append(options, n)
		}
	}
	return options
}

// ── Theme loading ─────────────────────────────────────────────────────────────

// LoadEmbeddedTheme loads a theme by name from the embedded assets.
func LoadEmbeddedTheme(name string) (*LoadedTheme, error) {
	data, err := assets.Themes.ReadFile("themes/" + name + ".toml")
	if err != nil {
		return nil, fmt.Errorf("embedded theme %q not found: %w", name, err)
	}
	lt, err := parseThemeBytes(data, name)
	if err != nil {
		return nil, err
	}
	lt.Embedded = true
	return lt, nil
}

// LoadTheme loads a theme from a file path on disk.
func LoadTheme(path string) (*LoadedTheme, error) {
	name := strings.TrimSuffix(filepath.Base(path), ".toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read theme: %w", err)
	}
	return parseThemeBytes(data, name)
}

func parseThemeBytes(data []byte, name string) (*LoadedTheme, error) {
	var tf themeFile
	if err := toml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parse theme: %w", err)
	}
	if tf.Dark == nil && tf.Light == nil {
		return nil, fmt.Errorf("theme must have at least a [dark] or [light] section")
	}
	lt := &LoadedTheme{
		Name:    name,
		HasBoth: tf.Dark != nil && tf.Light != nil,
	}
	if tf.Dark != nil {
		m, err := parsePalette(tf.Dark)
		if err != nil {
			return nil, fmt.Errorf("[dark]: %w", err)
		}
		lt.Dark = m
	}
	if tf.Light != nil {
		m, err := parsePalette(tf.Light)
		if err != nil {
			return nil, fmt.Errorf("[light]: %w", err)
		}
		lt.Light = m
	}
	if lt.Dark == nil {
		lt.Dark = lt.Light
	}
	if lt.Light == nil {
		lt.Light = lt.Dark
	}
	return lt, nil
}

func parsePalette(p *themePalette) (map[fyne.ThemeColorName]color.Color, error) {
	raw := map[string]string{
		"background":         p.Background,
		"button":             p.Button,
		"disabled_button":    p.DisabledButton,
		"disabled":           p.Disabled,
		"error":              p.Error,
		"focus":              p.Focus,
		"foreground":         p.Foreground,
		"header_background":  p.HeaderBackground,
		"hover":              p.Hover,
		"hyperlink":          p.Hyperlink,
		"input_background":   p.InputBackground,
		"input_border":       p.InputBorder,
		"menu_background":    p.MenuBackground,
		"overlay_background": p.OverlayBackground,
		"placeholder":        p.PlaceHolder,
		"pressed":            p.Pressed,
		"primary":            p.Primary,
		"scrollbar":          p.ScrollBar,
		"selection":          p.Selection,
		"separator":          p.Separator,
		"shadow":             p.Shadow,
		"success":            p.Success,
		"warning":            p.Warning,
	}
	m := make(map[fyne.ThemeColorName]color.Color, len(raw))
	for key, val := range raw {
		if val == "" {
			continue
		}
		c, err := parseHex(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if colorName, ok := paletteKeysReverse[key]; ok {
			m[colorName] = c
		}
	}
	return m, nil
}

func parseHex(h string) (color.NRGBA, error) {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return color.NRGBA{}, fmt.Errorf("invalid hex %q (expected #rrggbb)", h)
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.NRGBA{}, fmt.Errorf("invalid hex %q: %w", h, err)
	}
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}, nil
}

// ── Fyne theme implementation ─────────────────────────────────────────────────

type vagueTheme struct {
	dark   bool
	loaded *LoadedTheme
}

func NewVagueTheme(dark bool) fyne.Theme {
	return &vagueTheme{dark: dark}
}

func NewVagueThemeWithLoaded(dark bool, lt *LoadedTheme) fyne.Theme {
	return &vagueTheme{dark: dark, loaded: lt}
}

func (t *vagueTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.loaded != nil {
		var palette map[fyne.ThemeColorName]color.Color
		if t.dark {
			palette = t.loaded.Dark
		} else {
			palette = t.loaded.Light
		}
		if c, ok := palette[name]; ok {
			return c
		}
	}
	if t.dark {
		return theme.DefaultTheme().Color(name, theme.VariantDark)
	}
	return theme.DefaultTheme().Color(name, theme.VariantLight)
}

func (t *vagueTheme) Font(style fyne.TextStyle) fyne.Resource {
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
}

func (t *vagueTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *vagueTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == theme.SizeNameInlineIcon {
		return 28
	}
	return theme.DefaultTheme().Size(name)
}

// ── Icons ─────────────────────────────────────────────────────────────────────

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
	case "toolbox":
		return fyne.NewStaticResource("toolbox.png", assets.IconToolbox)
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
