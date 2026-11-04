package reporting

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/runs"
)

func renderFixture(t *testing.T) runs.Report {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 80, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 80; x++ {
			im.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 5), 128, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, nil); err != nil {
		t.Fatal(err)
	}
	rows := [][]catalog.TypedValue{}
	for i := 0; i < 130; i++ {
		rows = append(rows, []catalog.TypedValue{{Type: "decimal", Value: "9007199254740993123.125"}, {Type: "null"}, {Type: "string", Value: ""}})
	}
	return runs.Report{Title: "Проверка кириллицы", Images: []runs.ReportImage{{ID: "image", MIME: "image/jpeg", Width: 80, Height: 48, Data: base64.StdEncoding.EncodeToString(b.Bytes())}}, Document: []runs.ReportNode{
		{Kind: "heading", Text: "Проверка кириллицы", Level: 1},
		{Kind: "heading", Text: "Шаг с ручной отметкой", Level: 2, Status: "passed"},
		{Kind: "heading", Text: "Исходный вызов", Level: 3, CallID: "call"},
		{Kind: "code", Text: "<script>literal</script>\n```\n" + strings.Repeat(`{"decimal":9007199254740993123.125,"text":"длинная строка"}`, 70) + "\nJSON-END", CallID: "call"},
		{Kind: "image", Text: "Эталон JPEG", ImageID: "image", CallID: "call"},
		{Kind: "table", Columns: []catalog.Column{{Name: "точное", Type: "numeric"}, {Name: "null", Type: "text"}, {Name: "пустое", Type: "text"}}, Rows: rows, CallID: "call"},
		{Kind: "text", Text: "TABLE-END", CallID: "call"},
	}}
}

func TestServerRenderersPreserveFullContentAndPortableImages(t *testing.T) {
	r := renderFixture(t)
	assets := os.DirFS("../../web/public")
	md, err := Markdown(r)
	if err != nil || !strings.Contains(md, "````\n") || !strings.Contains(md, "9007199254740993123.125 | NULL | \"\"") || !strings.Contains(md, "data:image/jpeg;base64,") {
		t.Fatalf("markdown: %v", err)
	}
	parts, err := CallPNGs(context.Background(), assets, r)
	if err != nil || len(parts) < 2 {
		t.Fatalf("PNG pagination: %v, %d", err, len(parts))
	}
	for i, part := range parts {
		data, e := base64.StdEncoding.DecodeString(part.DataBase64)
		if e != nil {
			t.Fatal(e)
		}
		im, e := png.Decode(bytes.NewReader(data))
		if e != nil || im.Bounds().Dx() != 1120 || im.Bounds().Dy() > 1924 {
			t.Fatalf("part %d: %v", i, e)
		}
	}
	for _, format := range []string{"pdf", "text", "markdown", "rich"} {
		files, err := Render(context.Background(), assets, r, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if len(files) == 0 {
			t.Fatal("empty export")
		}
		for _, f := range files {
			if len(f.Data) == 0 {
				t.Fatal("empty file")
			}
			if f.MIMEType == "application/pdf" && !bytes.HasPrefix(f.Data, []byte("%PDF-")) {
				t.Fatal("invalid PDF")
			}
			if f.MIMEType == "text/html; charset=utf-8" && (!bytes.Contains(f.Data, []byte("data:image/png;base64,")) || bytes.Contains(f.Data, []byte("<script>"))) {
				t.Fatal("unsafe/nonportable HTML")
			}
			if dir := os.Getenv("QAG_RENDER_FIXTURE_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, f.Name), f.Data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestRenderCancellationCorruptImageAndSlotCleanup(t *testing.T) {
	r := renderFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if files, err := Render(ctx, os.DirFS("../../web/public"), r, "pdf"); err == nil || files != nil {
		t.Fatal("cancelled PDF published")
	}
	r.Images[0].Data = "not base64"
	for _, format := range []string{"pdf", "rich"} {
		if files, err := Render(context.Background(), os.DirFS("../../web/public"), r, format); err == nil || files != nil {
			t.Fatal("corrupt image published")
		}
	}
	s := NewService(nil, nil, nil, false)
	r.Images = nil
	r.Document = nil
	r.RunID = "run"
	r.SnapshotID = "snapshot"
	if err := s.Cache.Put(&r); err != nil {
		t.Fatal(err)
	}
	ref := Reference{RunID: r.RunID, SnapshotID: r.SnapshotID, Hash: r.Hash}
	active, cancelActive := context.WithCancel(context.Background())
	entered := make(chan struct{}, 2)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := s.WithReport(active, ref, func(ctx context.Context, _ runs.Report) ([]File, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			})
			done <- err
		}()
	}
	<-entered
	<-entered
	if _, err := s.Export(context.Background(), ref, "text"); err == nil {
		t.Fatal("third render admitted")
	}
	cancelActive()
	for i := 0; i < 2; i++ {
		if err := <-done; err == nil {
			t.Fatal("active render ignored cancellation")
		}
	}
	if _, err := s.Export(ctx, ref, "text"); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := s.Export(context.Background(), ref, "text"); err != nil {
		t.Fatal("slot leaked", err)
	}
	b := limitedBuffer{ctx: context.Background(), limit: 4}
	if _, err := b.Write([]byte("12345")); err == nil || b.Len() != 0 {
		t.Fatal("partial oversized write")
	}
	if lines, err := wrapped(context.Background(), strings.Repeat("\n", maxWrappedLines+1), 100, func(string) (float64, error) { return 1, nil }); err == nil || lines != nil {
		t.Fatal("unbounded newline expansion")
	}
}

func TestWebPReferencesRenderWithEmbeddedFonts(t *testing.T) {
	data, err := os.ReadFile("../catalog/testdata/pixel.webp")
	if err != nil {
		t.Fatal(err)
	}
	im, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r := runs.Report{Images: []runs.ReportImage{{ID: "webp", MIME: "image/webp", Width: im.Bounds().Dx(), Height: im.Bounds().Dy(), Data: base64.StdEncoding.EncodeToString(data)}}, Document: []runs.ReportNode{{Kind: "image", ID: "webp", Text: "Эталон WebP", ImageID: "webp"}}}
	for _, format := range []string{"pdf", "rich"} {
		if files, err := Render(context.Background(), os.DirFS("../../web/public"), r, format); err != nil || len(files) == 0 {
			t.Fatalf("%s WebP: %v", format, err)
		}
	}
}
