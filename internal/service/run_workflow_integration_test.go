//go:build integration

package service_test

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/resources"
	"qag/internal/runs"
)

func waitBatch(t *testing.T, f *authoringFixture, id string) runs.Run {
	t.Helper()
	until := time.Now().Add(12 * time.Second)
	for time.Now().Before(until) {
		run, err := f.engine.GetRun(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Batches) > 0 && run.Batches[len(run.Batches)-1].State != "running" {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("batch did not complete")
	return runs.Run{}
}

func TestAtomicBatchCommandConcurrencyAndCancelWriteFailure(t *testing.T) {
	f := authoringApp(t)
	entered := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		once.Do(func() { close(entered) })
		<-r.Context().Done()
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/one", BodyMode: "none"}}, {HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/two", BodyMode: "none"}}})
	sql := `CREATE FUNCTION qag.reject_batch_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test atomicity'; END $$; CREATE TRIGGER reject_batch_insert BEFORE INSERT ON qag.run_batches FOR EACH ROW EXECUTE FUNCTION qag.reject_batch_insert()`
	if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, sql), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	if _, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID(), true); err == nil {
		t.Fatal("failed batch insertion committed run")
	}
	var count int
	if err := f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT count(*) FROM qag.runs WHERE test_case_id=$1`), doc.ID).Scan(&count); err != nil || count != 0 || requests.Load() != 0 {
		t.Fatal("atomic preflight made run or network")
	}
	if _, err := f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_batch_insert ON qag.run_batches; DROP FUNCTION qag.reject_batch_insert()`), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	variant, err := f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := catalog.NewID()
	results := make(chan runs.Batch, 5)
	failures := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() { b, err := f.engine.StartBatch(f.ctx, run.ID, key); results <- b; failures <- err }()
	}
	var id string
	for i := 0; i < 5; i++ {
		b := <-results
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
		if id != "" && b.ID != id {
			t.Fatal("concurrent command created two queues")
		}
		id = b.ID
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("batch did not dispatch")
	}
	if _, err = f.engine.RunAttempt(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, catalog.NewID()); err == nil {
		t.Fatal("single call raced batch")
	}
	if _, err = f.engine.VariantAttempt(f.ctx, run.ID, variant.ID, variant.Revision, catalog.NewID()); err == nil {
		t.Fatal("variant raced batch")
	}
	sql = `CREATE FUNCTION qag.reject_run_cancel() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='cancelled' THEN RAISE EXCEPTION 'test cancel failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_run_cancel BEFORE UPDATE ON qag.runs FOR EACH ROW EXECUTE FUNCTION qag.reject_run_cancel()`
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, sql), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.CancelRun(f.ctx, run.ID); err == nil {
		t.Fatal("unconfirmed cancellation reported success")
	}
	current, err := f.engine.GetRun(f.ctx, run.ID)
	if err != nil || current.Status != "running" || current.Batches[0].State != "running" {
		t.Fatal("failed cancel did not roll back")
	}
	select {
	case <-stopped:
		t.Fatal("failed cancel stopped active context")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_run_cancel ON qag.runs; DROP FUNCTION qag.reject_run_cancel()`), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.CancelRun(f.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmed cancel did not stop request")
	}
	time.Sleep(150 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatal("next request started after confirmed cancel")
	}
	repeated, err := f.engine.StartBatch(f.ctx, run.ID, key)
	if err != nil || repeated.ID != id || repeated.State != "cancelled" {
		t.Fatal("lost command resumed cancelled queue")
	}
}
func workflowCase(t *testing.T, f *authoringFixture, target string, blocks []catalog.Block) catalog.TestCase {
	t.Helper()
	_, err := f.res.SaveResource(f.ctx, "", f.env.ID, "WORKFLOW", "http", 0, resources.Config{BaseURL: target, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	doc := f.caseInFolder(t)
	for i := range blocks {
		blocks[i].ID = catalog.NewID()
		blocks[i].Kind = "http"
		blocks[i].Resource = "WORKFLOW"
		code := 200
		blocks[i].Expected = catalog.Expectation{StatusCode: &code, BodyMode: "json", Body: `{"ok":true}`}
	}
	doc, err = f.c.SaveCase(f.ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: catalog.NewID(), Description: "Manual decision", Blocks: blocks}, {ID: catalog.NewID(), Description: "Manual only", Blocks: []catalog.Block{}}})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestRunBatchVariantsAndLegacyStatuses(t *testing.T) {
	f := authoringApp(t)
	var mu sync.Mutex
	paths := []string{}
	var failing atomic.Bool
	failing.Store(true)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/b" && failing.Load() {
			w.WriteHeader(500)
		}
		if r.URL.Path == "/variant" {
			w.Write([]byte(`{"ok":false}`))
		} else {
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer target.Close()
	blocks := []catalog.Block{}
	for _, path := range []string{"/a", "/b", "/c"} {
		blocks = append(blocks, catalog.Block{HTTP: &catalog.HTTPRequest{Method: "GET", Path: path, BodyMode: "none"}})
	}
	doc := workflowCase(t, f, target.URL, blocks)
	key := catalog.NewID()
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, key, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Batches) != 1 {
		t.Fatal("run and batch not atomic")
	}
	again, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, key, true)
	if err != nil || again.ID != run.ID || again.Batches[0].ID != run.Batches[0].ID {
		t.Fatal("lost command duplicated run/batch", err)
	}
	run = waitBatch(t, f, run.ID)
	if run.Status != "running" || run.Batches[0].Completed != 3 || run.Batches[0].Problems != 1 {
		t.Fatalf("batch result %+v", run.Batches)
	}
	for _, s := range run.StepStates {
		if s.Status != "not_run" {
			t.Fatal("batch changed manual decision")
		}
	}
	mu.Lock()
	order := append([]string{}, paths...)
	mu.Unlock()
	if !reflect.DeepEqual(order, []string{"/a", "/b", "/c"}) {
		t.Fatal("wrong order", order)
	}
	failing.Store(false)
	batchKey := catalog.NewID()
	batch, err := f.engine.StartBatch(f.ctx, run.ID, batchKey)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := f.engine.StartBatch(f.ctx, run.ID, batchKey)
	if err != nil || duplicate.ID != batch.ID {
		t.Fatal("batch repeated", err)
	}
	run = waitBatch(t, f, run.ID)
	if run.Batches[1].Completed != 3 || len(run.Attempts) != 6 {
		t.Fatal("all originals were not repeated")
	}
	variant, err := f.engine.Runs.SaveVariant(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := variant.Versions[0].Request
	payload.HTTP.BodyMode = "json"
	payload.HTTP.Body = "{unfinished"
	variant, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", variant.ID, "", variant.Revision, &payload)
	if err != nil {
		t.Fatal("raw draft rejected", err)
	}
	if _, err = f.engine.VariantAttempt(f.ctx, run.ID, variant.ID, variant.Revision, catalog.NewID()); err == nil {
		t.Fatal("invalid draft executed")
	}
	if _, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", variant.ID, "stale", 1, &payload); err == nil {
		t.Fatal("stale variant overwrote draft")
	}
	payload.HTTP.BodyMode = "none"
	payload.HTTP.Body = ""
	payload.HTTP.Path = "/variant"
	variant, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", variant.ID, "Research", variant.Revision, &payload)
	if err != nil {
		t.Fatal(err)
	}
	attemptKey := catalog.NewID()
	a, err := f.engine.VariantAttempt(f.ctx, run.ID, variant.ID, variant.Revision, attemptKey)
	if err != nil {
		t.Fatal(err)
	}
	a = awaitAuthoringAttempt(t, f, a)
	if a.Number != 1 || a.VariantVersionID != variant.CurrentVersionID || a.Result.Comparison.Status != "mismatch" {
		t.Fatal("variant result lost", a)
	}
	same, err := f.engine.VariantAttempt(f.ctx, run.ID, variant.ID, variant.Revision, attemptKey)
	if err != nil || same.ID != a.ID {
		t.Fatal("variant command repeated", err)
	}
	payload.HTTP.Path = "/a"
	variant, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", variant.ID, "Research changed", variant.Revision, &payload)
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.engine.GetAttempt(f.ctx, a.ID)
	if err != nil || old.Request.HTTP.Path != "/variant" || old.VariantVersionID == variant.CurrentVersionID {
		t.Fatal("old attempt rewritten")
	}
	if _, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, run.StepStates[0].StepID, "blocked", "", 1); err == nil {
		t.Fatal("legacy write accepted")
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `UPDATE qag.run_steps SET status='skipped' WHERE run_id=$1 AND step_id=$2`), run.ID, run.StepStates[0].StepID); err != nil {
		t.Fatal(err)
	}
	run, err = f.engine.GetRun(f.ctx, run.ID)
	if err != nil || run.StepStates[0].Status != "not_run" || run.StepStates[0].LegacyStatus != "skipped" {
		t.Fatal("legacy projection failed", err)
	}
	for _, s := range run.StepStates {
		if _, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, s.StepID, "passed", "manual", s.Revision); err != nil {
			t.Fatal(err)
		}
	}
	run, err = f.engine.FinishRun(f.ctx, run.ID)
	if err != nil || run.Status != "passed" {
		t.Fatal("research changed official outcome", err, run.Status)
	}
	if _, err = f.engine.Runs.SaveVariant(f.ctx, run.ID, "", variant.ID, "terminal", variant.Revision, &payload); err == nil {
		t.Fatal("terminal variant changed")
	}
	current, err := f.c.GetCase(f.ctx, doc.ID)
	before, _ := json.Marshal(doc)
	after, _ := json.Marshal(current)
	if err != nil || string(before) != string(after) {
		t.Fatal("workflow changed test case")
	}
}
func TestBatchCancellationStopsHeaderSourceAndQueue(t *testing.T) {
	f := authoringApp(t)
	entered := make(chan struct{})
	var once sync.Once
	var main atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			once.Do(func() { close(entered) })
			<-r.Context().Done()
			return
		}
		main.Add(1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()
	source := catalog.RequestHeader{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: target.URL + "/token", BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", FetchMode: "per_run"}}
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/main", BodyMode: "none", Headers: []catalog.RequestHeader{source}}}, {HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/next", BodyMode: "none"}}})
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not started")
	}
	if _, err = f.engine.FinishRun(f.ctx, run.ID); err == nil {
		t.Fatal("active batch finished")
	}
	if _, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, run.StepStates[0].StepID, "passed", "Keep this", 1); err != nil {
		t.Fatal(err)
	}
	run, err = f.engine.CancelRun(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "cancelled" || run.Batches[0].State != "cancelled" || run.StepStates[0].Comment != "Keep this" {
		t.Fatal("cancel lost state")
	}
	if _, err = f.engine.CancelRun(f.ctx, run.ID); err != nil {
		t.Fatal("cancel is not idempotent")
	}
	time.Sleep(250 * time.Millisecond)
	if main.Load() != 0 {
		t.Fatal("network continued after cancel")
	}
}
func TestBatchContinuesAfterTimeoutWithoutChangingManualStatus(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{TimeoutSeconds: 1, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/slow", BodyMode: "none"}}, {HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/fast", BodyMode: "none"}}})
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID(), true)
	if err != nil {
		t.Fatal(err)
	}
	run = waitBatch(t, f, run.ID)
	if calls.Load() != 2 || run.Batches[0].Completed != 2 || run.Batches[0].Problems != 1 || run.Attempts[0].State != "timed_out" || run.Attempts[1].State != "completed" {
		t.Fatal("timeout stopped or repeated batch")
	}
	if run.Status != "running" || run.StepStates[0].Status != "not_run" {
		t.Fatal("batch assessed manual steps")
	}
}
