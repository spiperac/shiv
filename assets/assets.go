package assets

import _ "embed"

//go:embed logo.png
var Logo []byte

//go:embed findings.md.tmpl
var FindingsTemplate string
