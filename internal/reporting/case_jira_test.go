package reporting

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/ports"
)

// An unavailable embedded port makes any extra repository access fail the test.
type caseExportCatalog struct {
	ports.Catalog
	doc    catalog.TestCase
	images int
}

func (r *caseExportCatalog) GetCase(context.Context, string) (catalog.TestCase, error) {
	return r.doc, nil
}
func (r *caseExportCatalog) ReadImage(context.Context, string, string) (catalog.ExpectedImage, []byte, error) {
	r.images++
	return catalog.ExpectedImage{Filename: "Эталон [1].png"}, nil, nil
}
func TestJiraDefinitionProjection(t *testing.T) {
	source := &catalog.HTTPHeaderSource{Method: "POST", URL: "https://auth.invalid", BodyMode: "json", Body: `{"secret":"${CLIENT_SECRET}"}`, JSONPointer: "/token", FetchMode: "per_run", TLS: catalog.TLSConfig{Mode: "mtls", CA: "PRIVATE-CA", Certificate: "PRIVATE-CERT"}, Headers: []catalog.KeyValue{{Name: "Cookie", Value: "source-private-cookie"}}}
	repo := &caseExportCatalog{doc: catalog.TestCase{ID: catalog.NewID(), Name: "Кейс [a]|*b*", Revision: 3, ReadOnly: true, Steps: []catalog.Step{
		{ID: catalog.NewID(), Title: "Первый", Description: "Строка 1\n{noformat}\n{code:java}\n# [unsafe|url] \\ !image!", Expected: &catalog.StepExpectation{Text: "Ручное ожидание", Images: []catalog.ImageRef{{ImageID: catalog.NewID(), Caption: "Подпись"}}}, Blocks: []catalog.Block{
			{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "POST", Path: "/${TASK}", Query: []catalog.KeyValue{{Name: "n", Value: "9007199254740993123.125"}}, BodyMode: "json", Body: `{"n":9007199254740993123.125,"x":null,"empty":""}`, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer private-literal"}, {Name: "X-Api-Key", Value: "${API_KEY}"}, {Name: "X-Source", Source: source}}}, Expected: catalog.Expectation{BodyMode: "text", Body: "Ожидание HTTP", Headers: []catalog.KeyValue{{Name: "Set-Cookie", Value: "expected-private-cookie"}}}},
			{ID: catalog.NewID(), Kind: "grpc", Resource: "GRPC", GRPC: &catalog.GRPCRequest{Service: "pkg.Service", Method: "Method", Body: `{}`, Metadata: []catalog.RequestHeader{{Name: "authorization", Value: "grpc-private-token"}}}, Expected: catalog.Expectation{BodyMode: "json", Body: `{}`}},
			{ID: catalog.NewID(), Kind: "postgresql", Resource: "SQL", SQL: &catalog.SQLRequest{Query: "SELECT $1, $2, $3", Mode: "rows", Parameters: []catalog.TypedValue{{Type: "decimal", Value: "9007199254740993123.125"}, {Type: "null"}, {Type: "string"}}}, Expected: catalog.Expectation{BodyMode: "rows"}},
		}},
		{ID: catalog.NewID(), Description: "Неполный черновик", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http"}}},
	}}}
	got, err := ExportCase(context.Background(), repo, repo.doc.ID, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	golden := "testdata/test-case.jira.golden"
	if os.Getenv("QAG_UPDATE_JIRA_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got.Text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil || !bytes.Equal(want, []byte(got.Text)) {
		t.Fatal("Jira golden output changed", err)
	}
	for _, v := range []string{"h1. Кейс \\[a\\]\\|\\*b\\*", "9007199254740993123.125", "${TASK}", "${API_KEY}", "${CLIENT_SECRET}", "Ручное ожидание", "Приложите изображение", "pkg.Service", "SELECT $1", "Неполный черновик", `\{noformat\}`, `\{code:java\}`} {
		if !strings.Contains(got.Text, v) {
			t.Fatalf("missing %q", v)
		}
	}
	for _, v := range []string{"private-literal", "private-cookie", "private-token", "PRIVATE-CA", "PRIVATE-CERT", "localhost", "http://127.0.0.1", "first_run", "not_run"} {
		if strings.Contains(got.Text, v) {
			t.Fatalf("unexpected %q", v)
		}
	}
	if repo.doc.Steps[0].Blocks[0].HTTP.Headers[0].Value != "Bearer private-literal" {
		t.Fatal("mutated definition")
	}
	repo.images = 0
	without, err := ExportCase(context.Background(), repo, repo.doc.ID, 3, false)
	if err != nil || repo.images != 0 || strings.Contains(without.Text, "Ручное ожидание") || strings.Contains(without.Text, "Ожидание HTTP") {
		t.Fatal("includeExpected", err)
	}
	if _, err = ExportCase(context.Background(), repo, repo.doc.ID, 2, true); err == nil {
		t.Fatal("stale revision")
	}
	if _, err = ExportCase(context.Background(), repo, repo.doc.ID, 0, true); err == nil {
		t.Fatal("invalid revision")
	}
}
