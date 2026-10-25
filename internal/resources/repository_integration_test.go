//go:build integration

package resources_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"qag/internal/catalog"
	"qag/internal/resources"
	"qag/internal/storage"
)

func TestResourceVersionsAndWriteOnlySecrets(t *testing.T) {
	dsn := os.Getenv("QAG_TEST_DATABASE_URL")
	cfg, err := pgxpool.ParseConfig(dsn)
	if dsn == "" || err != nil || cfg.ConnConfig.Database != "qag_test" {
		t.Fatal("isolated qag_test required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	vault, _ := resources.NewVault(bytes.Repeat([]byte{3}, 32))
	repo := resources.NewRepository(store.Pool, vault)
	env, err := repo.SaveEnvironment(ctx, "", "Окружение "+catalog.NewID(), 0, map[string]string{"TASK_ID": "one"}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		tx, err := store.Pool.Begin(c)
		if err != nil {
			return
		}
		defer tx.Rollback(c)
		_, _ = tx.Exec(c, "UPDATE qag.resources SET current_version_id=NULL WHERE environment_id=$1", env.ID)
		_, _ = tx.Exec(c, "DELETE FROM qag.resource_versions WHERE resource_id IN (SELECT id FROM qag.resources WHERE environment_id=$1)", env.ID)
		_, _ = tx.Exec(c, "DELETE FROM qag.resources WHERE environment_id=$1", env.ID)
		_, _ = tx.Exec(c, "UPDATE qag.environments SET current_version_id=NULL WHERE id=$1", env.ID)
		_, _ = tx.Exec(c, "DELETE FROM qag.environment_versions WHERE environment_id=$1", env.ID)
		_, _ = tx.Exec(c, "DELETE FROM qag.environments WHERE id=$1", env.ID)
		_ = tx.Commit(c)
	}()
	config := resources.Config{BaseURL: "http://127.0.0.1:3000/api", TLS: resources.TLSConfig{Mode: "plaintext"}}
	first, err := repo.SaveResource(ctx, "", env.ID, "AI_GW_SERVISE", "http", 0, config, resources.SecretChanges{Replace: map[string]string{"TOKEN": "sentinel-old-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(first)
	if bytes.Contains(public, []byte("sentinel-old-secret")) {
		t.Fatal("public model contains secret")
	}
	var encrypted []byte
	if err = store.Pool.QueryRow(ctx, "SELECT encrypted_secrets FROM qag.resource_versions WHERE id=$1", first.VersionID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("sentinel-old-secret")) {
		t.Fatal("stored secret is plaintext")
	}
	snapshot, err := repo.Freeze(ctx, env.ID, map[string]string{"AI_GW_SERVISE": "http"})
	if err != nil {
		t.Fatal(err)
	}
	config.BaseURL = "http://127.0.0.1:4000/api"
	second, err := repo.SaveResource(ctx, first.ID, env.ID, first.Name, first.Kind, first.Revision, config, resources.SecretChanges{Replace: map[string]string{"TOKEN": "sentinel-new-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if second.VersionID == first.VersionID {
		t.Fatal("version overwritten")
	}
	if _, err = repo.SaveEnvironment(ctx, env.ID, env.Name, env.Revision, map[string]string{"TASK_ID": "two"}, resources.SecretChanges{}); err != nil {
		t.Fatal(err)
	}
	old, err := repo.Resolve(ctx, snapshot, first.Name)
	if err != nil {
		t.Fatal(err)
	}
	if old.Resource.Config.BaseURL != "http://127.0.0.1:3000/api" || old.Secrets["TOKEN"] != "sentinel-old-secret" || old.Variables["TASK_ID"] != "one" {
		t.Fatal("frozen context changed")
	}
	newSnapshot, err := repo.Freeze(ctx, env.ID, map[string]string{first.Name: "http"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := repo.Resolve(ctx, newSnapshot, first.Name)
	if err != nil || current.Secrets["TOKEN"] != "sentinel-new-secret" || current.Variables["TASK_ID"] != "two" {
		t.Fatal("new context missing updates")
	}
	if _, err = repo.Freeze(ctx, env.ID, map[string]string{first.Name: "grpc"}); err == nil {
		t.Fatal("incompatible kind accepted")
	}
	if _, err = repo.SaveResource(ctx, first.ID, env.ID, first.Name, first.Kind, first.Revision, config, resources.SecretChanges{}); err == nil {
		t.Fatal("stale version accepted")
	}
}
