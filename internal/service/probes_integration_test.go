//go:build integration

package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/datatransfer"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/service"
)

func waitProbe(t *testing.T, f *authoringFixture, id string) execution.ProbeView {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		v, err := f.engine.GetProbe(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !execution.ActiveState(v.Probe.State) {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("probe did not finish")
	return execution.ProbeView{}
}

type diagnosticFaults struct {
	ports.Runs
	denyBegin, denyComplete atomic.Bool
}

func (r *diagnosticFaults) BeginLog(ctx context.Context, l execution.CallLog) error {
	if r.denyBegin.Load() {
		return &problem.Error{Code: "storage_unavailable", Message: "fixture registration failure"}
	}
	return r.Runs.BeginLog(ctx, l)
}
func (r *diagnosticFaults) CompleteLog(ctx context.Context, l execution.CallLog) error {
	if r.denyComplete.Load() {
		return &problem.Error{Code: "storage_unavailable", Message: "fixture result failure"}
	}
	return r.Runs.CompleteLog(ctx, l)
}

func TestProbeCancellationAndResourceDraft(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	received := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.URL.Path == "/slow" {
			received <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
				t.Error("fixture did not observe cancellation")
			}
			return
		}
		if r.ParseForm() != nil || r.Form.Get("local") != "new-local" || r.Form.Get("secret") != "draft-only-secret" || r.Form.Get("env") != "old" {
			t.Error("wrong draft context")
		}
		w.Write([]byte(`{"value":"resource-probe-only"}`))
	}))
	defer srv.Close()
	source := catalog.HTTPHeaderSource{Method: "POST", URL: srv.URL, BodyMode: "form", Form: []catalog.KeyValue{{Name: "local", Value: "${LOCAL}"}, {Name: "secret", Value: "${LOCAL_SECRET}"}, {Name: "env", Value: "${TASK_ID}"}}, JSONPointer: "/value", FetchMode: "per_run", TLS: catalog.TLSConfig{Mode: "plaintext"}}
	in := execution.ProbeInput{Scope: "resource", EnvironmentID: f.env.ID, EnvironmentRevision: f.env.Revision, Source: &source, ResourceVariables: map[string]resources.VariableDefinition{"LOCAL": {Mode: "literal", Value: "new-local"}, "UNUSED_HTTP": {Mode: "http", Source: &catalog.HTTPHeaderSource{}}}, Secrets: &resources.SecretChanges{Replace: map[string]string{"LOCAL_SECRET": "draft-only-secret"}}, IdempotencyKey: catalog.NewID()}
	p, err := f.engine.StartProbe(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	v := waitProbe(t, f, p.ID)
	if !v.ValueAvailable || v.ExtractedValue == nil || *v.ExtractedValue != "resource-probe-only" {
		t.Fatal("resource draft", v)
	}
	before := calls.Load()
	source.URL = srv.URL + "/slow"
	in.IdempotencyKey = catalog.NewID()
	p, err = f.engine.StartProbe(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("source did not start")
	}
	if _, err = f.engine.CancelProbe(f.ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	v = waitProbe(t, f, p.ID)
	if v.Probe.State != "cancelled" || v.ValueAvailable {
		t.Fatal("cancel", v)
	}
	in.EnvironmentRevision = 0
	in.IdempotencyKey = catalog.NewID()
	if _, err = f.engine.StartProbe(f.ctx, in); err == nil || calls.Load() != before+1 {
		t.Fatal("stale revision reached source")
	}
	in.EnvironmentRevision = f.env.Revision
	in.ResourceVariables["LOCAL"] = resources.VariableDefinition{Mode: "literal", Value: "${UNUSED_HTTP}"}
	in.IdempotencyKey = catalog.NewID()
	if _, err = f.engine.StartProbe(f.ctx, in); err == nil || calls.Load() != before+1 {
		t.Fatal("dynamic chain reached source")
	}
	// A failed durable stage registration prevents dispatch; a failed result
	// write exposes pending metadata and retries only persistence.
	in.ResourceVariables["LOCAL"] = resources.VariableDefinition{Mode: "literal", Value: "new-local"}
	source.URL = srv.URL
	faults := &diagnosticFaults{Runs: f.engine.Runs}
	other := service.New(f.ctx, f.c, f.res, faults)
	defer other.Close()
	faults.denyBegin.Store(true)
	in.IdempotencyKey = catalog.NewID()
	p, err = other.StartProbe(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		v, err = other.GetProbe(f.ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !execution.ActiveState(v.Probe.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if v.Probe.State != "error" || calls.Load() != before+1 {
		t.Fatal("network before registration")
	}
	faults.denyBegin.Store(false)
	faults.denyComplete.Store(true)
	in.IdempotencyKey = catalog.NewID()
	p, err = other.StartProbe(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(3 * time.Second)
	pending := false
	for time.Now().Before(until) {
		page, e := other.ListLogs(f.ctx, execution.LogFilter{ProbeID: p.ID})
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) > 0 && page.Items[0].PersistencePending {
			pending = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pending {
		t.Fatal("result not marked pending")
	}
	count := calls.Load()
	faults.denyComplete.Store(false)
	until = time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		v, err = other.GetProbe(f.ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !execution.ActiveState(v.Probe.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !v.ValueAvailable || calls.Load() != count {
		t.Fatal("result write repeated HTTP", v, calls.Load())
	}
}
func TestHTTPVariableProbeDraftAPIAndMasking(t *testing.T) {
	f := authoringApp(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ParseForm() != nil || r.Form.Get("client_secret") != "probe-draft-password" {
			t.Error("draft secret not applied")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"token":"probe-only-token","n":9007199254740993123.125,"empty":"","bool":true,"looksBool":"true","safe":42}`))
	}))
	defer srv.Close()
	source := catalog.HTTPHeaderSource{Method: "POST", URL: srv.URL, BodyMode: "form", Form: []catalog.KeyValue{{Name: "client_secret", Value: "${CLIENT_SECRET}"}}, JSONPointer: "/token", Prefix: "Bearer ", Suffix: "!", FetchMode: "per_run", TLS: catalog.TLSConfig{Mode: "plaintext"}}
	in := execution.ProbeInput{Scope: "environment", EnvironmentID: f.env.ID, EnvironmentRevision: f.env.Revision, Source: &source, Secrets: &resources.SecretChanges{Replace: map[string]string{"CLIENT_SECRET": "probe-draft-password"}}, IdempotencyKey: catalog.NewID()}
	var p execution.HTTPVariableProbe
	if err := json.Unmarshal(f.api(t, "POST", "/http-variable-probes", in, 200), &p); err != nil {
		t.Fatal(err)
	}
	v := waitProbe(t, f, p.ID)
	if v.Probe.State != "completed" || !v.ValueAvailable || v.ExtractedValue == nil || *v.ExtractedValue != "probe-only-token" || v.FormattedValue == nil || *v.FormattedValue != "Bearer probe-only-token!" {
		t.Fatal("preview", v)
	}
	if same, err := f.engine.StartProbe(f.ctx, in); err != nil || same.ID != p.ID || calls.Load() != 1 {
		t.Fatal("repeat", err, calls.Load())
	}
	var logs catalog.Page[execution.LogSummary]
	if err := json.Unmarshal(f.api(t, "GET", "/call-logs?probeId="+p.ID, nil, 200), &logs); err != nil || len(logs.Items) != 1 {
		t.Fatal("logs", logs, err)
	}
	data := f.api(t, "GET", "/call-logs/"+logs.Items[0].ID, nil, 200)
	if strings.Contains(string(data), "probe-only-token") || strings.Contains(string(data), "probe-draft-password") || !strings.Contains(string(data), "9007199254740993123.125") {
		t.Fatal("unsafe or imprecise log", string(data))
	}
	env, err := f.res.GetEnvironment(f.ctx, f.env.ID)
	if err != nil || env.Revision != f.env.Revision || len(env.HTTPVariables) != 0 {
		t.Fatal("probe saved draft", err)
	}
	for _, c := range []struct{ pointer, want, kind string }{{"/n", "9007199254740993123.125", "number"}, {"/empty", "", "string"}, {"/bool", "true", "boolean"}, {"/looksBool", "true", "string"}} {
		source.JSONPointer = c.pointer
		in.IdempotencyKey = catalog.NewID()
		p, err = f.engine.StartProbe(f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		v = waitProbe(t, f, p.ID)
		if !v.ValueAvailable || v.ExtractedValue == nil || *v.ExtractedValue != c.want || v.ValueType != c.kind {
			t.Fatal("scalar", c, v)
		}
	}
	source.JSONPointer = "/missing"
	in.IdempotencyKey = catalog.NewID()
	p, err = f.engine.StartProbe(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	v = waitProbe(t, f, p.ID)
	if v.Probe.State != "error" || v.ValueAvailable {
		t.Fatal("missing pointer", v)
	}
	l, err := f.engine.GetLog(f.ctx, v.Probe.LogID)
	if err != nil || l.BodyOmittedReason == "" || l.Response.Body != "" {
		t.Fatal("unsafe error body", l, err)
	}
	var persisted []byte
	if f.fileStore != nil {
		doc, e := f.fileStore.Snapshot(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		persisted, _ = json.Marshal(doc)
	} else {
		vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
		doc, e := (&datatransfer.PostgreSQL{Store: f.store, Vault: vault}).Snapshot(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		persisted, _ = json.Marshal(doc)
	}
	if bytes.Contains(persisted, []byte("probe-only-token")) || bytes.Contains(persisted, []byte("probe-draft-password")) {
		t.Fatal("live data persisted")
	}
}

func TestMCPHTTPVariableProbeAndLogBothProtocols(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := authoringApp(t)
			session := connectMCP(t, f, version)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Write([]byte(`{"value":"mcp-live-only","public":"kept"}`))
			}))
			defer srv.Close()
			source := catalog.HTTPHeaderSource{Method: "GET", URL: srv.URL, BodyMode: "none", JSONPointer: "/value", FetchMode: "per_run", TLS: catalog.TLSConfig{Mode: "plaintext"}}
			in := execution.ProbeInput{Scope: "environment", EnvironmentID: f.env.ID, EnvironmentRevision: f.env.Revision, Source: &source, IdempotencyKey: catalog.NewID()}
			p := mcpCall[execution.HTTPVariableProbe](t, f, session, "qag_http_variable_probe_start", in)
			waitProbe(t, f, p.ID)
			v := mcpCall[execution.ProbeView](t, f, session, "qag_http_variable_probe_get", map[string]any{"id": p.ID})
			if !v.ValueAvailable || v.ExtractedValue == nil || *v.ExtractedValue != "mcp-live-only" {
				t.Fatal("MCP preview", v)
			}
			logs := mcpCall[catalog.Page[execution.LogSummary]](t, f, session, "qag_call_logs_list", map[string]any{"probeId": p.ID, "limit": 1})
			if len(logs.Items) != 1 {
				t.Fatal(logs)
			}
			l := mcpCall[execution.CallLog](t, f, session, "qag_call_log_get", map[string]any{"id": logs.Items[0].ID})
			if strings.Contains(l.Response.Body, "mcp-live-only") || !strings.Contains(l.Response.Body, "kept") {
				t.Fatal("MCP log", l)
			}
			mcpCall[execution.HTTPVariableProbe](t, f, session, "qag_http_variable_probe_start", in)
			if calls.Load() != 1 {
				t.Fatal("repeated source")
			}
			if strings.Contains(f.audit.String(), "mcp-live-only") {
				t.Fatal("audit leaked live value")
			}
		})
	}
}
