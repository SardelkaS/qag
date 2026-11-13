package filestore_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/SardelkaS/qag/internal/service"
)

func TestHistoryPoliciesAndSelectedStep(t *testing.T) {
	ctx := context.Background()
	s, err := filestore.Open(filepath.Join(t.TempDir(), "data.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, res, rr := &filestore.Catalog{Store: s}, &filestore.Resources{Store: s}, &filestore.Runs{Store: s}
	e := service.New(ctx, c, res, rr)
	defer e.Close()
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

func TestLegacyYAMLReadAndNoOpDoNotUpgradeFile(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "legacy.yaml")
	seed, err := filestore.Open(filepath.Join(t.TempDir(), "seed.yaml"), true)
	if err != nil {
		t.Fatal(err)
	}
	cat, res, rr := &filestore.Catalog{Store: seed}, &filestore.Resources{Store: seed}, &filestore.Runs{Store: seed}
	folder, _ := cat.CreateFolder(ctx, "Старые проверки")
	tc, _ := cat.CreateCase(ctx, folder.ID, "Старый сценарий")
	tc, err = cat.SaveCase(ctx, tc.ID, tc.Name, tc.Revision, []catalog.Step{{ID: catalog.NewID(), Description: "Исторический шаг"}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := res.SaveEnvironmentMetadata(ctx, "", "legacy", "", 0)
	frozen, err := res.Freeze(ctx, env.ID, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := rr.Start(ctx, tc, frozen, catalog.NewID(), "legacy-start", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = rr.Cancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	doc, err := seed.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()
	originalJSON := doc.Data.Runs[0].Snapshot
	b, _ := dataset.Encode(doc)
	b = bytes.Replace(b, []byte("version: 4"), []byte("version: 1"), 1)
	b = regexp.MustCompile(`(?m)^\s+historyPolicy: auto\n`).ReplaceAll(b, nil)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := filestore.Open(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loaded, err := s.Snapshot(ctx)
	if err != nil || loaded.Version != dataset.Version {
		t.Fatal("legacy decode", err)
	}
	if len(loaded.Data.Runs) != 1 || loaded.Data.Runs[0].HistoryPolicy != "auto" || loaded.Data.Runs[0].Snapshot != originalJSON {
		t.Fatal("legacy history rewritten")
	}
	legacy, _ := dataset.Clone(loaded)
	legacy.Version = 1
	legacy.Data.Runs[0].HistoryPolicy = ""
	if _, counts, err := dataset.Merge(loaded, legacy); err != nil || counts["runs"].Added != 0 {
		t.Fatal("legacy defaults conflict", err)
	}
	if _, err = dataset.Encode(loaded); err != nil {
		t.Fatal(err)
	}
	if err = s.Update(ctx, func(*dataset.Data) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(b, after) {
		t.Fatal("read/no-op changed source")
	}
	c := &filestore.Catalog{Store: s}
	if _, err = c.CreateFolder(ctx, "Новая"); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(p)
	if !bytes.Contains(after, []byte("version: 4")) {
		t.Fatal("mutation did not upgrade")
	}
}
