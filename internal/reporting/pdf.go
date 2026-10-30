package reporting

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"io/fs"
	"strings"

	"github.com/signintech/gopdf"
	"qag/internal/catalog"
	"qag/internal/problem"
	"qag/internal/runs"
)

const pdfMargin = 42.
const pdfWidth = 595.28
const pdfBottom = 799.89

type pdfReport struct {
	pdf     gopdf.GoPdf
	ctx     context.Context
	y       float64
	missing bool
}

func (p *pdfReport) page() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if p.pdf.GetNumberOfPages() >= 5000 {
		return problem.Invalid("report", "PDF превышает 5000 страниц")
	}
	p.pdf.AddPage()
	p.y = pdfMargin
	return nil
}
func (p *pdfReport) ensure(h float64) error {
	if p.y+h > pdfBottom {
		return p.page()
	}
	return p.ctx.Err()
}
func (p *pdfReport) setFont(mono bool, size float64) error {
	name := "text"
	if mono {
		name = "mono"
	}
	return p.pdf.SetFont(name, "", size)
}
func (p *pdfReport) measure(s string) (float64, error) {
	w, err := p.pdf.MeasureTextWidth(s)
	if p.missing {
		return 0, problem.Invalid("font", "Шрифт не содержит символ отчёта")
	}
	return w, err
}
func (p *pdfReport) text(text string, size float64, mono bool, width float64) error {
	if err := p.setFont(mono, size); err != nil {
		return err
	}
	lines, err := wrapped(p.ctx, text, width, p.measure)
	if err != nil {
		return err
	}
	lineHeight := size * 1.5
	for _, line := range lines {
		if err = p.ensure(lineHeight); err != nil {
			return err
		}
		p.pdf.SetXY(pdfMargin, p.y)
		if err = p.pdf.Cell(&gopdf.Rect{W: width, H: lineHeight}, line); err != nil {
			return err
		}
		p.y += lineHeight
	}
	p.y += 5
	return nil
}
func (p *pdfReport) mark(status string) {
	x, y := pdfWidth-pdfMargin-6, p.y+7
	p.pdf.SetLineWidth(1.6)
	switch status {
	case "passed":
		p.pdf.SetStrokeColor(36, 120, 55)
		p.pdf.Line(x-5, y, x-1, y+4)
		p.pdf.Line(x-1, y+4, x+6, y-5)
	case "failed":
		p.pdf.SetStrokeColor(170, 50, 45)
		p.pdf.Line(x-4, y-4, x+4, y+4)
		p.pdf.Line(x-4, y+4, x+4, y-4)
	default:
		p.pdf.SetStrokeColor(197, 203, 208)
		p.pdf.Oval(x-3, y-3, x+3, y+3)
	}
	p.pdf.SetStrokeColor(205, 210, 215)
	p.pdf.SetLineWidth(.5)
}
func (p *pdfReport) table(node runs.ReportNode) error {
	if len(node.Columns) == 0 {
		return p.text("(нет колонок)", 9, true, pdfWidth-2*pdfMargin)
	}
	for first := 0; first < len(node.Columns); first += 8 {
		last := min(first+8, len(node.Columns))
		columns := node.Columns[first:last]
		width := (pdfWidth - 2*pdfMargin) / float64(len(columns))
		size, lineHeight := 8., 12.
		if len(node.Columns) > 8 {
			if err := p.text(fmt.Sprintf("Колонки %d–%d из %d", first+1, last, len(node.Columns)), 9, false, pdfWidth-2*pdfMargin); err != nil {
				return err
			}
		}
		if err := p.setFont(true, size); err != nil {
			return err
		}
		wrapCells := func(values []string) ([][]string, int, error) {
			out := [][]string{}
			count := 1
			for _, v := range values {
				ls, e := wrapped(p.ctx, v, width-8, p.measure)
				if e != nil {
					return nil, 0, e
				}
				out = append(out, ls)
				count = max(count, len(ls))
			}
			return out, count, nil
		}
		head := []string{}
		for _, c := range columns {
			head = append(head, c.Name+" ("+c.Type+")")
		}
		headers, headLines, err := wrapCells(head)
		if err != nil {
			return err
		}
		headHeight := float64(headLines)*lineHeight + 8
		if headHeight > pdfBottom-pdfMargin-30 {
			return problem.Invalid("report", "Заголовок SQL-таблицы слишком велик для PDF")
		}
		drawRow := func(cells [][]string, offset, count int) error {
			if err := p.ctx.Err(); err != nil {
				return err
			}
			h := float64(count)*lineHeight + 8
			for col, lines := range cells {
				x := pdfMargin + float64(col)*width
				p.pdf.RectFromUpperLeft(x, p.y, width, h)
				for n := 0; n < count; n++ {
					if offset+n >= len(lines) {
						continue
					}
					p.pdf.SetXY(x+4, p.y+4+float64(n)*lineHeight)
					if e := p.pdf.Cell(&gopdf.Rect{W: width - 8, H: lineHeight}, lines[offset+n]); e != nil {
						return e
					}
				}
			}
			p.y += h
			return nil
		}
		drawHeader := func() error {
			if err := p.ensure(headHeight + lineHeight + 8); err != nil {
				return err
			}
			return drawRow(headers, 0, headLines)
		}
		if err = drawHeader(); err != nil {
			return err
		}
		for _, row := range node.Rows {
			values := []string{}
			for col := first; col < last; col++ {
				value := catalog.TypedValue{Type: "null"}
				if col < len(row) {
					value = row[col]
				}
				values = append(values, Value(value))
			}
			cells, count, e := wrapCells(values)
			if e != nil {
				return e
			}
			offset := 0
			for offset < count {
				available := int((pdfBottom - p.y - 8) / lineHeight)
				if available < 1 {
					if e = p.page(); e != nil {
						return e
					}
					if e = drawHeader(); e != nil {
						return e
					}
					available = int((pdfBottom - p.y - 8) / lineHeight)
				}
				n := min(available, count-offset)
				if e = drawRow(cells, offset, n); e != nil {
					return e
				}
				offset += n
			}
		}
		p.y += 10
	}
	return nil
}
func PDF(ctx context.Context, assets fs.FS, r runs.Report) ([]byte, error) {
	p := &pdfReport{ctx: ctx}
	p.pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.pdf.SetInfo(gopdf.PdfInfo{Title: r.Title, Author: "qag"})
	for i, name := range []string{"NotoSans-Regular.ttf", "NotoSansMono-Regular.ttf"} {
		data, err := fs.ReadFile(assets, "fonts/"+name)
		if err != nil {
			return nil, problem.Invalid("font", "Шрифт отчёта недоступен")
		}
		family := "text"
		if i == 1 {
			family = "mono"
		}
		if err = p.pdf.AddTTFFontDataWithOption(family, data, gopdf.TtfOption{OnGlyphNotFound: func(rune) { p.missing = true }, OnGlyphNotFoundSubstitute: func(r rune) rune { return r }}); err != nil {
			return nil, err
		}
	}
	if err := p.page(); err != nil {
		return nil, err
	}
	p.pdf.SetTextColor(30, 40, 50)
	p.pdf.SetStrokeColor(205, 210, 215)
	p.pdf.SetLineWidth(.5)
	for _, n := range r.Document {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n.Kind == "table" {
			if err := p.table(n); err != nil {
				return nil, err
			}
			continue
		}
		if n.Kind == "image" {
			im, err := reportImage(r, n.ImageID)
			if err != nil {
				return nil, err
			}
			if n.Text != "" {
				if err = p.text(n.Text, 9, false, pdfWidth-2*pdfMargin); err != nil {
					return nil, err
				}
			}
			bounds := im.Bounds()
			scale := min((pdfWidth-2*pdfMargin)/float64(bounds.Dx()), 500./float64(bounds.Dy()), 1.)
			w, h := float64(bounds.Dx())*scale, float64(bounds.Dy())*scale
			if err = p.ensure(h + 12); err != nil {
				return nil, err
			}
			// Normalize JPEG YCbCr and WebP images to an explicit 8-bit surface.
			// image/png otherwise may choose a 16-bit encoding rejected by gopdf.
			raster := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
			draw.Draw(raster, raster.Bounds(), im, bounds.Min, draw.Src)
			if err = p.pdf.ImageFrom(raster, pdfMargin, p.y, &gopdf.Rect{W: w, H: h}); err != nil {
				return nil, err
			}
			p.y += h + 12
			continue
		}
		size := 10.
		if n.Kind == "heading" {
			size = 11
			if n.Level == 1 {
				size = 18
			} else if n.Level == 2 {
				size = 14
			}
			if err := p.ensure(size * 2); err != nil {
				return nil, err
			}
		}
		width := pdfWidth - 2*pdfMargin
		if n.Status != "" {
			p.mark(n.Status)
			width -= 25
		}
		text := strings.ReplaceAll(n.Text, "\r\n", "\n")
		if err := p.text(text, size, n.Kind == "code", width); err != nil {
			return nil, err
		}
	}
	pages := p.pdf.GetNumberOfPages()
	for page := 1; page <= pages; page++ {
		if err := p.pdf.SetPage(page); err != nil {
			return nil, err
		}
		if err := p.setFont(false, 8); err != nil {
			return nil, err
		}
		p.pdf.SetXY(pdfMargin, 815)
		if err := p.pdf.Cell(&gopdf.Rect{W: pdfWidth - 2*pdfMargin, H: 12}, fmt.Sprintf("Страница %d из %d", page, pages)); err != nil {
			return nil, err
		}
	}
	if p.missing {
		return nil, problem.Invalid("font", "Шрифт не содержит символ отчёта")
	}
	out := &limitedBuffer{ctx: ctx, limit: MaxRenderedBytes}
	if err := p.pdf.Write(out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
