package ui

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type decoderOperation struct {
	name         string
	fwdLabel     string // label for the forward button
	bwdLabel     string // label for the backward button; empty = no backward
	forward      func(string) string
	backward     func(string) (string, error) // nil = one-way
}

var decoderOps = []decoderOperation{
	{
		name:     "URL Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  url.QueryEscape,
		backward: func(s string) (string, error) { return url.QueryUnescape(s) },
	},
	{
		name:     "URL Path Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  url.PathEscape,
		backward: func(s string) (string, error) { return url.PathUnescape(s) },
	},
	{
		name:     "Base64 Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		backward: func(s string) (string, error) {
			s = strings.TrimSpace(s)
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				b, err = base64.URLEncoding.DecodeString(s)
			}
			return string(b), err
		},
	},
	{
		name:     "Base64 URL-safe Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) },
		backward: func(s string) (string, error) {
			b, err := base64.URLEncoding.DecodeString(strings.TrimSpace(s))
			return string(b), err
		},
	},
	{
		name:     "Hex Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  func(s string) string { return hex.EncodeToString([]byte(s)) },
		backward: func(s string) (string, error) {
			b, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
			return string(b), err
		},
	},
	{
		name:     "HTML Entity Encode / Decode",
		fwdLabel: "Encode →",
		bwdLabel: "← Decode",
		forward:  html.EscapeString,
		backward: func(s string) (string, error) { return html.UnescapeString(s), nil },
	},
	{
		name:     "JSON Pretty / Minify",
		fwdLabel: "Pretty →",
		bwdLabel: "← Minify",
		forward: func(s string) string {
			var v any
			if err := json.Unmarshal([]byte(s), &v); err != nil {
				return "Error: " + err.Error()
			}
			var buf strings.Builder
			enc := json.NewEncoder(&buf)
			enc.SetIndent("", "  ")
			if err := enc.Encode(v); err != nil {
				return "Error: " + err.Error()
			}
			return strings.TrimRight(buf.String(), "\n")
		},
		backward: func(s string) (string, error) {
			var v any
			if err := json.Unmarshal([]byte(s), &v); err != nil {
				return "", err
			}
			b, err := json.Marshal(v)
			return string(b), err
		},
	},
	{
		name:     "JWT Decode (no verify)",
		fwdLabel: "Decode →",
		forward: func(s string) string {
			parts := strings.Split(strings.TrimSpace(s), ".")
			if len(parts) < 2 {
				return "Error: not a JWT (expected header.payload.signature)"
			}
			var result strings.Builder
			for i, part := range parts[:min(2, len(parts))] {
				label := []string{"Header", "Payload"}[i]
				b, err := base64.RawURLEncoding.DecodeString(part)
				if err != nil {
					result.WriteString(fmt.Sprintf("=== %s ===\nError: %v\n\n", label, err))
					continue
				}
				result.WriteString(fmt.Sprintf("=== %s ===\n%s\n\n", label, string(PrettyJSON(b))))
			}
			if len(parts) >= 3 {
				result.WriteString(fmt.Sprintf("=== Signature ===\n%s", parts[2]))
			}
			return result.String()
		},
	},
	{
		name:     "MD5 Hash",
		fwdLabel: "Hash →",
		forward: func(s string) string {
			h := md5.Sum([]byte(s))
			return hex.EncodeToString(h[:])
		},
	},
	{
		name:     "SHA1 Hash",
		fwdLabel: "Hash →",
		forward: func(s string) string {
			h := sha1.Sum([]byte(s))
			return hex.EncodeToString(h[:])
		},
	},
	{
		name:     "SHA256 Hash",
		fwdLabel: "Hash →",
		forward: func(s string) string {
			h := sha256.Sum256([]byte(s))
			return hex.EncodeToString(h[:])
		},
	},
}

type decoderTab struct{}

func newDecoderTab() *decoderTab { return &decoderTab{} }

func (d *decoderTab) build() fyne.CanvasObject {
	inputEntry := widget.NewMultiLineEntry()
	inputEntry.SetPlaceHolder("Input...")
	inputEntry.TextStyle = fyne.TextStyle{Monospace: true}
	inputEntry.Wrapping = fyne.TextWrapOff

	outputEntry := widget.NewMultiLineEntry()
	outputEntry.SetPlaceHolder("Output...")
	outputEntry.TextStyle = fyne.TextStyle{Monospace: true}
	outputEntry.Wrapping = fyne.TextWrapOff

	opNames := make([]string, len(decoderOps))
	for i, op := range decoderOps {
		opNames[i] = op.name
	}

	selectedIdx := 0

	fwdBtn := widget.NewButton(decoderOps[0].fwdLabel, nil)
	fwdBtn.Importance = widget.HighImportance

	bwdBtn := widget.NewButton("← Decode", nil)

	fwdBtn.OnTapped = func() {
		op := decoderOps[selectedIdx]
		outputEntry.SetText(op.forward(inputEntry.Text))
	}
	bwdBtn.OnTapped = func() {
		op := decoderOps[selectedIdx]
		if op.backward == nil {
			return
		}
		result, err := op.backward(inputEntry.Text)
		if err != nil {
			outputEntry.SetText("Error: " + err.Error())
			return
		}
		outputEntry.SetText(result)
	}

	opSelect := widget.NewSelect(opNames, func(s string) {
		for i, op := range decoderOps {
			if op.name == s {
				selectedIdx = i
				fwdBtn.SetText(op.fwdLabel)
				if op.backward != nil {
					bwdBtn.SetText(op.bwdLabel)
					bwdBtn.Show()
				} else {
					bwdBtn.Hide()
				}
				return
			}
		}
	})
	opSelect.SetSelected(opNames[0])

	// SHA1/MD5/SHA256/JWT have no backward — hide decode button initially if needed.
	if decoderOps[0].backward == nil {
		bwdBtn.Hide()
	}

	swapBtn := widget.NewButton("⇅ Swap", func() {
		in := inputEntry.Text
		inputEntry.SetText(outputEntry.Text)
		outputEntry.SetText(in)
	})

	copyBtn := widget.NewButton("Copy Output", func() {
		fyne.CurrentApp().Clipboard().SetContent(outputEntry.Text)
	})

	toolbar := container.NewBorder(nil, nil,
		container.NewHBox(fwdBtn, bwdBtn),
		container.NewHBox(swapBtn, copyBtn),
		opSelect,
	)

	inputPane := container.NewBorder(newBoldLabel("Input"), nil, nil, nil, inputEntry)
	outputPane := container.NewBorder(newBoldLabel("Output"), nil, nil, nil, outputEntry)

	split := container.NewVSplit(inputPane, outputPane)
	split.SetOffset(0.5)

	return container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(8, 8, 0, 0), toolbar),
		nil, nil, nil,
		split,
	)
}
