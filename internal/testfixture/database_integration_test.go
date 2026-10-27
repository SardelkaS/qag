//go:build integration

package testfixture_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/demo"
	"qag/internal/resources"
	"qag/internal/runs"
	"qag/internal/service"
	"qag/internal/storage"
	"qag/internal/testfixture"
)

func TestSharedDatabaseNamespaceIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, err := testfixture.NewDatabase(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := testfixture.CleanupContext()
		defer done()
		if err := fixture.Close(c); err != nil {
			t.Error(err)
		}
	}()
	s, err := storage.Open(ctx, fixture.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.AcquireInstance(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := storage.Open(ctx, fixture.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = other.AcquireInstance(ctx); err == nil {
		t.Fatal("second namespace instance accepted")
	}
	c := catalog.NewRepository(s.Pool)
	folder, err := c.CreateFolder(ctx, "Изолированный каталог")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := c.CreateCase(ctx, folder.ID, "")
	if err != nil || doc.Name == "" {
		t.Fatal("namespace catalog failed")
	}
	vault, _ := resources.NewVault(bytes.Repeat([]byte{7}, 32))
	cfg, secrets, err := demo.DatabaseConfig(fixture.AppURL, fixture.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	info, err := demo.Bootstrap(ctx, s.Pool, vault, "http://127.0.0.1:1", "127.0.0.1:2", cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := demo.OpenReader(ctx, s.Pool, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = (&demo.Service{Pool: reader}).Ready(ctx); err != nil {
		t.Fatal(err)
	}
	env, err := resources.NewRepository(s.Pool, vault).SaveEnvironmentMetadata(ctx, "", "isolated", "namespace test", 0)
	if err != nil {
		t.Fatal(err)
	}
	doc, err = c.SaveCase(ctx, doc.ID, doc.Name, doc.Revision, []catalog.Step{{ID: catalog.NewID(), Description: "Проверить изоляцию"}})
	if err != nil {
		t.Fatal(err)
	}
	history := runs.NewRepository(s.Pool)
	engine := service.New(ctx, c, resources.NewRepository(s.Pool, vault), history)
	defer engine.Close()
	run, err := engine.StartRun(ctx, doc.ID, env.ID, doc.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = history.UpdateStep(ctx, run.ID, doc.Steps[0].ID, "passed", "Только тестовая схема", 1); err != nil {
		t.Fatal(err)
	}
	run, err = engine.FinishRun(ctx, run.ID)
	if err != nil || run.Status != "passed" || len(run.RetentionReasons) != 2 {
		t.Fatal("namespace run history failed")
	}
	sys, err := c.GetCase(ctx, info.TestCaseID)
	if err != nil || sys.Steps[2].Blocks[0].SQL.Query == "SELECT id, name, status, amount FROM qag_demo.items WHERE id=$1 ORDER BY id" {
		t.Fatal("demo SQL namespace not explicit")
	}
	for _, query := range []string{"SELECT count(*) FROM qag.test_cases", "DROP SCHEMA qag", "CREATE DATABASE forbidden", "ALTER ROLE " + pgx.Identifier{fixture.AppRole}.Sanitize() + " SUPERUSER"} {
		if _, err = s.Pool.Exec(ctx, query); err == nil {
			t.Fatal("test role escaped isolation")
		}
	}
	if err = fixture.Cleanup(ctx, "qag", fixture.Nonce); err == nil {
		t.Fatal("unsafe cleanup accepted")
	}
	// A second test namespace must run alongside the first and the real app.
	second, err := testfixture.NewDatabase(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := testfixture.CleanupContext()
		defer done()
		if err := second.Close(c); err != nil {
			t.Error(err)
		}
	}()
	ss, err := storage.Open(ctx, second.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	if err = ss.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = ss.AcquireInstance(ctx); err != nil {
		t.Fatal(err)
	}
	if err = fixture.Cleanup(ctx, second.ID, second.Nonce); err == nil {
		t.Fatal("foreign test cleanup accepted")
	}
	admin, err := pgx.Connect(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var manifest []byte
	if err = admin.QueryRow(ctx, `SELECT objects FROM qag_test.manifest WHERE id=$1`, fixture.ID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `UPDATE qag_test.manifest SET objects=jsonb_set(objects,'{0,oid}',to_jsonb((objects->0->>'oid')::bigint+1)) WHERE id=$1`, fixture.ID); err != nil {
		t.Fatal(err)
	}
	badCleanup := fixture.Cleanup(ctx, fixture.ID, fixture.Nonce)
	if _, err = admin.Exec(ctx, `UPDATE qag_test.manifest SET objects=$2 WHERE id=$1`, fixture.ID, manifest); err != nil {
		t.Fatal(err)
	}
	if badCleanup == nil {
		t.Fatal("changed schema OID accepted")
	}
	if _, err = ss.Pool.Exec(ctx, "SELECT * FROM "+pgx.Identifier{fixture.Namespace.App, "test_cases"}.Sanitize()); err == nil {
		t.Fatal("test namespaces can read each other")
	}
}
