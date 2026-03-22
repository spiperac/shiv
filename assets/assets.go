package assets

import _ "embed"

//go:embed logo.png
var Logo []byte

//go:embed icons/delete.png
var IconDelete []byte

//go:embed icons/save.png
var IconSave []byte

//go:embed icons/history.png
var IconHistory []byte

//go:embed icons/inspector.png
var IconInspector []byte

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

//go:embed findings.md.tmpl
var FindingsTemplate string
