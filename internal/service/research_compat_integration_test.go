//go:build integration

package service_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/comparison"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/reporting"
)

func TestLegacyResearchProjectionNeverRewritesOrExportsExpectations(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"n": json.Number("9007199254740993123.125"), "reflected": r.Header.Get("Authorization")})
	}))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/research", BodyMode: "none"}}})
	var picture bytes.Buffer
	png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 2, 2)))
	asset, err := f.c.UploadImage(f.ctx, doc.ID, catalog.ImageUpload{Filename: "legacy.png", DataBase64: base64.StdEncoding.EncodeToString(picture.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	doc.Steps[0].Blocks[0].HTTP.Headers = []catalog.RequestHeader{{Name: "Authorization", Value: "${CLIENT_SECRET}"}}
	doc.Steps[0].Blocks[0].Expected.Images = []catalog.ImageRef{{ImageID: asset.ID, Caption: "original-only-image"}}
	doc, err = f.c.SaveCase(f.ctx, doc.ID, doc.Name, doc.Revision, doc.Steps)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "Research", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var newHasExpected bool
	if err = f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT request ? 'expected' FROM qag.run_call_variant_versions WHERE id=$1`), v.CurrentVersionID).Scan(&newHasExpected); err != nil || newHasExpected {
		t.Fatal("new research JSON inherited expectation", err)
	}
	a, err := f.engine.VariantAttempt(f.ctx, run.ID, v.ID, v.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	a = awaitAuthoringAttempt(t, f, a)
	if a.State != "completed" || a.Result.StatusCode == nil || *a.Result.StatusCode != 404 || a.Result.Comparison.Status != "not_compared" || !a.Result.Redacted {
		t.Fatal("research result lost protocol/redaction")
	}

	// Materialize the exact shape that the previous version stored, only in this
	// manifest-owned fixture. Every subsequent read/export must leave it intact.
	legacy := catalog.RequestFromBlock(doc.Steps[0].Blocks[0], true)
	legacy.Expected.Body = `{"legacy":"${EXPECTED_ONLY_MISSING}"}`
	result := *a.Result
	result.Comparison = comparison.Result{Status: "mismatch", Reason: "legacy-reason-sentinel", Differences: []comparison.Difference{{Path: "legacy-diff", Expected: "legacy-expected-sentinel", Actual: "actual"}}}
	envelope := map[string]any{"block": legacy, "resource": a.Resource, "environmentName": a.EnvironmentName}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `UPDATE qag.run_call_variant_versions SET request=$2 WHERE id=$1`), v.CurrentVersionID, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `UPDATE qag.call_attempts SET safe_request=$2,result=$3 WHERE id=$1`), a.ID, envelope, result); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		t.Helper()
		var hash string
		err := f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT md5(v.request::text||a.safe_request::text||a.result::text||r.snapshot::text) FROM qag.run_call_variant_versions v,qag.call_attempts a,qag.runs r WHERE v.id=$1 AND a.id=$2 AND r.id=$3`), v.CurrentVersionID, a.ID, run.ID).Scan(&hash)
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	before := fingerprint()
	old, err := f.engine.GetAttempt(f.ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(old)
	if old.Request.Expected != nil || old.Result.Comparison.Status != "not_compared" || bytes.Contains(wire, []byte("legacy-")) || bytes.Contains(wire, []byte("EXPECTED_ONLY")) {
		t.Fatal("legacy expectation escaped public projection")
	}
	// Even a legacy expectation containing a missing variable cannot prevent the
	// next explicit request. The persisted version and first attempt stay exact.
	newer, err := f.engine.VariantAttempt(f.ctx, run.ID, v.ID, v.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	newer = awaitAuthoringAttempt(t, f, newer)
	if newer.Request.Expected != nil || newer.Result.Comparison.Status != "not_compared" || newer.VariantVersionID != v.CurrentVersionID {
		t.Fatal("legacy research executed with expectation")
	}
	for _, include := range []bool{true, false} {
		report, err := f.engine.Runs.ReportSnapshot(f.ctx, run.ID, include)
		if err != nil {
			t.Fatal(err)
		}
		research := report.Steps[0].Calls[0].Variants[0]
		raw, _ := json.Marshal(research)
		if bytes.Contains(raw, []byte(`"expected"`)) || bytes.Contains(raw, []byte("legacy-")) || bytes.Contains(raw, []byte("fixture-auth-password")) {
			t.Fatal("research report leaked expectation or secret")
		}
		for _, attempt := range research.Attempts {
			if attempt.Expected != nil || attempt.Result.Comparison.Status != "not_compared" || len(attempt.Result.Comparison.Differences) != 0 {
				t.Fatal("research report compared")
			}
		}
		for _, node := range reporting.BuildDocument(report) {
			if strings.HasPrefix(node.CallID, "version:") || node.CallID == "attempt:"+a.ID || node.CallID == "attempt:"+newer.ID {
				if strings.Contains(node.Text, "Ожида") || strings.Contains(node.Text, "Сравнение:") || strings.Contains(node.Text, "legacy-") || node.Kind == "image" {
					t.Fatal("research document inherited parent expectation")
				}
			}
		}
	}
	if fingerprint() != before || calls.Load() != 2 {
		t.Fatal("projection rewrote history or repeated request")
	}
}
