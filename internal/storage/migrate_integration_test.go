//go:build integration

package storage

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"errors"
	"github.com/SardelkaS/qag/internal/testfixture"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigrateFreshDatabaseAndRepeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	dsn := fixture.AppURL
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var count int
	if err = store.Pool.QueryRow(ctx, dbscope.Render(store.Pool, "SELECT count(*) FROM qag.schema_migrations")).Scan(&count); err != nil || count != 11 {
		t.Fatalf("migration count %d: %v", count, err)
	}
	for _, name := range []string{"expected_images", "folders", "test_cases", "environments", "environment_versions", "resources", "resource_versions", "runs", "run_resource_versions", "run_steps", "call_attempts", "call_logs", "http_variable_probes"} {
		var exists bool
		if err = store.Pool.QueryRow(ctx, dbscope.Render(store.Pool, "SELECT to_regclass($1) IS NOT NULL"), store.Namespace.App+"."+name).Scan(&exists); err != nil || !exists {
			t.Fatalf("missing table %s: %v", name, err)
		}
	}
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var id string
	if err = tx.QueryRow(ctx, dbscope.Render(tx, "INSERT INTO qag.folders(name,normalized_name) VALUES('Миграция','migration-integration') RETURNING id")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, dbscope.Render(tx, "INSERT INTO qag.folders(name,normalized_name) VALUES('Дубликат','migration-integration')"))
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "23505" {
		t.Fatalf("duplicate normalized name not rejected: %v", err)
	}
}

func TestExclusiveInstanceLock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	dsn := fixture.AppURL
	first, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = first.AcquireInstance(ctx); err != nil {
		t.Fatal(err)
	}
	if err = second.AcquireInstance(ctx); !errors.Is(err, ErrInstanceBusy) {
		t.Fatal("second instance obtained the lock")
	}
	first.Close()
	if err = second.AcquireInstance(ctx); err != nil {
		t.Fatal("lock not released on shutdown")
	}
}
