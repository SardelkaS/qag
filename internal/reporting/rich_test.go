package reporting

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"github.com/SardelkaS/qag/internal/runs"
	"strings"
	"testing"
)

func TestRichEscapesTextAndEmbedsValidatedPNG(t *testing.T) {
	text := "Русский {\\rtf1} <script>alert('x')</script> ` ``` 🧪"
	report := runs.Report{Document: []runs.ReportNode{{Kind: "heading", Text: text, Status: "passed"}, {Kind: "heading", Text: "Исследовательский вызов", CallID: "attempt:one"}, {Kind: "code", Text: "body", CallID: "attempt:one"}}}
	var out bytes.Buffer
	_ = png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 2, 3)))
	parts := []PNGPart{{GroupID: "attempt:one", Part: 1, Total: 1, DataBase64: base64.StdEncoding.EncodeToString(out.Bytes())}}
	content, err := Rich(report, parts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content.HTML, "<script>") || !strings.Contains(content.HTML, "&lt;script&gt;") || !strings.Contains(content.HTML, "data:image/png;base64,") {
		t.Fatal("unsafe or incomplete HTML")
	}
	if strings.Contains(content.RTF, "{\\rtf1} ") || !strings.Contains(content.RTF, `\{\\rtf1\}`) || !strings.Contains(content.RTF, `\u1056?`) || !strings.Contains(content.RTF, `\pict\pngblip`) {
		t.Fatal("unsafe or incomplete RTF")
	}
	if !strings.Contains(content.Text, text) || strings.Contains(content.HTML, "src=\"http") || strings.Contains(content.HTML, "src=\"file:") {
		t.Fatal("plain text lost or external image link")
	}
	for _, bad := range [][]PNGPart{nil, {{GroupID: "foreign", Part: 1, Total: 1, DataBase64: parts[0].DataBase64}}, {{GroupID: "attempt:one", Part: 2, Total: 2, DataBase64: parts[0].DataBase64}}, {{GroupID: "attempt:one", Part: 1, Total: 1, DataBase64: base64.StdEncoding.EncodeToString([]byte("<svg/>"))}}} {
		if _, err = Rich(report, bad); err == nil {
			t.Fatal("invalid/missing/foreign parts accepted")
		}
	}
}
