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
	"time"

	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/reporting"
	"qag/internal/runs"
)

func TestReportProjectionAndUnconfirmedBatchResult(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"actual":"9007199254740993123.125","html":"<script>never execute</script>"}`))
	}))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/one", BodyMode: "none"}}, {HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/two", BodyMode: "none"}}})
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewNRGBA(image.Rect(0, 0, 3, 3)))
	asset, err := f.c.UploadImage(f.ctx, doc.ID, catalog.ImageUpload{Filename: "expected.png", DataBase64: base64.StdEncoding.EncodeToString(img.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "expected-private-sentinel-5731"
	doc.Steps[0].Expected = &catalog.StepExpectation{Text: expected, Images: []catalog.ImageRef{{ImageID: asset.ID, Caption: expected}}}
	for i := range doc.Steps[0].Blocks {
		doc.Steps[0].Blocks[i].Expected.Body = `{"expected":"` + expected + `"}`
		doc.Steps[0].Blocks[i].Expected.Images = []catalog.ImageRef{{ImageID: asset.ID, Caption: expected}}
	}
	doc, err = f.c.SaveCase(f.ctx, doc.ID, doc.Name, doc.Revision, doc.Steps)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_batch_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state NOT IN ('queued','running') THEN RAISE EXCEPTION 'write refused'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_batch_result BEFORE UPDATE ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.reject_batch_result()`), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	pending := false
	for time.Now().Before(until) {
		run, err = f.engine.GetRun(f.ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Attempts) > 0 && run.Attempts[0].PersistencePending {
			pending = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !pending || calls.Load() != 1 {
		t.Fatal("failed result write advanced batch")
	}
	snapshot, err := f.engine.Runs.ReportSnapshot(f.ctx, run.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Partial || snapshot.Steps[0].Calls[0].Attempts[0].Result != nil {
		t.Fatal("unconfirmed result exported")
	}
	cache := reporting.NewCache()
	if err = cache.Put(&snapshot); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_batch_result ON qag.call_attempts; DROP FUNCTION qag.reject_batch_result()`), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	run = waitBatch(t, f, run.ID)
	if calls.Load() != 2 {
		t.Fatal("external effect repeated after persistence retry")
	}
	old, err := cache.Get(snapshot.SnapshotID, snapshot.Hash, run.ID, true)
	if err != nil || old.Steps[0].Calls[0].Attempts[0].Result != nil {
		t.Fatal("late result changed snapshot")
	}
	v, err := f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.engine.VariantAttempt(f.ctx, run.ID, v.ID, v.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	_ = awaitAuthoringAttempt(t, f, a)
	for _, include := range []bool{true, false} {
		report, err := f.engine.Runs.ReportSnapshot(f.ctx, run.ID, include)
		if err != nil {
			t.Fatal(err)
		}
		report.Document = reporting.BuildDocument(report)
		raw, _ := json.Marshal(report)
		if bytes.Contains(raw, []byte(expected)) != include {
			t.Fatal("expected projection leaked or lost expectation")
		}
		if include && (len(report.Images) != 1 || report.Images[0].Data == "") {
			t.Fatal("image snapshot incomplete")
		}
		if !include && (len(report.Images) != 0 || bytes.Contains(raw, []byte(`"expected"`))) {
			t.Fatal("expected fields present when disabled")
		}
		if !bytes.Contains(raw, []byte("9007199254740993123.125")) || !strings.Contains(reporting.Plain(report), "Исследовательский") {
			t.Fatal("actual precision/variant lost")
		}
		if bytes.Contains(raw, []byte("fixture-auth-password")) {
			t.Fatal("credentials in report")
		}
	}
	// Projection and cache operations never change the run or create network calls.
	before, _ := json.Marshal(run.Snapshot)
	afterCase, _ := f.c.GetCase(f.ctx, doc.ID)
	after, _ := json.Marshal(afterCase)
	if !bytes.Equal(before, after) {
		t.Fatal("report changed case")
	}
	if calls.Load() != 3 {
		t.Fatal("report made network calls")
	}
	f.api(t, "GET", "/runs/"+run.ID+"/report?includeExpected=false", nil, 200)
	f.api(t, "POST", "/runs/"+run.ID+"/report/clipboard", map[string]any{"snapshotId": snapshot.SnapshotID, "hash": snapshot.Hash, "includeExpected": true, "parts": []any{}}, 409)
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DELETE FROM qag.expected_images WHERE id=$1`), asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Runs.ReportSnapshot(f.ctx, run.ID, true); err == nil {
		t.Fatal("missing expected image silently omitted")
	}
	if _, err = f.engine.Runs.ReportSnapshot(f.ctx, run.ID, false); err != nil {
		t.Fatal("expected-off report read an expected image", err)
	}
}

func TestVariantLimitOwnershipAndDatabaseFences(t *testing.T) {
	f := authoringApp(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) }))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}}})
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	var first runs.Variant
	for i := 0; i < 20; i++ {
		v, err := f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "", 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = v
		}
	}
	if _, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "", 0, nil); err == nil {
		t.Fatal("variant limit ignored")
	}
	if _, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, catalog.NewID(), "", "", 0, nil); err == nil {
		t.Fatal("foreign block accepted")
	}
	payload := first.Versions[0].Request
	if _, err = f.engine.Runs.SaveVariant(f.ctx, catalog.NewID(), "", first.ID, "foreign", first.Revision, &payload); err == nil {
		t.Fatal("foreign run accepted")
	}
	// All composite version references must belong to the original run and block.
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `UPDATE qag.run_call_variants SET current_version_id=$2 WHERE id=$1`), first.ID, catalog.NewID())
	if err == nil {
		t.Fatal("foreign version FK accepted")
	}
	if _, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", first.ID, "Changed", first.Revision, &payload); err != nil {
		t.Fatal("failed FK update damaged variant")
	}
}
