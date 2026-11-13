//go:build integration

package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/reporting"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

func TestMCPResourceVariablesStepAndJira(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := authoringApp(t)
			s := connectMCP(t, f, version)
			env := mcpCall[resources.Environment](t, f, s, "qag_variables_set", map[string]any{"id": f.env.ID, "revision": f.env.Revision, "variables": map[string]any{" ": 123, "": map[string]any{"ignored": true}, "STATIC": "value"}, "secrets": map[string]any{"replace": map[string]any{"": false, " \t": 123, "KEY": "kept"}}})
			if len(env.Variables) != 1 || len(env.SecretNames) != 2 {
				t.Fatal("blank environment names")
			}
			apiEnv := f.api(t, "PUT", "/environments/"+env.ID+"/variables", map[string]any{"revision": env.Revision, "variables": map[string]any{"": false, " ": 123, "STATIC": "value"}, "secrets": map[string]any{"replace": map[string]any{"": false}, "clear": []string{" "}}}, 200)
			if err := json.Unmarshal(apiEnv, &env); err != nil || len(env.Variables) != 1 || len(env.SecretNames) != 2 {
				t.Fatal("API blank normalization", err)
			}
			var sources, mains atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sources.Add(1)
				w.Write([]byte(`{"token":"dynamic-private-sentinel"}`))
			}))
			defer source.Close()
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mains.Add(1)
				if r.Header.Get("Authorization") != "Bearer dynamic-private-sentinel" || r.Header.Get("X-Override") != "own" {
					t.Error("effective headers")
				}
				fmt.Fprintf(w, `{"echo":%q}`, r.Header.Get("Authorization"))
			}))
			defer target.Close()
			src := &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "none", JSONPointer: "/token", FetchMode: "per_run"}
			cfg := resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Variables: map[string]resources.VariableDefinition{"": {Mode: "invalid"}, " ": {Mode: "http"}, "TOKEN_VALUE": {Mode: "http", Source: src}}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${TOKEN_VALUE}"}, {ID: catalog.NewID(), Name: "X-Override", Source: src}}}
			r := mcpCall[resources.Resource](t, f, s, "qag_resource_create", mcpserver.ResourceCreate{EnvironmentID: env.ID, ResourceData: mcpserver.ResourceData{Name: "TARGET", Kind: "http", Config: cfg}})
			if len(r.Config.Variables) != 1 || sources.Load() != 0 {
				t.Fatal("save fetched or retained blanks")
			}
			c := f.caseInFolder(t)
			code := 200
			block := func() catalog.Block {
				return catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: r.Name, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none", Headers: []catalog.RequestHeader{{Name: "x-override", Value: "own"}}}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
			}
			steps := []catalog.Step{{ID: catalog.NewID(), Description: "Выбранный", Blocks: []catalog.Block{block(), block()}}, {ID: catalog.NewID(), Description: "Не выбранный", Blocks: []catalog.Block{block()}}, {ID: catalog.NewID(), Description: "Ручной шаг"}}
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "steps": steps})
			for _, expected := range []bool{true, false} {
				raw := f.api(t, "GET", fmt.Sprintf("/test-cases/%s/export?revision=%d&includeExpected=%t", c.ID, c.Revision, expected), nil, 200)
				var ui reporting.CaseExport
				json.Unmarshal(raw, &ui)
				exp := mcpCall[mcpserver.ExportResult](t, f, s, "qag_test_case_export", mcpserver.CaseExportInput{ID: c.ID, Revision: c.Revision, Format: "jira-wiki", IncludeExpected: &expected})
				if len(exp.Files) != 1 || !bytes.Equal(artifactBytes(t, f, s, exp.Files[0]), []byte(ui.Text)) {
					t.Fatal("Jira API/MCP bytes differ")
				}
			}
			if sources.Load() != 0 || mains.Load() != 0 {
				t.Fatal("export executed network")
			}
			startArgs := map[string]any{"testCaseId": c.ID, "environmentId": env.ID, "revision": c.Revision, "historyPolicy": "unretained", "idempotencyKey": catalog.NewID()}
			run := mcpCall[runs.Run](t, f, s, "qag_run_start", startArgs)
			if sources.Load() != 0 {
				t.Fatal("start fetched source")
			}
			batchArgs := map[string]any{"runId": run.ID, "stepId": steps[0].ID, "idempotencyKey": catalog.NewID()}
			batch := mcpCall[runs.Batch](t, f, s, "qag_run_step_execute", batchArgs)
			run = waitBatch(t, f, run.ID)
			if run.Status != "running" || len(run.Attempts) != 2 || sources.Load() != 1 || mains.Load() != 2 || batch.StepID != steps[0].ID {
				t.Fatal("step selection/cache")
			}
			for _, state := range run.StepStates {
				if state.Status != "not_run" {
					t.Fatal("automatic manual status")
				}
			}
			again := mcpCall[runs.Batch](t, f, s, "qag_run_step_execute", batchArgs)
			if again.ID != batch.ID || sources.Load() != 1 {
				t.Fatal("step replay")
			}
			repeat := mcpCall[runs.Batch](t, f, s, "qag_run_step_execute", map[string]any{"runId": run.ID, "stepId": steps[0].ID, "idempotencyKey": catalog.NewID()})
			for i := 0; i < 100; i++ {
				run = mcpGetRun(t, f, s, run.ID)
				if len(run.Batches) == 2 && run.Batches[1].State == "completed" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if repeat.ID == batch.ID || len(run.Attempts) != 4 || run.Batches[1].State != "completed" || sources.Load() != 1 || mains.Load() != 4 {
				t.Fatal("explicit repeat/retained cache")
			}
			encoded, _ := json.Marshal(run)
			if bytes.Contains(encoded, []byte("dynamic-private-sentinel")) || len(run.Attempts[0].Result.VariableSources) != 1 || !run.Attempts[0].Result.Redacted {
				t.Fatal("source privacy/trace")
			}
			empty := mcpCall[runs.Batch](t, f, s, "qag_run_step_execute", map[string]any{"runId": run.ID, "stepId": steps[2].ID, "idempotencyKey": catalog.NewID()})
			if empty.State != "completed" || len(empty.Items) != 0 {
				t.Fatal("manual queue")
			}
			run = mcpCall[runs.Run](t, f, s, "qag_run_finish", map[string]any{"id": run.ID})
			if run.Status != "blocked" || len(run.RetentionReasons) != 0 {
				t.Fatal("unretained/no false pass")
			}
			mcpError(t, f, s, "qag_run_step_execute", map[string]any{"runId": run.ID, "stepId": steps[0].ID, "idempotencyKey": catalog.NewID()}, "conflict")
			retained := mcpCall[runs.Run](t, f, s, "qag_run_start", map[string]any{"testCaseId": c.ID, "environmentId": env.ID, "revision": c.Revision, "historyPolicy": "retain", "executeAll": true, "idempotencyKey": catalog.NewID()})
			retained = waitBatch(t, f, retained.ID)
			retained = mcpCall[runs.Run](t, f, s, "qag_run_cancel", map[string]any{"id": retained.ID})
			if strings.Join(retained.RetentionReasons, ",") != "first_run,manual" {
				t.Fatal("retain", retained.RetentionReasons)
			}
		})
	}
}
