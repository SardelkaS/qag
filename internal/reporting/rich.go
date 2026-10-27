package reporting

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"image/png"
	"strings"
	"unicode/utf16"

	"qag/internal/problem"
	"qag/internal/runs"
)

type PNGPart struct {
	GroupID    string `json:"groupId"`
	Part       int    `json:"part"`
	Total      int    `json:"total"`
	DataBase64 string `json:"dataBase64"`
}
type ClipboardWrite struct {
	SnapshotID      string    `json:"snapshotId"`
	Hash            string    `json:"hash"`
	IncludeExpected bool      `json:"includeExpected"`
	Parts           []PNGPart `json:"parts"`
}
type ClipboardResult struct {
	Copied bool `json:"copied"`
	Native bool `json:"native"`
}
type RichContent struct{ Text, HTML, RTF string }

func GroupID(node runs.ReportNode) string {
	if node.CallID != "" {
		return node.CallID
	}
	if node.Kind == "image" {
		return "image:" + node.ID
	}
	return ""
}
func rtfText(text string) string {
	var out strings.Builder
	for _, unit := range utf16.Encode([]rune(text)) {
		switch unit {
		case '\\', '{', '}':
			out.WriteByte('\\')
			out.WriteRune(rune(unit))
		case '\n':
			out.WriteString(`\line `)
		case '\r':
		case '\t':
			out.WriteString(`\tab `)
		default:
			if unit >= 32 && unit < 127 {
				out.WriteByte(byte(unit))
			} else {
				fmt.Fprintf(&out, `\u%d?`, int16(unit))
			}
		}
	}
	return out.String()
}
func Rich(report runs.Report, parts []PNGPart) (RichContent, error) {
	fail := func() (RichContent, error) {
		return RichContent{}, problem.Invalid("parts", "Некорректные или неполные изображения отчёта")
	}
	allowed := map[string]bool{}
	for _, node := range report.Document {
		if id := GroupID(node); id != "" {
			allowed[id] = true
		}
	}
	if len(parts) > 512 {
		return fail()
	}
	groups := map[string][]PNGPart{}
	totalBytes := 0
	for _, part := range parts {
		if !allowed[part.GroupID] || part.Part < 1 || part.Total < 1 || part.Total > 512 || part.Part > part.Total || part.Part != len(groups[part.GroupID])+1 {
			return fail()
		}
		if len(part.DataBase64) > 12<<20 {
			return fail()
		}
		data, err := base64.StdEncoding.Strict().DecodeString(part.DataBase64)
		if err != nil {
			return fail()
		}
		totalBytes += len(data)
		if totalBytes > 24<<20 {
			return fail()
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 1600 || config.Height > 4096 || config.Width*config.Height > 6500000 {
			return fail()
		}
		if _, err = png.Decode(bytes.NewReader(data)); err != nil {
			return fail()
		}
		groups[part.GroupID] = append(groups[part.GroupID], part)
	}
	for id := range allowed {
		g := groups[id]
		if len(g) == 0 {
			return fail()
		}
		for _, p := range g {
			if p.Total != len(g) {
				return fail()
			}
		}
	}
	var h, r strings.Builder
	h.WriteString(`<html><body style="font-family:Arial,sans-serif;color:#20372e;background:#fff">`)
	r.WriteString(`{\rtf1\ansi\ansicpg1252\deff0{\fonttbl{\f0 Arial;}{\f1 Courier New;}}{\colortbl;\red197\green203\blue208;\red36\green120\blue55;\red170\green50\blue45;}\uc1\f0\fs22 `)
	rendered := map[string]bool{}
	for _, node := range report.Document {
		if group := GroupID(node); group != "" {
			if rendered[group] {
				continue
			}
			rendered[group] = true
			h.WriteString("<p><strong>" + html.EscapeString(node.Text) + "</strong></p>")
			r.WriteString(`\par\b ` + rtfText(node.Text) + `\b0\par `)
			for _, part := range groups[group] {
				label := fmt.Sprintf("Часть %d/%d", part.Part, part.Total)
				h.WriteString("<p>" + label + `</p><p><img alt="` + html.EscapeString(label) + `" style="max-width:100%;height:auto" src="data:image/png;base64,` + part.DataBase64 + `" /></p>`)
				data, _ := base64.StdEncoding.DecodeString(part.DataBase64)
				config, _ := png.DecodeConfig(bytes.NewReader(data))
				width := config.Width * 15
				height := config.Height * 15
				if width > 9000 {
					height = height * 9000 / width
					width = 9000
				}
				if height > 13000 {
					width = width * 13000 / height
					height = 13000
				}
				r.WriteString(`\par ` + rtfText(label) + `\par `)
				fmt.Fprintf(&r, `{\pict\pngblip\picw%d\pich%d\picwgoal%d\pichgoal%d `, config.Width, config.Height, width, height)
				r.WriteString(hex.EncodeToString(data))
				r.WriteString("}\\par ")
			}
			continue
		}
		text := node.Text
		if node.Kind == "table" {
			text = strings.Join(TableLines(node), "\n")
		}
		escaped := html.EscapeString(text)
		if node.Kind == "heading" {
			if node.Status != "" {
				h.WriteString(`<table style="width:100%"><tr><td><h2>` + escaped + `</h2></td><td style="text-align:right">` + html.EscapeString(StepLabel(node.Status)) + `</td></tr></table>`)
				color := 1
				if node.Status == "passed" {
					color = 2
				} else if node.Status == "failed" {
					color = 3
				}
				fmt.Fprintf(&r, `\par\b\tqr\tx9000 %s\tab\cf%d %s\cf0\b0\par `, rtfText(text), color, rtfText(StepLabel(node.Status)))
			} else {
				h.WriteString("<h2>" + escaped + "</h2>")
				r.WriteString(`\par\b ` + rtfText(text) + `\b0\par `)
			}
		} else if node.Kind == "code" || node.Kind == "table" {
			h.WriteString(`<pre style="white-space:pre-wrap">` + escaped + `</pre>`)
			r.WriteString(`\par\f1 ` + rtfText(text) + `\f0\par `)
		} else {
			h.WriteString(`<p style="white-space:pre-wrap">` + escaped + `</p>`)
			r.WriteString(`\par ` + rtfText(text) + `\par `)
		}
	}
	h.WriteString("</body></html>")
	r.WriteString("}")
	return RichContent{Text: Plain(report), HTML: h.String(), RTF: r.String()}, nil
}
