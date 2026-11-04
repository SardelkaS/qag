package reporting

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/runs"
)

type bitmapLine struct {
	text, imageID string
	heading       bool
	height        int
}

func loadFaces(assets fs.FS) (font.Face, font.Face, error) {
	faces := []font.Face{}
	ok := false
	defer func() {
		if !ok {
			for _, face := range faces {
				face.Close()
			}
		}
	}()
	for _, name := range []string{"NotoSans-Regular.ttf", "NotoSansMono-Regular.ttf"} {
		data, err := fs.ReadFile(assets, "fonts/"+name)
		if err != nil {
			return nil, nil, problem.Invalid("font", "Шрифт отчёта недоступен")
		}
		ttf, err := opentype.Parse(data)
		if err != nil {
			return nil, nil, err
		}
		face, err := opentype.NewFace(ttf, &opentype.FaceOptions{Size: 14, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, nil, err
		}
		faces = append(faces, face)
	}
	ok = true
	return faces[0], faces[1], nil
}
func CallPNGs(ctx context.Context, assets fs.FS, r runs.Report) ([]PNGPart, error) {
	regular, mono, err := loadFaces(assets)
	if err != nil {
		return nil, err
	}
	defer regular.Close()
	defer mono.Close()
	groups := map[string][]runs.ReportNode{}
	order := []string{}
	for _, n := range r.Document {
		if id := GroupID(n); id != "" {
			if _, ok := groups[id]; !ok {
				order = append(order, id)
			}
			groups[id] = append(groups[id], n)
		}
	}
	parts := []PNGPart{}
	total := 0
	for _, id := range order {
		lines := []bitmapLine{}
		for _, n := range groups[id] {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if n.Kind == "image" {
				if n.Text != "" {
					captions, e := wrapped(ctx, n.Text, 1072, func(s string) (float64, error) {
						w, ok := regular.GlyphAdvance([]rune(s)[0])
						if !ok {
							return 0, problem.Invalid("font", "Шрифт не содержит символ отчёта")
						}
						return float64(w) / 64, nil
					})
					if e != nil {
						return nil, e
					}
					if len(lines)+len(captions) > (512-len(parts))*88 {
						return nil, problem.Invalid("report", "Отчёт превышает 512 растровых частей")
					}
					for _, caption := range captions {
						lines = append(lines, bitmapLine{text: caption, heading: true, height: 21})
					}
				}
				found := false
				for _, im := range r.Images {
					if im.ID == n.ImageID {
						scale := min(1., 1072./float64(im.Width), 1500./float64(im.Height))
						lines = append(lines, bitmapLine{imageID: im.ID, height: max(1, int(float64(im.Height)*scale)) + 16})
						found = true
						break
					}
				}
				if !found {
					return nil, problem.Invalid("image", "Эталон недоступен")
				}
				continue
			}
			text := n.Text
			if n.Kind == "table" {
				text = strings.Join(TableLines(n), "\n")
			}
			face := mono
			if n.Kind == "heading" {
				face = regular
			}
			wrappedLines, e := wrapped(ctx, text, 1072, func(s string) (float64, error) {
				w, ok := face.GlyphAdvance([]rune(s)[0])
				if !ok {
					return 0, problem.Invalid("font", "Шрифт не содержит символ отчёта")
				}
				return float64(w) / 64, nil
			})
			if e != nil {
				return nil, e
			}
			if len(lines)+len(wrappedLines) > (512-len(parts))*88 {
				return nil, problem.Invalid("report", "Отчёт превышает 512 растровых частей")
			}
			for _, line := range wrappedLines {
				lines = append(lines, bitmapLine{text: line, heading: n.Kind == "heading", height: 21})
			}
			lines = append(lines, bitmapLine{height: 12})
		}
		chunks := [][]bitmapLine{{}}
		height := 0
		for _, line := range lines {
			if height+line.height > 1840 && len(chunks[len(chunks)-1]) > 0 {
				chunks = append(chunks, []bitmapLine{})
				height = 0
			}
			chunks[len(chunks)-1] = append(chunks[len(chunks)-1], line)
			height += line.height
		}
		if len(parts)+len(chunks) > 512 {
			return nil, problem.Invalid("report", "Отчёт превышает 512 растровых частей")
		}
		for index, chunk := range chunks {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			h := 84
			for _, line := range chunk {
				h += line.height
			}
			canvas := image.NewRGBA(image.Rect(0, 0, 1120, h))
			draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			drawer := font.Drawer{Dst: canvas, Src: image.NewUniform(color.RGBA{32, 55, 46, 255}), Face: regular, Dot: fixed.P(24, 28)}
			drawer.DrawString(fmt.Sprintf("Часть %d/%d", index+1, len(chunks)))
			y := 58
			for _, line := range chunk {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if line.imageID != "" {
					im, e := reportImage(r, line.imageID)
					if e != nil {
						return nil, e
					}
					bounds := im.Bounds()
					scale := min(1., 1072./float64(bounds.Dx()), 1500./float64(bounds.Dy()))
					w := max(1, int(float64(bounds.Dx())*scale))
					imgHeight := line.height - 16
					xdraw.CatmullRom.Scale(canvas, image.Rect(24, y, 24+w, y+imgHeight), im, bounds, draw.Over, nil)
				} else {
					drawer.Face = mono
					if line.heading {
						drawer.Face = regular
					}
					drawer.Dot = fixed.P(24, y+15)
					drawer.DrawString(line.text)
				}
				y += line.height
			}
			buffer := &limitedBuffer{ctx: ctx, limit: 12 << 20}
			encoder := png.Encoder{CompressionLevel: png.BestSpeed}
			if err = encoder.Encode(buffer, canvas); err != nil {
				return nil, err
			}
			total += buffer.Len()
			if total > 24<<20 {
				return nil, problem.Invalid("report", "PNG отчёта превышают 24 MiB")
			}
			parts = append(parts, PNGPart{GroupID: id, Part: index + 1, Total: len(chunks), DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes())})
		}
	}
	return parts, nil
}
