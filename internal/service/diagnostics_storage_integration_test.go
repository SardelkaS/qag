//go:build integration

package service_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/datatransfer"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestDiagnosticsStorageAndPortableSnapshot(t *testing.T) {
	f := authoringApp(t)
	repo := f.engine.Runs
	at := time.Now().UTC().Truncate(time.Microsecond)
	p := execution.HTTPVariableProbe{ID: catalog.NewID(), Scope: "environment", EnvironmentID: f.env.ID, State: "running", StartedAt: at}
	key := catalog.NewID()
	hash := strings.Repeat("a", 64)
	if _, created, e := repo.BeginProbe(f.ctx, p, key, hash); e != nil || !created {
		t.Fatal("begin probe", created, e)
	}
	if same, created, e := repo.BeginProbe(f.ctx, p, key, hash); e != nil || created || same.ID != p.ID {
		t.Fatal("idempotency", created, e)
	}
	if _, _, e := repo.BeginProbe(f.ctx, p, key, strings.Repeat("b", 64)); e == nil {
		t.Fatal("different hash accepted")
	}
	for i := 0; i < 5; i++ {
		l := execution.CallLog{Summary: execution.LogSummary{ID: catalog.NewID(), ProbeID: p.ID, EnvironmentID: f.env.ID, EnvironmentName: f.env.Name, Protocol: "http", Origin: "variable_probe", Stage: "variable_probe", State: "running", StartedAt: at, Position: i + 1}, Request: execution.LogRequest{Protocol: "http", Method: "POST", Body: "redacted"}}
		if e := repo.BeginLog(f.ctx, l); e != nil {
			t.Fatal("begin log", e)
		}
		if i == 0 {
			l.Summary.State = "completed"
			l.Summary.FinishedAt = &at
			l.Response = &execution.Result{Protocol: "http", Body: `{"n":9007199254740993123.125}`}
			if e := repo.CompleteLog(f.ctx, l); e != nil {
				t.Fatal("complete", e)
			}
			if e := repo.CompleteLog(f.ctx, l); e != nil {
				t.Fatal("repeat complete", e)
			}
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, e := repo.ListLogs(f.ctx, execution.LogFilter{Limit: 2, Cursor: cursor, ProbeID: p.ID})
		if e != nil {
			t.Fatal("list", e)
		}
		for _, s := range page.Items {
			if seen[s.ID] {
				t.Fatal("duplicate row")
			}
			seen[s.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatal("lost rows", len(seen))
	}
	if page, e := repo.ListLogs(f.ctx, execution.LogFilter{State: "completed", Protocol: "http", From: at.Format(time.RFC3339Nano)}); e != nil || len(page.Items) != 1 {
		t.Fatal("filters", page, e)
	}
	var doc dataset.Document
	var err error
	if f.fileStore != nil {
		doc, err = f.fileStore.Snapshot(f.ctx)
	} else {
		vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
		doc, err = (&datatransfer.PostgreSQL{Store: f.store, Vault: vault}).Snapshot(f.ctx)
	}
	if err != nil {
		t.Fatal("snapshot", err)
	}
	encoded, err := dataset.Encode(doc)
	if err != nil {
		t.Fatal("encode", err)
	}
	decoded, err := dataset.Decode(bytes.NewReader(encoded))
	if err != nil || len(decoded.Data.CallLogs) != 5 || len(decoded.Data.HTTPVariableProbes) != 1 {
		t.Fatal("portable graph", err)
	}
	if _, _, err = dataset.Merge(decoded, decoded); err != nil {
		t.Fatal("repeat merge", err)
	}
	if e := f.store.Migrate(f.ctx); e != nil {
		t.Fatal("repeat migration", e)
	}
	if e := repo.Recover(f.ctx); e != nil {
		t.Fatal("recover", e)
	}
	got, e := repo.GetProbe(f.ctx, p.ID)
	if e != nil || got.State != "interrupted" {
		t.Fatal("probe recovery", e)
	}
	if page, e := repo.ListLogs(f.ctx, execution.LogFilter{State: "interrupted"}); e != nil || len(page.Items) != 4 {
		t.Fatal("log recovery", page, e)
	}
}
