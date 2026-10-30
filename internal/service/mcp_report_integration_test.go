//go:build integration

package service_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"image/png"
	"strings"
	"testing"

	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/mcpserver"
	"qag/internal/reporting"
	"qag/internal/runs"
)

func TestMCPLargePayloadsAndImmutableReportImages(t *testing.T) {
	f := authoringApp(t)
	s := connectMCP(t, f, "2026-07-28")
	c := f.caseInFolder(t)
	var im bytes.Buffer
	png.Encode(&im, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	// A valid PNG with trailing bytes exercises the exact 10 MiB upload bound.
	data := append(im.Bytes(), make([]byte, catalog.MaxImageBytes-im.Len())...)
	meta := mcpCall[catalog.ExpectedImage](t, f, s, "qag_image_upload", map[string]any{"testCaseId": c.ID, "filename": "ten-mib.png", "dataBase64": base64.StdEncoding.EncodeToString(data)})
	if meta.SizeBytes != catalog.MaxImageBytes {
		t.Fatal("image body was cut")
	}
	long := strings.Repeat("кириллица 9007199254740993123.125\n", 40000)
	steps := []catalog.Step{{ID: catalog.NewID(), Description: long, Blocks: []catalog.Block{}, Expected: &catalog.StepExpectation{Text: "immutable-reference", Images: []catalog.ImageRef{{ImageID: meta.ID, Caption: "old-caption"}}}}}
	c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": steps})
	out, err := mcpResult(t, f.ctx, s, "qag_test_case_get", map[string]any{"id": c.ID})
	if err != nil || out.Artifact == nil || len(out.Data) != 0 {
		t.Fatal("large case was not returned as artifact", err)
	}
	if _, err = s.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: out.Artifact.URI}); err == nil {
		t.Fatal("whole resource exceeded inline limit")
	}
	var got catalog.TestCase
	if json.Unmarshal(artifactBytes(t, f, s, *out.Artifact), &got) != nil || got.Steps[0].Description != long {
		t.Fatal("large UTF-8/numbers changed")
	}
	// Keep render fixtures small; old bytes must remain available even when the
	// current editable case no longer links the reference.
	steps[0].Description = "Ручная проверка"
	c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": steps})
	run := mcpCall[runs.Run](t, f, s, "qag_run_start", map[string]any{"testCaseId": c.ID, "environmentId": f.env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
	// Prepare directly because the 10 MiB image itself is also a large MCP report.
	r, err := f.reports.Prepare(f.ctx, run.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	ref := reporting.Reference{RunID: r.RunID, SnapshotID: r.SnapshotID, Hash: r.Hash, IncludeExpected: true}
	steps[0].Expected = nil
	c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": steps})
	for _, format := range []string{"pdf", "markdown", "rich"} {
		files, err := f.reports.Export(f.ctx, ref, format)
		if err != nil || len(files) == 0 {
			t.Fatalf("cached %s: %v", format, err)
		}
	}
	cached, err := f.reports.Cached(ref)
	if err != nil || cached.OwnerCaseID != c.ID || cached.Images[0].Data != "" {
		t.Fatal("private cache ownership/bytes", err)
	}
	raw, _ := json.Marshal(cached)
	if bytes.Contains(raw, []byte(c.ID)) {
		t.Fatal("private owner serialized")
	}
	if _, err = f.reports.Cached(reporting.Reference{RunID: catalog.NewID(), SnapshotID: r.SnapshotID, Hash: r.Hash, IncludeExpected: true}); err == nil {
		t.Fatal("foreign run snapshot accepted")
	}
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `UPDATE qag.expected_images SET data=set_byte(data,0,0) WHERE id=$1`), meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if files, err := f.reports.Export(f.ctx, ref, "pdf"); err == nil || files != nil {
		t.Fatal("corrupt reference published")
	}
	// An earlier failure releases both render slots and does not poison reads.
	if _, err = f.reports.Export(context.Background(), reporting.Reference{RunID: "wrong"}, "pdf"); err == nil {
		t.Fatal("unknown snapshot silently rebuilt")
	}
	mcpCall[mcpserver.Status](t, f, s, "qag_status", map[string]any{})
}
