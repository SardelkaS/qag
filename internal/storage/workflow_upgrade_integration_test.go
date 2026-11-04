//go:build integration

package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/SardelkaS/qag/internal/storage"
	"github.com/SardelkaS/qag/internal/testfixture"
)

func TestUpgradePopulatedSixMigrationNamespace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	store, err := storage.Open(ctx, fixture.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Pool.Exec(ctx, dbscope.Render(store.Pool, `CREATE TABLE qag.schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`)); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() >= "007" {
			continue
		}
		raw, err := os.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		sql := dbscope.Render(store.Pool, string(raw))
		sql = strings.ReplaceAll(sql, "CREATE SCHEMA "+pgx.Identifier{store.Namespace.Demo}.Sanitize()+";", "")
		if _, err = store.Pool.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if _, err = store.Pool.Exec(ctx, dbscope.Render(store.Pool, `INSERT INTO qag.schema_migrations(name,checksum) VALUES($1,$2)`), "migrations/"+file.Name(), hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
	}
	c := catalog.NewRepository(store.Pool)
	vault, _ := resources.NewVault(bytes.Repeat([]byte{5}, 32))
	res := resources.NewRepository(store.Pool, vault)
	folder, err := c.CreateFolder(ctx, "Старые данные 006")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.CreateCase(ctx, folder.ID, "Исторический сценарий")
	if err != nil {
		t.Fatal(err)
	}
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: catalog.NewID(), Description: "Был заблокирован", Blocks: []catalog.Block{}}, {ID: catalog.NewID(), Description: "Был пропущен", Blocks: []catalog.Block{}}})
	if err != nil {
		t.Fatal(err)
	}
	env, err := res.SaveEnvironmentMetadata(ctx, "", "old", "Описание 006", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = res.SaveResource(ctx, "", env.ID, "OLD", "http", 0, resources.Config{BaseURL: "http://127.0.0.1:1", TLS: resources.TLSConfig{Mode: "plaintext"}}, resources.SecretChanges{Replace: map[string]string{"TOKEN": "old-upgrade-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := res.Freeze(ctx, env.ID, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	id := catalog.NewID()
	if _, err = store.Pool.Exec(ctx, dbscope.Render(store.Pool, `INSERT INTO qag.runs(id,test_case_id,environment_version_id,snapshot,context,status,retention_reasons,idempotency_key,request_hash,finished_at) VALUES($1,$2,$3,$4,$5,'blocked',ARRAY['first_run'],$6,'old',now())`), id, doc.ID, frozen.EnvironmentVersionID, doc, frozen, catalog.NewID()); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"blocked", "skipped"} {
		if _, err = store.Pool.Exec(ctx, dbscope.Render(store.Pool, `INSERT INTO qag.run_steps(run_id,step_id,status,comment) VALUES($1,$2,$3,'До обновления')`), id, doc.Steps[i].ID, status); err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := func() string {
		var value string
		if err := store.Pool.QueryRow(ctx, dbscope.Render(store.Pool, `SELECT md5(jsonb_build_object('cases',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.test_cases t),'env',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.environment_versions t),'secrets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.resource_versions t),'runs',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM qag.runs t),'steps',(SELECT jsonb_agg(to_jsonb(t) ORDER BY step_id) FROM qag.run_steps t),'migrations',(SELECT jsonb_agg(to_jsonb(t) ORDER BY name) FROM qag.schema_migrations t WHERE name<'migrations/007'))::text)`)).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := fingerprint()
	for i := 0; i < 2; i++ {
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if fingerprint() != before {
		t.Fatal("upgrade rewrote snapshots, secrets, IDs, statuses or migration checksums")
	}
	run, err := runs.NewRepository(store.Pool).Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "blocked" || len(run.RetentionReasons) != 1 || len(run.Variants) != 0 || len(run.Batches) != 0 {
		t.Fatal("old run changed")
	}
	for i, state := range run.StepStates {
		if state.Status != "not_run" || state.LegacyStatus != []string{"blocked", "skipped"}[i] {
			t.Fatal("legacy status projection failed")
		}
	}
	report, err := runs.NewRepository(store.Pool).ReportSnapshot(ctx, id, false)
	if err != nil || report.Partial || report.Status != "blocked" {
		t.Fatal("legacy report failed", err)
	}
}
