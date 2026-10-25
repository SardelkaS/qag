//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"qag/internal/catalog"
	"qag/internal/problem"
	"qag/internal/storage"
)

func TestCatalogPersistenceRevisionAndArchive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("QAG_TEST_DATABASE_URL")
	cfg, err := pgxpool.ParseConfig(dsn)
	if dsn == "" || err != nil || cfg.ConnConfig.Database != "qag_test" {
		t.Fatal("isolated qag_test required")
	}
	store, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := catalog.NewRepository(store.Pool)
	folder, err := repo.CreateFolder(ctx, "Каталог "+catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_, _ = store.Pool.Exec(cleanup, "DELETE FROM qag.test_cases WHERE folder_id=$1", folder.ID)
		_, _ = store.Pool.Exec(cleanup, "DELETE FROM qag.folders WHERE id=$1", folder.ID)
	}()
	if _, err = repo.CreateFolder(ctx, strings.ToUpper(folder.Name)); err == nil {
		t.Fatal("case-insensitive duplicate accepted")
	}
	yadro, err := repo.CreateCase(ctx, folder.ID, "Ядро")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := repo.CreateCase(ctx, folder.ID, "Бета")
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := repo.CreateCase(ctx, folder.ID, "Альфа")
	if err != nil {
		t.Fatal(err)
	}
	status := 200
	steps := []catalog.Step{{ID: catalog.NewID(), Description: "Проверить результат", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "POST", Path: "/task", BodyMode: "json", Body: `{"name":"test"}`}, Expected: catalog.Expectation{StatusCode: &status, BodyMode: "json", Body: `{"ok":true}`}}}}}
	saved, err := repo.SaveCase(ctx, yadro.ID, yadro.Name, yadro.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 2 || saved.Steps[0].Blocks[0].TimeoutSeconds != 30 {
		t.Fatal("revision/default not persisted")
	}
	_, err = repo.SaveCase(ctx, yadro.ID, "Не должен сохраниться", yadro.Revision, steps)
	var issue *problem.Error
	if !errors.As(err, &issue) || issue.Code != "conflict" {
		t.Fatalf("stale save not rejected: %v", err)
	}
	bad := []catalog.Step{{ID: catalog.NewID(), Description: ""}}
	if _, err = repo.SaveCase(ctx, yadro.ID, "Тоже не сохранять", saved.Revision, bad); err == nil {
		t.Fatal("invalid step saved")
	}
	current, err := repo.GetCase(ctx, yadro.ID)
	if err != nil || current.Name != "Ядро" || current.Revision != 2 {
		t.Fatal("failed save changed document")
	}
	archived := true
	firstArchive, err := repo.PatchCase(ctx, alpha.ID, nil, &archived)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := repo.PatchCase(ctx, alpha.ID, nil, &archived)
	if err != nil || repeated.Revision != firstArchive.Revision {
		t.Fatal("archive is not idempotent")
	}
	page, err := repo.ListCases(ctx, folder.ID, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != beta.ID || page.Items[1].ID != yadro.ID || page.NextCursor == "" {
		t.Fatalf("bad first page: %+v", page)
	}
	last, err := repo.ListCases(ctx, folder.ID, 2, page.NextCursor)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != alpha.ID {
		t.Fatal("archive pagination failed")
	}
	if _, err = repo.PatchFolder(ctx, folder.ID, nil, &archived); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CreateCase(ctx, folder.ID, "Запрещено"); err == nil {
		t.Fatal("created in archived folder")
	}
	if _, err = repo.SaveCase(ctx, yadro.ID, yadro.Name, current.Revision, current.Steps); err == nil {
		t.Fatal("edited in archived folder")
	}
	restored := false
	if _, err = repo.PatchFolder(ctx, folder.ID, nil, &restored); err != nil {
		t.Fatal(err)
	}
	alphaNow, err := repo.GetCase(ctx, alpha.ID)
	if err != nil || !alphaNow.Archived || alphaNow.ParentArchived {
		t.Fatal("own archive flag lost")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, description := range []string{"Изменение A", "Изменение B"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			_, err := repo.SaveCase(ctx, beta.ID, beta.Name, beta.Revision, []catalog.Step{{ID: catalog.NewID(), Description: text}})
			results <- err
		}(description)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.As(err, &issue) && issue.Code == "conflict" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("concurrent saves: success=%d conflicts=%d", success, conflicts)
	}
	reopened, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, err := catalog.NewRepository(reopened.Pool).GetCase(ctx, yadro.ID)
	if err != nil || len(persisted.Steps) != 1 || persisted.Steps[0].Blocks[0].Expected.Body != `{"ok":true}` {
		t.Fatal("scenario not durable")
	}
}
