package reporting

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"strings"

	"qag/internal/problem"
	"qag/internal/runs"
)

const MaxRenderedBytes = 64 << 20
const maxWrappedLines = 250000

type limitedBuffer struct {
	bytes.Buffer
	ctx   context.Context
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.Len()+len(p) > b.limit {
		return 0, problem.Invalid("report", "Файл отчёта превышает допустимый предел")
	}
	return b.Buffer.Write(p)
}
func Render(ctx context.Context, assets fs.FS, r runs.Report, format string) ([]File, error) {
	var files []File
	switch format {
	case "text":
		files = []File{{Name: "qag-report.txt", MIMEType: "text/plain; charset=utf-8", Data: []byte(Plain(r))}}
	case "markdown":
		text, err := Markdown(r)
		if err != nil {
			return nil, err
		}
		files = []File{{Name: "qag-report.md", MIMEType: "text/markdown; charset=utf-8", Data: []byte(text)}}
	case "pdf":
		data, err := PDF(ctx, assets, r)
		if err != nil {
			return nil, err
		}
		files = []File{{Name: "qag-report.pdf", MIMEType: "application/pdf", Data: data}}
	case "rich":
		parts, err := CallPNGs(ctx, assets, r)
		if err != nil {
			return nil, err
		}
		rich, err := Rich(r, parts)
		if err != nil {
			return nil, err
		}
		files = []File{{Name: "qag-report.html", MIMEType: "text/html; charset=utf-8", Data: []byte(rich.HTML)}, {Name: "qag-report.rtf", MIMEType: "application/rtf", Data: []byte(rich.RTF)}, {Name: "qag-report.txt", MIMEType: "text/plain; charset=utf-8", Data: []byte(rich.Text)}}
	default:
		return nil, problem.Invalid("format", "Выберите pdf, text, markdown или rich")
	}
	for _, f := range files {
		if len(f.Data) > MaxRenderedBytes {
			return nil, problem.Invalid("report", "Файл отчёта превышает 64 MiB")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return files, nil
}
func reportImage(r runs.Report, id string) (image.Image, error) {
	for _, im := range r.Images {
		if im.ID == id {
			data, err := base64.StdEncoding.Strict().DecodeString(im.Data)
			if err != nil {
				return nil, problem.Invalid("image", "Эталон повреждён")
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil || decoded.Bounds().Dx() != im.Width || decoded.Bounds().Dy() != im.Height {
				return nil, problem.Invalid("image", "Эталон повреждён")
			}
			return decoded, nil
		}
	}
	return nil, problem.Invalid("image", "Эталон недоступен")
}
func wrapped(ctx context.Context, text string, width float64, measure func(string) (float64, error)) ([]string, error) {
	lines := []string{}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    ")
	// Iterate without allocating a slice proportional to attacker-controlled
	// newline count. The bound is checked before retaining another output line.
	for source := range strings.SplitSeq(text, "\n") {
		var b strings.Builder
		var used float64
		for _, ch := range source {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			w, err := measure(string(ch))
			if err != nil {
				return nil, err
			}
			if w > width {
				return nil, problem.Invalid("report", "Символ не помещается в колонку отчёта")
			}
			if used+w > width && b.Len() > 0 {
				if len(lines) >= maxWrappedLines {
					return nil, problem.Invalid("report", "Текстовый блок превышает 250000 строк")
				}
				lines = append(lines, b.String())
				b.Reset()
				used = 0
			}
			b.WriteRune(ch)
			used += w
		}
		if len(lines) >= maxWrappedLines {
			return nil, problem.Invalid("report", "Текстовый блок превышает 250000 строк")
		}
		lines = append(lines, b.String())
	}
	return lines, nil
}
