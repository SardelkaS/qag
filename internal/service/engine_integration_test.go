//go:build integration

package service_test

import "qag/internal/dbscope"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"qag/internal/catalog"
	"qag/internal/execution"
	"qag/internal/problem"
	"qag/internal/resources"
	"qag/internal/runs"
	"qag/internal/service"
	"qag/internal/storage"
	"qag/internal/testfixture"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func await(t *testing.T, ctx context.Context, repo *runs.Repository, id string) execution.Attempt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		attempt, err := repo.GetAttempt(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.State != "queued" && attempt.State != "running" {
			return attempt
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("attempt did not finish")
	return execution.Attempt{}
}
func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func TestRunsRetentionIdempotencyAndSnapshots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	dsn := fixture.AppURL
	store, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	c := catalog.NewRepository(store.Pool)
	vault, _ := resources.NewVault(bytes.Repeat([]byte{5}, 32))
	res := resources.NewRepository(store.Pool, vault)
	history := runs.NewRepository(store.Pool)
	engine := service.New(ctx, c, res, history)
	folder, err := c.CreateFolder(ctx, "Прогоны "+catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironment(ctx, "", "Прогоны "+catalog.NewID(), 0, map[string]string{"TASK_ID": "v1"}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		engine.Close()
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		tx, err := store.Pool.Begin(cleanup)
		if err != nil {
			return
		}
		defer tx.Rollback(cleanup)
		for _, sql := range []string{`DELETE FROM qag.call_attempts WHERE test_case_id IN (SELECT id FROM qag.test_cases WHERE folder_id=$1)`, `DELETE FROM qag.runs WHERE test_case_id IN (SELECT id FROM qag.test_cases WHERE folder_id=$1)`, `DELETE FROM qag.test_cases WHERE folder_id=$1`, `DELETE FROM qag.folders WHERE id=$1`} {
			_, _ = tx.Exec(cleanup, dbscope.Render(tx, sql), folder.ID)
		}
		for _, sql := range []string{`UPDATE qag.resources SET current_version_id=NULL WHERE environment_id=$1`, `DELETE FROM qag.resource_versions WHERE resource_id IN (SELECT id FROM qag.resources WHERE environment_id=$1)`, `DELETE FROM qag.resources WHERE environment_id=$1`, `UPDATE qag.environments SET current_version_id=NULL WHERE id=$1`, `DELETE FROM qag.environment_versions WHERE environment_id=$1`, `DELETE FROM qag.environments WHERE id=$1`} {
			_, _ = tx.Exec(cleanup, dbscope.Render(tx, sql), env.ID)
		}
		_ = tx.Commit(cleanup)
	}()
	var success atomic.Bool
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Echo", r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer original-token" || r.URL.Path != "/api/task/v1" {
			t.Error("frozen request changed")
		}
		fmt.Fprintf(w, `{"ok":%t}`, success.Load())
	}))
	defer target.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, `{"ok":false}`) }))
	defer second.Close()
	resource, err := res.SaveResource(ctx, "", env.ID, "GW", "http", 0, resources.Config{BaseURL: target.URL + "/api", Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${TOKEN}"}}, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "original-token"}})
	if err != nil {
		t.Fatal(err)
	}
	caseDoc, err := c.CreateCase(ctx, folder.ID, "Проверка")
	if err != nil {
		t.Fatal(err)
	}
	statusCode := 200
	stepID, blockID := catalog.NewID(), catalog.NewID()
	caseDoc, err = c.SaveCase(ctx, caseDoc.ID, caseDoc.Name, caseDoc.Revision, []catalog.Step{{ID: stepID, Description: "Ручная проверка", Blocks: []catalog.Block{{ID: blockID, Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/task/${TASK_ID}", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &statusCode, BodyMode: "json", Body: `{"ok":true}`}}}}})
	if err != nil {
		t.Fatal(err)
	}
	key := catalog.NewID()
	editor, err := engine.EditorAttempt(ctx, caseDoc.ID, blockID, env.ID, caseDoc.Revision, key)
	if err != nil {
		t.Fatal(err)
	}
	await(t, ctx, history, editor.ID)
	masked, err := history.GetAttempt(ctx, editor.ID)
	if err != nil || masked.Result == nil || !masked.Result.Redacted {
		t.Fatal("echoed token not masked")
	}
	var persisted string
	if err = store.Pool.QueryRow(ctx, dbscope.Render(store.Pool, `SELECT result::text FROM qag.call_attempts WHERE id=$1`), editor.ID).Scan(&persisted); err != nil || strings.Contains(persisted, "original-token") {
		t.Fatal("secret persisted in result")
	}
	same, err := engine.EditorAttempt(ctx, caseDoc.ID, blockID, env.ID, caseDoc.Revision, key)
	if err != nil || same.ID != editor.ID || calls.Load() != 1 {
		t.Fatal("idempotent editor command repeated")
	}
	var count int
	if err = store.Pool.QueryRow(ctx, dbscope.Render(store.Pool, `SELECT count(*) FROM qag.runs WHERE test_case_id=$1`), caseDoc.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("editor trial created a run")
	}
	startKey := catalog.NewID()
	first, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, startKey)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, startKey)
	if err != nil || repeated.ID != first.ID {
		t.Fatal("run creation repeated")
	}
	active, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, catalog.NewID())
	if err != nil || active.ID != first.ID {
		t.Fatal("parallel run created")
	}
	attempt, err := engine.RunAttempt(ctx, first.ID, blockID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	await(t, ctx, history, attempt.ID)
	if _, err = history.UpdateStep(ctx, first.ID, stepID, "passed", "Проверено вручную", 1); err != nil {
		t.Fatal(err)
	}
	first, err = history.Finish(ctx, first.ID)
	if err != nil || first.Status != "failed" || !contains(first.RetentionReasons, "first_run") {
		t.Fatalf("first run: %+v %v", first, err)
	}
	again, err := history.Finish(ctx, first.ID)
	if err != nil || again.Status != "failed" || len(again.RetentionReasons) != 1 {
		t.Fatal("finish is not idempotent")
	}
	success.Store(true)
	good, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	resource.Config.BaseURL = second.URL
	if _, err = res.SaveResource(ctx, resource.ID, env.ID, resource.Name, resource.Kind, resource.Revision, resource.Config, resources.SecretChanges{Replace: map[string]string{"TOKEN": "changed-token"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = res.SaveEnvironment(ctx, env.ID, env.Name, env.Revision, map[string]string{"TASK_ID": "v2"}, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	attempt, err = engine.RunAttempt(ctx, good.ID, blockID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	finished := await(t, ctx, history, attempt.ID)
	if finished.Result == nil || finished.Result.Comparison.Status != "match" {
		t.Fatalf("frozen call: %+v", finished)
	}
	if _, err = history.UpdateStep(ctx, good.ID, stepID, "passed", "", 1); err != nil {
		t.Fatal(err)
	}
	good, err = history.Finish(ctx, good.ID)
	if err != nil || good.Status != "passed" || !contains(good.RetentionReasons, "first_success") {
		t.Fatalf("first success: %+v %v", good, err)
	}
	later, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = engine.RunAttempt(ctx, later.ID, blockID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	await(t, ctx, history, attempt.ID)
	later, err = history.Finish(ctx, later.ID)
	if err != nil || later.Status != "failed" || len(later.RetentionReasons) != 0 {
		t.Fatalf("later run: %+v %v", later, err)
	}
	later, err = history.Save(ctx, later.ID)
	if err != nil || !contains(later.RetentionReasons, "manual") {
		t.Fatal("manual save failed")
	}
	later, err = history.Save(ctx, later.ID)
	if err != nil || len(later.RetentionReasons) != 1 {
		t.Fatal("manual save duplicated")
	}
	if err = history.Delete(ctx, later.ID); err == nil {
		t.Fatal("retained run deleted")
	}
	// A refused durable start must not invoke the target. A refused result
	// write must remain visibly unconfirmed and retry only the metadata write.
	faultName := "fixture_" + strings.ReplaceAll(catalog.NewID(), "-", "")
	installFault := func(operation, condition string) {
		t.Helper()
		sql := fmt.Sprintf(`CREATE FUNCTION qag.%[1]s() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF %[2]s THEN RAISE EXCEPTION 'fixture write failure'; END IF; RETURN NEW; END$$; CREATE TRIGGER %[1]s BEFORE %[3]s ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.%[1]s()`, faultName, condition, operation)
		if _, err := store.Pool.Exec(ctx, dbscope.Render(store.Pool, sql), pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
	}
	clearFault := func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = store.Pool.Exec(cleanup, dbscope.Render(store.Pool, fmt.Sprintf(`DROP TRIGGER IF EXISTS %[1]s ON qag.call_attempts;DROP FUNCTION IF EXISTS qag.%[1]s()`, faultName)), pgx.QueryExecModeSimpleProtocol)
	}
	defer clearFault()
	installFault("INSERT", "true")
	beforeFault := calls.Load()
	if _, err = engine.EditorAttempt(ctx, caseDoc.ID, blockID, env.ID, caseDoc.Revision, catalog.NewID()); err == nil {
		t.Fatal("refused durable start reported success")
	}
	if calls.Load() != beforeFault {
		t.Fatal("target invoked without durable attempt")
	}
	clearFault()
	installFault("UPDATE", `NEW.state NOT IN ('queued','running')`)
	unconfirmed, err := engine.EditorAttempt(ctx, caseDoc.ID, blockID, env.ID, caseDoc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	pending := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, err := engine.GetAttempt(ctx, unconfirmed.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.PersistencePending {
			pending = true
			if view.Result == nil || view.Result.Comparison.Status != "not_compared" {
				t.Fatal("unconfirmed result reported as saved")
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pending || calls.Load() != beforeFault+1 {
		t.Fatal("missing unconfirmed result or repeated target request")
	}
	clearFault()
	await(t, ctx, history, unconfirmed.ID)
	if calls.Load() != beforeFault+1 {
		t.Fatal("external request was replayed on storage recovery")
	}
	caseDoc, err = c.SaveCase(ctx, caseDoc.ID, "Новая версия", caseDoc.Revision, []catalog.Step{{ID: stepID, Description: "Теперь вручную"}})
	if err != nil {
		t.Fatal(err)
	}
	old, err := history.Get(ctx, first.ID)
	if err != nil || len(old.Snapshot.Steps[0].Blocks) != 1 || old.Snapshot.Name == caseDoc.Name || old.StepStates[0].Comment != "Проверено вручную" {
		t.Fatal("history snapshot changed")
	}
	transient, err := engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.Finish(ctx, transient.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = history.Save(ctx, transient.ID) }()
	go func() { defer wg.Done(); _ = history.Delete(ctx, transient.ID) }()
	wg.Wait()
	transientRetained := false
	if remaining, err := history.Get(ctx, transient.ID); err == nil {
		if !contains(remaining.RetentionReasons, "manual") {
			t.Fatal("save/delete race left an unprotected record")
		}
		transientRetained = true
	} else {
		var issue *problem.Error
		if !errors.As(err, &issue) || issue.Code != "not_found" {
			t.Fatalf("race result unavailable: %v", err)
		}
	}
	archive := true
	if _, err = c.PatchCase(ctx, caseDoc.ID, nil, &archive); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.StartRun(ctx, caseDoc.ID, env.ID, caseDoc.Revision, catalog.NewID()); err == nil {
		t.Fatal("archived case started")
	}
	if _, err = history.Get(ctx, good.ID); err != nil {
		t.Fatal("archive lost history")
	}
	manual, err := c.CreateCase(ctx, folder.ID, "Только вручную")
	if err != nil {
		t.Fatal(err)
	}
	manualStep := catalog.NewID()
	manual, err = c.SaveCase(ctx, manual.ID, manual.Name, manual.Revision, []catalog.Step{{ID: manualStep, Description: "Проверить экран"}})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := engine.StartRun(ctx, manual.ID, env.ID, manual.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.UpdateStep(ctx, direct.ID, manualStep, "passed", "", 1); err != nil {
		t.Fatal(err)
	}
	direct, err = history.Finish(ctx, direct.ID)
	if err != nil || direct.Status != "passed" || !contains(direct.RetentionReasons, "first_run") || !contains(direct.RetentionReasons, "first_success") || len(direct.RetentionReasons) != 2 {
		t.Fatal("first successful run not stored as one record with two reasons")
	}
	abandoned, err := engine.StartRun(ctx, manual.ID, env.ID, manual.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	beforeRecovery := calls.Load()
	engine.Close()
	if err = history.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := history.Get(ctx, abandoned.ID)
	if err != nil || recovered.Status != "interrupted" || calls.Load() != beforeRecovery {
		t.Fatal("recovery replayed calls or did not interrupt")
	}
	retained := true
	page, err := history.List(ctx, caseDoc.ID, &retained, 100, "")
	expectedCount := 3
	if transientRetained {
		expectedCount++
	}
	if err != nil || len(page.Items) != expectedCount {
		t.Fatalf("retained history: %d %v", len(page.Items), err)
	}
	serialized := fmt.Sprintf("%+v", page)
	if strings.Contains(serialized, "original-token") || strings.Contains(serialized, "changed-token") {
		t.Fatal("secrets in history")
	}
}
