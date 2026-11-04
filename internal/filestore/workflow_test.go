package filestore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/SardelkaS/qag/internal/service"
)

func await(t *testing.T, e *service.Engine, id string) execution.Attempt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a, err := e.GetAttempt(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if a.State != "running" && a.State != "queued" {
			return a
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("attempt timeout")
	return execution.Attempt{}
}
func TestEngineFrozenVersionsVariantsAndRetention(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "qag.yaml")
	s, err := filestore.Open(p, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, res, rr := &filestore.Catalog{Store: s}, &filestore.Resources{Store: s}, &filestore.Runs{Store: s}
	e := service.New(ctx, c, res, rr)
	defer e.Close()
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "private-token" {
			t.Error("missing frozen secret")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"n":9007199254740993123.125,"token":"private-token"}`))
	}))
	defer target.Close()
	f, err := c.CreateFolder(ctx, "Папка")
	if err != nil {
		t.Fatal(err)
	}
	tc, err := c.CreateCase(ctx, f.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironmentMetadata(ctx, "", "Старт", "Описание", 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := res.SaveResource(ctx, "", env.ID, "TARGET", "http", 0, resources.Config{BaseURL: target.URL, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "${TOKEN}"}}, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "private-token"}})
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	step := catalog.Step{ID: catalog.NewID(), Description: "Проверить", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}}}
	tc, err = c.SaveCase(ctx, tc.ID, "", tc.Revision, []catalog.Step{step})
	if err != nil {
		t.Fatal(err)
	}
	run, err := e.StartRun(ctx, tc.ID, env.ID, tc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = res.SaveResource(ctx, r.ID, env.ID, r.Name, r.Kind, r.Revision, r.Config, resources.SecretChanges{Replace: map[string]string{"TOKEN": "new-token"}})
	if err != nil {
		t.Fatal(err)
	}
	key := catalog.NewID()
	a, err := e.RunAttempt(ctx, run.ID, step.Blocks[0].ID, key)
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, e, a.ID)
	if done.State != "completed" || done.Result == nil || done.Result.Comparison.Status != "match" || !done.Result.Redacted {
		t.Fatalf("result: %+v", done)
	}
	again, err := e.RunAttempt(ctx, run.ID, step.Blocks[0].ID, key)
	if err != nil || again.ID != a.ID || calls.Load() != 1 {
		t.Fatal("idempotency", err)
	}
	v, err := rr.SaveVariant(ctx, run.ID, step.Blocks[0].ID, "", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	va, err := e.VariantAttempt(ctx, run.ID, v.ID, v.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	done = await(t, e, va.ID)
	if done.Request.Expected != nil || done.Result.Comparison.Status != "not_compared" {
		t.Fatal("research expectation")
	}
	run, err = rr.PassAll(ctx, run.ID, []runs.StepRevision{{StepID: step.ID, Revision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	run, err = e.FinishRun(ctx, run.ID)
	if err != nil || run.Status != "passed" || len(run.RetentionReasons) != 2 {
		t.Fatal(run.Status, err)
	}
	if err = rr.Delete(ctx, run.ID); err == nil {
		t.Fatal("deleted pinned run")
	}
	report, err := rr.ReportSnapshot(ctx, run.ID, false)
	if err != nil || len(report.Steps) != 1 || report.Steps[0].Calls[0].Expected != nil {
		t.Fatal("report", err)
	}
	e.Close()
	s.Close()
	s2, err := filestore.Open(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	rr2 := &filestore.Runs{Store: s2}
	old, err := rr2.Get(ctx, run.ID)
	if err != nil || len(old.Attempts) != 2 || old.Status != "passed" {
		t.Fatal("history after restart", err)
	}
}

func TestNoNetworkWithoutDurableAttempt(t *testing.T) {
	ctx := context.Background()
	s, err := filestore.Open(filepath.Join(t.TempDir(), "qag.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Bootstrap(ctx, "http://127.0.0.1:1", "127.0.0.1:2"); err != nil {
		t.Fatal(err)
	}
	c, res, rr := &filestore.Catalog{Store: s}, &filestore.Resources{Store: s}, &filestore.Runs{Store: s}
	e := service.New(ctx, c, res, rr)
	defer e.Close()
	info, _ := c.DemoInfo(ctx)
	tc, _ := c.GetCase(ctx, info.TestCaseID)
	s.Failpoint = func(stage string) error {
		if stage == "before-replace" {
			return errors.New("disk full")
		}
		return nil
	}
	if _, err = e.EditorAttempt(ctx, tc.ID, tc.Steps[0].Blocks[0].ID, info.EnvironmentID, tc.Revision, catalog.NewID()); err == nil {
		t.Fatal("attempt registered without storage")
	}
	s.Failpoint = nil
	a, err := e.EditorAttempt(ctx, tc.ID, tc.Steps[2].Blocks[0].ID, info.EnvironmentID, tc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	done := await(t, e, a.ID)
	if done.State != "error" || done.Result.ErrorCode != "demo_sql_unavailable" {
		t.Fatalf("SQL placeholder: %+v", done)
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal("SQL absence affected readiness", err)
	}
}
