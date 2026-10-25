//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err != nil || cfg.Database != "qag_test" {
		t.Fatal("isolated qag_test required")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		docker = filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "DockerDesktop", "resources", "bin", "docker.exe")
		if _, err = os.Stat(docker); err != nil {
			docker = `C:\Program Files\Docker\Docker\resources\bin\docker.exe`
		}
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer admin.Close(context.Background())
	names := []string{"qag_backup_source_" + strings.ReplaceAll(catalog.NewID(), "-", ""), "qag_backup_restore_" + strings.ReplaceAll(catalog.NewID(), "-", "")}
	var demoRole string
	for _, name := range names {
		if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal("create backup fixture database")
		}
		defer func(name string) {
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = admin.Exec(c, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			if name == names[0] && demoRole != "" {
				_, _ = admin.Exec(c, "DROP ROLE "+pgx.Identifier{demoRole}.Sanitize())
			}
		}(name)
	}
	open := func(db string) *storage.Store {
		u, e := url.Parse(os.Getenv("QAG_TEST_DATABASE_URL"))
		if e != nil {
			t.Fatal("test URL required")
		}
		u.Path = "/" + db
		s, e := storage.Open(ctx, u.String())
		if e != nil {
			t.Fatal(e)
		}
		var actual string
		if e = s.Pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); e != nil || actual != db {
			s.Close()
			t.Fatal("backup connection is not isolated")
		}
		return s
	}
	source := open(names[0])
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
	_, err = res.SaveResource(ctx, "", env.ID, "GW", "http", 0, resources.Config{BaseURL: "http://127.0.0.1:1/api", TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "backup-test-token"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.CreateCase(ctx, folder.ID, "Восстановленный сценарий")
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	step := catalog.NewID()
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: step, Description: "Сценарий в backup", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/task", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.StartRun(ctx, doc.ID, env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.UpdateStep(ctx, run.ID, step, "blocked", "Сохранённый комментарий", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = history.Finish(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	demoAdminURL, _ := url.Parse(os.Getenv("QAG_TEST_DATABASE_URL"))
	demoAdminURL.Path = "/" + names[0]
	demoReaderURL := *demoAdminURL
	roleName := "qag_demo_backup_" + strings.ReplaceAll(catalog.NewID(), "-", "")
	demoReaderURL.User = url.UserPassword(roleName, catalog.NewID())
	demoConfig, demoSecrets, err := demo.DatabaseConfig(demoAdminURL.String(), demoReaderURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if err = demo.PrepareRole(ctx, source.Pool, demoConfig, demoSecrets); err != nil {
		t.Fatal(err)
	}
	demoRole = roleName
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
	dump := exec.CommandContext(ctx, docker, "exec", "qag-postgres-test-1", "pg_dump", "-U", cfg.User, "-d", names[0], "--format=custom", "--no-owner", "--no-acl")
	dump.Stdout = out
	dump.Stderr = &diagnostic
	err = dump.Run()
	_ = out.Close()
	if err != nil {
		t.Fatal("pg_dump failed")
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	restore := exec.CommandContext(ctx, docker, "exec", "-i", "qag-postgres-test-1", "pg_restore", "-U", cfg.User, "-d", names[1], "--no-owner", "--no-acl", "--exit-on-error")
	restore.Stdin = in
	restore.Stderr = &diagnostic
	if err = restore.Run(); err != nil {
		t.Fatal("pg_restore failed")
	}
	restored := open(names[1])
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
	rr := resources.NewRepository(restored.Pool, vault)
	snapshot, err := rr.Freeze(ctx, env.ID, map[string]string{"GW": "http"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := rr.Resolve(ctx, snapshot, "GW")
	if err != nil || resolved.Secrets["TOKEN"] != "backup-test-token" {
		t.Fatal("separate master key cannot decrypt restored resource")
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
	demoConfig.Database = names[1]
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
	if err = restoredReader.QueryRow(ctx, `SELECT amount::text FROM qag_demo.items`).Scan(&amount); err != nil || amount != "9007199254740993.125" {
		t.Fatal("demo schema not restored")
	}
}
