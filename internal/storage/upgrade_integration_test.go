//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"net/url"
	"os"
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

func TestUpgradePopulatedFourMigrationDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgx.ParseConfig(os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil || cfg.Database != "qag_test" {
		t.Fatal("isolated qag_test required")
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	db := "qag_upgrade_" + strings.ReplaceAll(catalog.NewID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = admin.Exec(c, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)")
	}()
	u, _ := url.Parse(os.Getenv("QAG_TEST_DATABASE_URL"))
	u.Path = "/" + db
	store, err := storage.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	vault, _ := resources.NewVault(bytes.Repeat([]byte{7}, 32))
	c := catalog.NewRepository(store.Pool)
	res := resources.NewRepository(store.Pool, vault)
	history := runs.NewRepository(store.Pool)
	engine := service.New(ctx, c, res, history)
	defer engine.Close()
	folder, err := c.CreateFolder(ctx, "Существующие сценарии")
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironment(ctx, "", "legacy", 0, map[string]string{"ID": "unchanged"}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "upgrade-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := res.SaveResource(ctx, "", env.ID, "GW", "http", 0, resources.Config{BaseURL: "http://127.0.0.1:1", Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "${TOKEN}"}}, TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	// Preserve an old TLS mode as stored JSON, without rewriting it through the new editor.
	if _, err = store.Pool.Exec(ctx, `UPDATE qag.resource_versions SET config=jsonb_set(config,'{tls}', '{"mode":"tls","certificate":"legacy-certificate"}') WHERE id=$1`, resource.VersionID); err != nil {
		t.Fatal(err)
	}
	doc, err := c.CreateCase(ctx, folder.ID, "Старый кейс")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: catalog.NewID(), Description: "Ручной старый шаг", Blocks: []catalog.Block{}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := engine.StartRun(ctx, doc.ID, env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.UpdateStep(ctx, run.ID, doc.Steps[0].ID, "passed", "Прежний комментарий", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.FinishRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	sql := resources.Config{Address: "127.0.0.1:1", Database: db, Username: "unused", TLS: resources.TLSConfig{Mode: "plaintext"}}
	info, err := demo.Bootstrap(ctx, store.Pool, vault, "http://127.0.0.1:1", "127.0.0.1:2", sql, map[string]string{"PASSWORD": "demo-secret"})
	if err != nil {
		t.Fatal(err)
	}
	// No new-feature rows were created. Removing only the two additive migrations
	// reconstructs the exact four-migration schema that a previous release used.
	if _, err = store.Pool.Exec(ctx, `DROP TABLE qag.expected_images; ALTER TABLE qag.environments DROP COLUMN description; DELETE FROM qag.schema_migrations WHERE name LIKE 'migrations/005_%' OR name LIKE 'migrations/006_%'`, pgx.QueryExecModeSimpleProtocol); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var data string
		err := store.Pool.QueryRow(ctx, `SELECT jsonb_build_object(
 'cases',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.test_cases t),
 'envs',(SELECT jsonb_agg(to_jsonb(t)-'description' ORDER BY id) FROM qag.environments t),
 'envVersions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.environment_versions t),
 'resources',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.resources t),
 'resourceVersions',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.resource_versions t),
 'runs',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.runs t),
 'steps',(SELECT jsonb_agg(to_jsonb(t) ORDER BY step_id) FROM qag.run_steps t),
 'demo',(SELECT jsonb_agg(to_jsonb(t)) FROM qag.builtin_demo t),
 'oldMigrations',(SELECT jsonb_agg(to_jsonb(t) ORDER BY name) FROM qag.schema_migrations t WHERE name < 'migrations/005')
 )::text`).Scan(&data)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := snapshot()
	for i := 0; i < 2; i++ {
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := demo.Bootstrap(ctx, store.Pool, vault, "http://127.0.0.1:1", "127.0.0.1:2", sql, map[string]string{"PASSWORD": "demo-secret"})
		if err != nil || got != info {
			t.Fatal("builtin IDs changed during upgrade")
		}
	}
	if before != snapshot() {
		t.Fatal("upgrade changed existing documents, versions, ciphertext, checksums or history")
	}
	gotEnv, err := res.GetEnvironment(ctx, env.ID)
	if err != nil || gotEnv.Description != "" || gotEnv.Variables["ID"] != "unchanged" {
		t.Fatal("environment upgrade failed")
	}
	gotResource, err := res.GetResource(ctx, resource.ID)
	if err != nil || gotResource.Config.TLS.Mode != "tls" || gotResource.Config.Headers[0].Value != "${TOKEN}" {
		t.Fatal("legacy headers or TLS rewritten")
	}
}
