//go:build integration

package service_test

import (
	"fmt"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHistoryPoliciesAndSelectedStep(t *testing.T) {
	fixture := authoringApp(t)
	ctx := fixture.ctx
	c, res, rr, e := fixture.c, fixture.res, fixture.engine.Runs, fixture.engine
	var err error
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer target.Close()
	f, _ := c.CreateFolder(ctx, "Проверки")
	tc, _ := c.CreateCase(ctx, f.ID, "")
	env, _ := res.SaveEnvironmentMetadata(ctx, "", "Окружение", "", 0)
	if _, err = res.SaveResource(ctx, "", env.ID, "TARGET", "http", 0, resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	code := 204
	block := func() catalog.Block {
		return catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	}
	steps := []catalog.Step{{ID: catalog.NewID(), Description: "Selected step", Blocks: []catalog.Block{block(), block()}}, {ID: catalog.NewID(), Description: "Selected step", Blocks: []catalog.Block{block()}}, {ID: catalog.NewID(), Description: "Selected step", Blocks: []catalog.Block{}}}
	tc, err = c.SaveCase(ctx, tc.ID, "", tc.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	key := catalog.NewID()
	run, err := e.StartRunWithPolicy(ctx, tc.ID, env.ID, tc.Revision, key, "unretained", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.StartRunWithPolicy(ctx, tc.ID, env.ID, tc.Revision, key, "retain", false); err == nil {
		t.Fatal("changed policy accepted")
	}
	batchKey := catalog.NewID()
	b, err := e.StartStep(ctx, run.ID, steps[0].ID, batchKey)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err = e.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Batches[0].State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(run.Attempts) != 2 || requests.Load() != 2 || run.Batches[0].StepID != steps[0].ID || run.Batches[0].State != "completed" || run.Batches[0].Problems != 1 {
		t.Fatalf("wrong selection: %d attempts %d calls", len(run.Attempts), requests.Load())
	}
	again, err := e.StartStep(ctx, run.ID, steps[0].ID, batchKey)
	if err != nil || again.ID != b.ID {
		t.Fatal("lost reply repeated", err)
	}
	empty, err := e.StartStep(ctx, run.ID, steps[2].ID, catalog.NewID())
	if err != nil || empty.State != "completed" || len(empty.Items) != 0 {
		t.Fatal("manual step", err)
	}
	if _, err = e.StartStep(ctx, run.ID, catalog.NewID(), catalog.NewID()); err == nil {
		t.Fatal("foreign step accepted")
	}
	run, err = e.FinishRun(ctx, run.ID)
	if err != nil || run.Status != "failed" || len(run.RetentionReasons) != 0 || run.HistoryPolicy != "unretained" {
		t.Fatal("unretained", err, run.Status, run.RetentionReasons)
	}
	if _, err = rr.Save(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	run, _ = rr.Get(ctx, run.ID)
	if strings.Join(run.RetentionReasons, ",") != "manual" {
		t.Fatal("manual save took first pin")
	}
	next, err := e.StartRun(ctx, tc.ID, env.ID, tc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	next, err = e.CancelRun(ctx, next.ID)
	if err != nil || strings.Join(next.RetentionReasons, ",") != "first_run" {
		t.Fatal("auto first run", err, next.RetentionReasons)
	}
	retained, err := e.StartRunWithPolicy(ctx, tc.ID, env.ID, tc.Revision, catalog.NewID(), "retain", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = rr.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	retained, err = rr.Get(ctx, retained.ID)
	if err != nil || retained.Status != "interrupted" || strings.Join(retained.RetentionReasons, ",") != "manual" {
		t.Fatal("retain recovery", err, retained.RetentionReasons)
	}
	// Pure retention selection is shared with PostgreSQL, including first success.
	if len(runs.PolicyReasons("unretained", "passed", false, false)) != 0 || strings.Join(runs.PolicyReasons("auto", "passed", false, false), ",") != "first_run,first_success" {
		t.Fatal("policy reasons")
	}
}

func TestResourceVariableDurabilityCancelAndRecovery(t *testing.T) {
	f := authoringApp(t)
	var sources, mains atomic.Int32
	var reject atomic.Bool
	entered := make(chan struct{}, 2)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sources.Add(1)
		if r.URL.Path == "/slow" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Write([]byte(`{"token":"fetched-sensitive-value"}`))
	}))
	defer source.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mains.Add(1); reject.Store(true); w.WriteHeader(204) }))
	defer target.Close()
	src := &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "none", JSONPointer: "/token", FetchMode: "per_run"}
	r, err := f.res.SaveResource(f.ctx, "", f.env.ID, "DURABLE", "http", 0, resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Variables: map[string]resources.VariableDefinition{"HTTP_VALUE": {Mode: "http", Source: src}}}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	c := f.caseInFolder(t)
	code := 204
	b := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: r.Name, TimeoutSeconds: 60, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", Headers: []catalog.RequestHeader{{Name: "X-Value", Value: "${HTTP_VALUE}"}}, BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	step := catalog.Step{ID: catalog.NewID(), Description: "Устойчивое выполнение", Blocks: []catalog.Block{b, b}}
	step.Blocks[1].ID = catalog.NewID()
	c, err = f.c.SaveCase(f.ctx, c.ID, c.Name, c.Revision, []catalog.Step{step})
	if err != nil {
		t.Fatal(err)
	}
	if f.fileStore != nil {
		f.fileStore.Failpoint = func(stage string) error {
			if stage == "before-replace" && reject.Load() {
				return fmt.Errorf("refused write")
			}
			return nil
		}
		t.Cleanup(func() { f.fileStore.Failpoint = nil })
	} else {
		_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_variable_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state NOT IN ('queued','running') THEN RAISE EXCEPTION 'refused write'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_variable_result BEFORE UPDATE ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.reject_variable_result()`), pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := f.engine.EditorAttempt(f.ctx, c.ID, b.ID, f.env.ID, c.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		a, err = f.engine.GetAttempt(f.ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if a.PersistencePending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !a.PersistencePending || sources.Load() != 1 || mains.Load() != 1 {
		t.Fatal("pending source result", a.State)
	}
	reject.Store(false)
	if f.fileStore == nil {
		_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_variable_result ON qag.call_attempts; DROP FUNCTION qag.reject_variable_result()`), pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			t.Fatal(err)
		}
	}
	a = awaitAuthoringAttempt(t, f, a)
	if a.State != "completed" || sources.Load() != 1 || mains.Load() != 1 {
		t.Fatal("persistence retried network")
	}
	if f.fileStore == nil {
		_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_variable_begin() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'refused write'; END $$; CREATE TRIGGER reject_variable_begin BEFORE INSERT ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.reject_variable_begin()`), pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		reject.Store(true)
	}
	if _, err = f.engine.EditorAttempt(f.ctx, c.ID, b.ID, f.env.ID, c.Revision, catalog.NewID()); err == nil || sources.Load() != 1 || mains.Load() != 1 {
		t.Fatal("refused registration used network")
	}
	if f.fileStore == nil {
		_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_variable_begin ON qag.call_attempts; DROP FUNCTION qag.reject_variable_begin()`), pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		reject.Store(false)
	}
	r.Config.Variables["HTTP_VALUE"].Source.URL = source.URL + "/slow"
	if _, err = f.res.SaveResource(f.ctx, r.ID, f.env.ID, r.Name, r.Kind, r.Revision, r.Config, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRunWithPolicy(f.ctx, c.ID, f.env.ID, c.Revision, catalog.NewID(), "unretained", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.StartStep(f.ctx, run.ID, step.ID, catalog.NewID()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-f.ctx.Done():
		t.Fatal("source not reached")
	}
	if _, err = f.engine.StartStep(f.ctx, run.ID, step.ID, catalog.NewID()); err == nil {
		t.Fatal("parallel step accepted")
	}
	if _, err = f.engine.RunAttempt(f.ctx, run.ID, b.ID, catalog.NewID()); err == nil {
		t.Fatal("parallel attempt accepted")
	}
	run, err = f.engine.CancelRun(f.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err = f.engine.GetRun(f.ctx, run.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if sources.Load() != 2 || mains.Load() != 1 || run.Batches[0].State != "cancelled" {
		t.Fatal("cancel replayed source/main")
	}
	next, err := f.engine.StartRunWithPolicy(f.ctx, c.ID, f.env.ID, c.Revision, catalog.NewID(), "retain", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Runs.StartBatch(f.ctx, next.ID, catalog.NewID(), "queued-before-restart", step.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.engine.Runs.Recover(f.ctx); err != nil {
		t.Fatal(err)
	}
	next, err = f.engine.GetRun(f.ctx, next.ID)
	if err != nil || next.Status != "interrupted" || next.Batches[0].StepID != step.ID || next.Batches[0].State != "interrupted" || sources.Load() != 2 {
		t.Fatal("selected queue replayed after recovery", err)
	}
}

func TestHistoryPolicyTerminalOutcomes(t *testing.T) {
	f := authoringApp(t)
	c := f.caseInFolder(t)
	step := catalog.Step{ID: catalog.NewID(), Description: "Ручное подтверждение"}
	c, err := f.c.SaveCase(f.ctx, c.ID, c.Name, c.Revision, []catalog.Step{step})
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"unretained", "auto", "retain"} {
		for _, status := range []string{"failed", "passed", "cancelled", "interrupted"} {
			key := catalog.NewID()
			run, err := f.engine.StartRunWithPolicy(f.ctx, c.ID, f.env.ID, c.Revision, key, policy, false)
			if err != nil {
				t.Fatal(err)
			}
			if policy == "auto" {
				again, e := f.engine.StartRun(f.ctx, c.ID, f.env.ID, c.Revision, key)
				if e != nil || again.ID != run.ID {
					t.Fatal("legacy/explicit auto differ", e)
				}
			}
			if status == "passed" || status == "failed" {
				if _, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, step.ID, status, "Проверено вручную", 1); err != nil {
					t.Fatal(err)
				}
				// Concurrent acknowledgements of the same terminal action never add
				// duplicate reasons or move first-success pins to another run.
				var group sync.WaitGroup
				for i := 0; i < 2; i++ {
					group.Add(1)
					go func() {
						defer group.Done()
						if _, e := f.engine.FinishRun(f.ctx, run.ID); e != nil {
							t.Error(e)
						}
					}()
				}
				group.Wait()
			} else if status == "cancelled" {
				_, err = f.engine.CancelRun(f.ctx, run.ID)
			} else {
				err = f.engine.Runs.Recover(f.ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			run, err = f.engine.Runs.Get(f.ctx, run.ID)
			if err != nil || run.Status != status {
				t.Fatal("terminal outcome", err, run.Status)
			}
			joined := strings.Join(run.RetentionReasons, ",")
			if policy == "unretained" && joined != "" {
				t.Fatal("unretained reasons", joined)
			}
			if policy == "retain" && joined != "manual" {
				t.Fatal("retain reason", joined)
			}
			if policy == "auto" && status == "failed" && joined != "first_run" {
				t.Fatal("first run consumed", joined)
			}
			if policy == "auto" && status == "passed" && joined != "first_success" {
				t.Fatal("first success consumed", joined)
			}
		}
	}
}
