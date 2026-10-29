//go:build integration

package service_test

import (
	"bytes"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/httpapi"
	"qag/internal/runs"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func stepRevisions(run runs.Run) []runs.StepRevision {
	r := make([]runs.StepRevision, 0, len(run.StepStates))
	for _, s := range run.StepStates {
		r = append(r, runs.StepRevision{StepID: s.StepID, Revision: s.Revision})
	}
	return r
}
func manualRun(t *testing.T, f *authoringFixture) runs.Run {
	t.Helper()
	doc := f.caseInFolder(t)
	steps := []catalog.Step{}
	for i := 0; i < 3; i++ {
		steps = append(steps, catalog.Step{ID: catalog.NewID(), Description: "Manual action", Blocks: []catalog.Block{}})
	}
	doc, err := f.c.SaveCase(f.ctx, doc.ID, doc.Name, doc.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestPassAllIsAtomicAndKeepsCommentsAndHTTPGuards(t *testing.T) {
	f := authoringApp(t)
	run := manualRun(t, f)
	var err error
	run, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, run.StepStates[0].StepID, "failed", "Saved failure", 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, run.StepStates[1].StepID, "passed", "Saved success", 1)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]runs.StepState(nil), run.StepStates...)
	body := httpapi.StepsPassAll{Steps: stepRevisions(run)}
	path := "/runs/" + run.ID + "/steps/pass-all"
	for _, bad := range [][]runs.StepRevision{body.Steps[:2], {body.Steps[0], body.Steps[0], body.Steps[2]}, {{StepID: catalog.NewID(), Revision: 1}, body.Steps[1], body.Steps[2]}, {{StepID: run.StepStates[0].StepID, Revision: 1}, body.Steps[1], body.Steps[2]}} {
		want := 400
		if len(bad) == 3 && bad[0].StepID == run.StepStates[0].StepID && bad[0].Revision == 1 {
			want = 409
		}
		f.api(t, "POST", path, httpapi.StepsPassAll{Steps: bad}, want)
	}
	for _, field := range []string{"Origin", "X-QAG-CSRF"} {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", f.server.URL+"/api/v1"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", f.server.URL)
		req.Header.Set("X-QAG-CSRF", f.csrf)
		req.Header.Set(field, "invalid")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatal("browser guard accepted invalid ", field)
		}
	}
	fail := `CREATE FUNCTION qag.reject_pass_all() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced status failure'; END $$; CREATE TRIGGER reject_pass_all BEFORE UPDATE ON qag.run_steps FOR EACH ROW EXECUTE FUNCTION qag.reject_pass_all()`
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, fail), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", path, body, 503)
	current, err := f.engine.GetRun(f.ctx, run.ID)
	if err != nil || !reflect.DeepEqual(current.StepStates, before) {
		t.Fatal("failed operation partly committed", err)
	}
	if _, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_pass_all ON qag.run_steps; DROP FUNCTION qag.reject_pass_all()`), pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(f.api(t, "POST", path, body, 200), &current)
	if current.Status != "running" || len(current.Attempts) != 0 {
		t.Fatal("bulk finished or executed run")
	}
	for i, s := range current.StepStates {
		if s.Status != "passed" || s.Comment != before[i].Comment {
			t.Fatal("bulk lost comment")
		}
	}
	stable := current.StepStates
	json.Unmarshal(f.api(t, "POST", path, httpapi.StepsPassAll{Steps: stepRevisions(current)}, 200), &current)
	if !reflect.DeepEqual(stable, current.StepStates) {
		t.Fatal("no-op changed revision")
	}
	if _, err = f.engine.FinishRun(f.ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", path, httpapi.StepsPassAll{Steps: stepRevisions(current)}, 409)
}

func TestPassAllDoesNotExecuteOrOverrideOriginalFailure(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		w.Write([]byte(`{"ok":false}`))
	}))
	defer target.Close()
	doc := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}}})
	run, err := f.engine.StartRun(f.ctx, doc.ID, f.env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	run, err = f.engine.Runs.PassAll(f.ctx, run.ID, stepRevisions(run))
	if err != nil || calls.Load() != 0 {
		t.Fatal("bulk made external call", err)
	}
	a, err := f.engine.RunAttempt(f.ctx, run.ID, doc.Steps[0].Blocks[0].ID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	a = awaitAuthoringAttempt(t, f, a)
	if a.Result.Comparison.Status != "mismatch" {
		t.Fatal("original comparison lost")
	}
	run, err = f.engine.FinishRun(f.ctx, run.ID)
	if err != nil || run.Status != "failed" || calls.Load() != 1 {
		t.Fatal("manual pass overrode original failure", err)
	}
}

func TestPassAllSerializesWithStepEditsAndTerminalCommands(t *testing.T) {
	f := authoringApp(t)
	for _, kind := range []string{"edit", "finish", "cancel"} {
		for i := 0; i < 3; i++ {
			run := manualRun(t, f)
			var wg sync.WaitGroup
			wg.Add(2)
			var bulkErr, otherErr error
			go func() { defer wg.Done(); _, bulkErr = f.engine.Runs.PassAll(f.ctx, run.ID, stepRevisions(run)) }()
			go func() {
				defer wg.Done()
				switch kind {
				case "edit":
					_, otherErr = f.engine.Runs.UpdateStep(f.ctx, run.ID, run.StepStates[0].StepID, "failed", "Concurrent", 1)
				case "finish":
					_, otherErr = f.engine.FinishRun(f.ctx, run.ID)
				case "cancel":
					_, otherErr = f.engine.CancelRun(f.ctx, run.ID)
				}
			}()
			wg.Wait()
			result, err := f.engine.GetRun(f.ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			passed := 0
			for _, s := range result.StepStates {
				if s.Status == "passed" {
					passed++
				}
			}
			if passed != 0 && passed != 3 {
				t.Fatal("partially applied bulk")
			}
			if kind != "edit" && otherErr != nil {
				t.Fatal(otherErr)
			}
			if bulkErr != nil && otherErr != nil {
				t.Fatal("both serialized commands failed")
			}
		}
	}
}
