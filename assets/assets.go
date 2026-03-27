package assets

import (
	"embed"
	_ "embed"
)

//go:embed logo.png
var Logo []byte

//go:embed themes/*.toml
var Themes embed.FS

//go:embed icons/delete.png
var IconDelete []byte

//go:embed icons/save.png
var IconSave []byte

//go:embed icons/history.png
var IconHistory []byte

//go:embed icons/inspector.png
var IconInspector []byte

//go:embed icons/intruder.png
var IconIntruder []byte

//go:embed icons/boomerang.png
var IconRepeater []byte

//go:embed icons/intercept.png
var IconIntercept []byte

//go:embed icons/loot.png
var IconLoot []byte

//go:embed icons/project.png
var IconProject []byte

//go:embed icons/scope.png
var IconScope []byte

//go:embed icons/settings.png
var IconSettings []byte

//go:embed icons/toolbox.png
var IconToolbox []byte

//go:embed icons/on-button.png
var IconOnButton []byte

//go:embed icons/off-button.png
var IconOffButton []byte

//go:embed icons/web.png
var IconWeb []byte

//go:embed icons/web1.png
var IconWeb1 []byte

//go:embed icons/document.png
var IconDocument []byte

//go:embed icons/folder.png
var IconFolder []byte

//go:embed icons/media.png
var IconMedia []byte

//go:embed findings.md.tmpl
var FindingsTemplate string

//go:embed fonts/JetBrainsMono-Regular.ttf
var FontJetBrainsMonoRegular []byte

//go:embed fonts/JetBrainsMono-Bold.ttf
var FontJetBrainsMonoBold []byte

//go:embed fonts/JetBrainsMono-Italic.ttf
var FontJetBrainsMonoItalic []byte
