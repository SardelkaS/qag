package filestore_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/filestore"
)

func TestDiagnosticDurabilityPagingConflictsAndRecovery(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "diagnostics.yaml")
	s, err := filestore.Open(file, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	res := &filestore.Resources{Store: s}
	repo := &filestore.Runs{Store: s}
	env, err := res.SaveEnvironmentMetadata(ctx, "", "Diagnostics", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	p := execution.HTTPVariableProbe{ID: catalog.NewID(), Scope: "environment", EnvironmentID: env.ID, State: "running", StartedAt: at}
	key := catalog.NewID()
	hash := strings.Repeat("a", 64)
	if _, created, e := repo.BeginProbe(ctx, p, key, hash); e != nil || !created {
		t.Fatal("begin", created, e)
	}
	if same, created, e := repo.BeginProbe(ctx, p, key, hash); e != nil || created || same.ID != p.ID {
		t.Fatal("repeat", created, e)
	}
	if _, _, e := repo.BeginProbe(ctx, p, key, strings.Repeat("b", 64)); e == nil {
		t.Fatal("conflict accepted")
	}
	for i := 0; i < 5; i++ {
		l := execution.CallLog{Summary: execution.LogSummary{ID: catalog.NewID(), ProbeID: p.ID, EnvironmentID: env.ID, EnvironmentName: env.Name, Protocol: "http", Origin: "variable_probe", Stage: "variable_probe", State: "running", Position: i + 1, StartedAt: at}, Request: execution.LogRequest{Protocol: "http", Method: "POST", Body: "masked"}}
		if e := repo.BeginLog(ctx, l); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			l.Summary.State = "completed"
			l.Summary.FinishedAt = &at
			l.Response = &execution.Result{Protocol: "http", Body: `{"public":1,"token":"[REDACTED]"}`}
			if e := repo.CompleteLog(ctx, l); e != nil {
				t.Fatal(e)
			}
			if e := repo.CompleteLog(ctx, l); e != nil {
				t.Fatal("repeat complete", e)
			}
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, e := repo.ListLogs(ctx, execution.LogFilter{Limit: 2, Cursor: cursor, EnvironmentID: env.ID})
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Items {
			if seen[v.ID] {
				t.Fatal("duplicate cursor row")
			}
			seen[v.ID] = true
			if v.Legacy {
				t.Fatal("unexpected legacy")
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatal("missing rows", len(seen))
	}
	before, _ := s.Snapshot(ctx)
	s.Failpoint = func(stage string) error {
		if stage == "before-replace" {
			return errors.New("fixture write failure")
		}
		return nil
	}
	l := execution.CallLog{Summary: execution.LogSummary{ID: catalog.NewID(), ProbeID: p.ID, EnvironmentID: env.ID, Protocol: "http", Origin: "variable_probe", Stage: "variable_probe", State: "running", StartedAt: at}, Request: execution.LogRequest{Protocol: "http"}}
	if e := repo.BeginLog(ctx, l); e == nil {
		t.Fatal("failed write accepted")
	}
	s.Failpoint = nil
	after, _ := s.Snapshot(ctx)
	if len(after.Data.CallLogs) != len(before.Data.CallLogs) {
		t.Fatal("unconfirmed row published")
	}
	if _, _, e := dataset.Merge(after, after); e != nil {
		t.Fatal("identical merge", e)
	}
	changed, _ := dataset.Clone(after)
	changed.Data.CallLogs[0].Payload = dataset.JSON(strings.Replace(string(changed.Data.CallLogs[0].Payload), `"body":"masked"`, `"body":"other"`, 1))
	if _, _, e := dataset.Merge(after, changed); e == nil {
		t.Fatal("different ID content accepted")
	}
	s.Close()
	s, err = filestore.Open(file, false)
	if err != nil {
		t.Fatal(err)
	}
	repo = &filestore.Runs{Store: s}
	if e := repo.Recover(ctx); e != nil {
		t.Fatal(e)
	}
	got, e := repo.GetProbe(ctx, p.ID)
	if e != nil || got.State != "interrupted" {
		t.Fatal("recovery", got.State, e)
	}
	page, e := repo.ListLogs(ctx, execution.LogFilter{State: "interrupted"})
	if e != nil || len(page.Items) != 4 {
		t.Fatal("log recovery", len(page.Items), e)
	}
	doc, e := s.Snapshot(ctx)
	if e != nil || doc.Version != 4 {
		t.Fatal("v4", e)
	}
}
