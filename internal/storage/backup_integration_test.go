//go:build integration

package storage_test

import "qag/internal/dbscope"

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"qag/internal/mcpserver"
	"qag/internal/reporting"
	"qag/internal/testfixture"
	"qag/web"

	"os"
	"os/exec"
	"path/filepath"

	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/demo"
	"qag/internal/resources"
	"qag/internal/runs"
	"qag/internal/service"
	"qag/internal/storage"
)

func TestBackupRestoreCatalogHistoryAndSecret(t *testing.T) {
	ctx, done := context.WithTimeout(context.Background(), 45*time.Second)
	defer done()
	cfg, err := pgx.ParseConfig(os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil || cfg.Database != "qag" {
		t.Fatal("isolated qag_test required")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		docker = filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "DockerDesktop", "resources", "bin", "docker.exe")
		if _, err = os.Stat(docker); err != nil {
			docker = `C:\Program Files\Docker\Docker\resources\bin\docker.exe`
		}
	}
	fixture := testfixture.ForTest(t, ctx)
	open := func() *storage.Store {
		s, e := storage.Open(ctx, fixture.AppURL)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	source := open()
	defer source.Close()
	if err = source.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, _ := resources.NewVault(key)
	c := catalog.NewRepository(source.Pool)
	res := resources.NewRepository(source.Pool, vault)
	history := runs.NewRepository(source.Pool)
	engine := service.New(ctx, c, res, history)
	defer engine.Close()
	folder, err := c.CreateFolder(ctx, "Backup")
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironment(ctx, "", "backup", 0, map[string]string{"ID": "123"}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	env, err = res.SaveEnvironmentMetadata(ctx, env.ID, env.Name, "Описание из резервной копии", env.Revision)
	if err != nil {
		t.Fatal(err)
	}
	const derived = "backup-derived-secret-sentinel"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprintf(w, `{"token":%q}`, derived)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"reflected": r.Header.Get("Authorization")})
	}))
	defer target.Close()
	header := catalog.RequestHeader{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "POST", URL: target.URL + "/token", BodyMode: "json", Body: `{"client":"${TOKEN}"}`, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "Bearer ", FetchMode: "per_run"}}
	_, err = res.SaveResource(ctx, "", env.ID, "GW", "http", 0, resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{header}}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "backup-test-token"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.CreateCase(ctx, folder.ID, "Восстановленный сценарий")
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	step := catalog.NewID()
	var pngBytes bytes.Buffer
	if err = png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	picture, err := c.UploadImage(ctx, doc.ID, catalog.ImageUpload{Filename: "backup.png", DataBase64: base64.StdEncoding.EncodeToString(pngBytes.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	refs := []catalog.ImageRef{{ImageID: picture.ID, Caption: "Исходный эталон"}}
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: step, Description: "Сценарий в backup", Expected: &catalog.StepExpectation{Text: "Ручная проверка", Images: refs}, Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/task", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none", Images: refs}}}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.StartRun(ctx, doc.ID, env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := engine.RunAttempt(ctx, run.ID, doc.Steps[0].Blocks[0].ID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for attempt.State == "queued" || attempt.State == "running" {
		time.Sleep(10 * time.Millisecond)
		attempt, err = engine.GetAttempt(ctx, attempt.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if attempt.State != "completed" || !attempt.Result.Redacted {
		t.Fatal("backup source execution failed")
	}
	variant, err := history.SaveVariant(ctx, run.ID, doc.Steps[0].Blocks[0].ID, "", "Backup variant", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	research, err := engine.VariantAttempt(ctx, run.ID, variant.ID, variant.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for research.FinishedAt == nil {
		time.Sleep(10 * time.Millisecond)
		research, err = engine.GetAttempt(ctx, research.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	batch, err := engine.StartBatch(ctx, run.ID, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for batch.State == "running" {
		time.Sleep(10 * time.Millisecond)
		current, readErr := history.Get(ctx, run.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		batch = current.Batches[0]
	}
	if _, err = history.UpdateStep(ctx, run.ID, step, "not_run", "Сохранённый комментарий", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = history.Finish(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	doc.Steps[0].Expected.Images = nil
	doc.Steps[0].Blocks[0].Expected.Images = nil
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, doc.Steps)
	if err != nil {
		t.Fatal(err)
	}
	demoConfig, demoSecrets, err := demo.DatabaseConfig(fixture.AppURL, fixture.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := demo.OpenReader(ctx, source.Pool, demoConfig, demoSecrets)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	demoInfo, err := demo.Bootstrap(ctx, source.Pool, vault, "http://127.0.0.1:1", "127.0.0.1:2", demoConfig, demoSecrets)
	if err != nil {
		t.Fatal(err)
	}
	demoCase, err := c.GetCase(ctx, demoInfo.TestCaseID)
	if err != nil {
		t.Fatal(err)
	}
	demoRun, err := engine.StartRun(ctx, demoCase.ID, demoInfo.EnvironmentID, demoCase.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.UpdateStep(ctx, demoRun.ID, demoRun.StepStates[0].StepID, "passed", "Пример в резервной копии", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = history.Finish(ctx, demoRun.ID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "qag.dump")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	dump := exec.CommandContext(ctx, docker, "exec", "qag-postgres-1", "pg_dump", "-U", cfg.User, "-d", "qag", "--schema", fixture.Namespace.App, "--schema", fixture.Namespace.Demo, "--format=custom", "--no-owner", "--no-acl")
	dump.Stdout = out
	dump.Stderr = &diagnostic
	err = dump.Run()
	_ = out.Close()
	if err != nil {
		t.Fatal("pg_dump failed")
	}
	// Check the expanded dump in memory, without printing any of its contents.
	plainDump, err := exec.CommandContext(ctx, docker, "exec", "qag-postgres-1", "pg_dump", "-U", cfg.User, "-d", "qag", "--schema", fixture.Namespace.App, "--schema", fixture.Namespace.Demo, "--format=plain", "--no-owner", "--no-acl").Output()
	if err != nil || bytes.Contains(plainDump, []byte("CREATE SCHEMA qag;")) || bytes.Contains(plainDump, []byte("CREATE SCHEMA qag_demo;")) || bytes.Contains(plainDump, []byte(derived)) || bytes.Contains(plainDump, []byte("backup-test-token")) {
		t.Fatal("secret leaked into dump")
	}
	engine.Close()
	source.Close()
	if err = fixture.RenameSource(ctx); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	restore := exec.CommandContext(ctx, docker, "exec", "-i", "qag-postgres-1", "pg_restore", "-U", cfg.User, "-d", "qag", "--no-owner", "--no-acl", "--exit-on-error")
	restore.Stdin = in
	restore.Stderr = &diagnostic
	if err = restore.Run(); err != nil {
		t.Fatal("pg_restore failed")
	}
	if err = fixture.AdoptRestored(ctx); err != nil {
		t.Fatal(err)
	}
	restored := open()
	defer restored.Close()
	if err = restored.Migrate(ctx); err != nil {
		t.Fatal("restored migration checksums differ")
	}
	got, err := catalog.NewRepository(restored.Pool).GetCase(ctx, doc.ID)
	if err != nil || got.Steps[0].Description != "Сценарий в backup" {
		t.Fatal("catalog not restored")
	}
	r, err := runs.NewRepository(restored.Pool).Get(ctx, run.ID)
	if err != nil || r.StepStates[0].Comment != "Сохранённый комментарий" || len(r.RetentionReasons) != 1 {
		t.Fatal("history not restored")
	}
	if len(r.Variants) != 1 || r.Variants[0].ID != variant.ID || len(r.Batches) != 1 || r.Batches[0].ID != batch.ID || r.Batches[0].State != "completed" || len(r.Attempts) != 3 {
		t.Fatal("variant versions, batch items or attempts not restored")
	}
	if len(got.Steps[0].Expected.Images) != 0 || r.Snapshot.Steps[0].Expected.Images[0].Caption != "Исходный эталон" || r.Snapshot.Steps[0].Blocks[0].Expected.Images[0].ImageID != picture.ID {
		t.Fatal("restored historic gallery changed")
	}
	restoredPicture, restoredBytes, err := catalog.NewRepository(restored.Pool).ReadImage(ctx, doc.ID, picture.ID)
	if err != nil || !bytes.Equal(restoredBytes, pngBytes.Bytes()) || restoredPicture.SHA256 != picture.SHA256 || restoredPicture.Width != 3 {
		t.Fatal("image bytes or metadata not restored")
	}
	rr := resources.NewRepository(restored.Pool, vault)
	restoredEnv, err := rr.GetEnvironment(ctx, env.ID)
	if err != nil || restoredEnv.Description != "Описание из резервной копии" {
		t.Fatal("environment description not restored")
	}
	snapshot, err := rr.Freeze(ctx, env.ID, map[string]string{"GW": "http"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := rr.Resolve(ctx, snapshot, "GW")
	if err != nil || resolved.Secrets["TOKEN"] != "backup-test-token" {
		t.Fatal("separate master key cannot decrypt restored resource")
	}
	if resolved.Resource.Config.Headers[0].Source.JSONPointer != "/token" || resolved.Resource.Config.Headers[0].Source.Body != `{"client":"${TOKEN}"}` {
		t.Fatal("source configuration not restored")
	}
	wrong, _ := resources.NewVault(bytes.Repeat([]byte{9}, 32))
	if _, err = resources.NewRepository(restored.Pool, wrong).Resolve(ctx, snapshot, "GW"); err == nil {
		t.Fatal("backup decrypted with wrong key")
	}
	demoSnapshot, err := rr.Freeze(ctx, demoInfo.EnvironmentID, map[string]string{"DEMO_SQL": "postgresql"})
	if err != nil {
		t.Fatal(err)
	}
	demoResolved, err := rr.Resolve(ctx, demoSnapshot, "DEMO_SQL")
	if err != nil || demoResolved.Secrets["PASSWORD"] != demoSecrets["PASSWORD"] {
		t.Fatal("restored demo secret cannot be decrypted")
	}
	demoConfig.Database = "qag"
	restoredReader, err := demo.OpenReader(ctx, restored.Pool, demoConfig, demoSecrets)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredReader.Close()
	restoredInfo, err := demo.Bootstrap(ctx, restored.Pool, vault, "http://127.0.0.1:1", "127.0.0.1:2", demoConfig, demoSecrets)
	if err != nil || restoredInfo != demoInfo {
		t.Fatal("restored demo identity changed")
	}
	restoredRun, err := runs.NewRepository(restored.Pool).Get(ctx, demoRun.ID)
	if err != nil || restoredRun.StepStates[0].Comment != "Пример в резервной копии" {
		t.Fatal("demo history not restored")
	}
	var amount string
	if err = restoredReader.QueryRow(ctx, dbscope.Render(restoredReader, `SELECT amount::text FROM qag_demo.items`)).Scan(&amount); err != nil || amount != "9007199254740993.125" {
		t.Fatal("demo schema not restored")
	}
	// Read and render restored history through both MCP protocols. Hash every
	// persisted public/encrypted/snapshot field before and after these operations.
	digests := func() map[string]string {
		out := map[string]string{}
		for _, table := range []string{"test_cases", "runs", "call_attempts", "resource_versions", "environment_versions", "expected_images", "run_call_variants", "run_call_variant_versions", "run_batches", "run_batch_items", "run_steps"} {
			var digest string
			if err := restored.Pool.QueryRow(ctx, dbscope.Render(restored.Pool, `SELECT md5(coalesce(string_agg(to_jsonb(t)::text,'' ORDER BY to_jsonb(t)::text),'')) FROM qag.`+table+` t`)).Scan(&digest); err != nil {
				t.Fatal(err)
			}
			out[table] = digest
		}
		return out
	}
	beforeMCP := digests()
	mcpEngine := service.New(ctx, catalog.NewRepository(restored.Pool), rr, runs.NewRepository(restored.Pool))
	defer mcpEngine.Close()
	reports := reporting.NewService(mcpEngine.Runs, mcpEngine.Catalog, web.Assets(), false)
	mcpServer := httptest.NewServer(mcpserver.New(mcpserver.Options{Engine: mcpEngine, Reports: reports}).Handler())
	defer mcpServer.Close()
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		client := mcp.NewClient(&mcp.Implementation{Name: "backup-verify", Version: "1"}, nil)
		session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: mcpServer.URL}, &mcp.ClientSessionOptions{ProtocolVersion: version})
		if e != nil {
			t.Fatal(e)
		}
		call := func(name string, args, out any) {
			t.Helper()
			result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if e != nil || result.IsError {
				t.Fatalf("restored %s failed: %v", name, e)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			json.Unmarshal(raw, &envelope)
			if out != nil && json.Unmarshal(envelope.Data, out) != nil {
				t.Fatal("restored MCP decode")
			}
		}
		var historic runs.Run
		call("qag_run_get", map[string]any{"id": run.ID}, &historic)
		if historic.ID != run.ID || historic.Attempts[0].ID != r.Attempts[0].ID {
			t.Fatal("MCP changed restored IDs")
		}
		var report runs.Report
		call("qag_report_prepare", map[string]any{"runId": run.ID, "includeExpected": true}, &report)
		for _, format := range []string{"pdf", "text", "markdown", "rich"} {
			var result mcpserver.ExportResult
			call("qag_report_export", map[string]any{"runId": run.ID, "snapshotId": report.SnapshotID, "hash": report.Hash, "includeExpected": true, "format": format}, &result)
			if len(result.Files) == 0 {
				t.Fatal("restored report empty")
			}
		}
		session.Close()
	}
	afterMCP := digests()
	for table, before := range beforeMCP {
		if afterMCP[table] != before {
			t.Fatal("MCP changed restored persisted data: " + table)
		}
	}
}
