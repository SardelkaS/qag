//go:build cli

package deployment_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func nativeCall[T any](t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args any) T {
	t.Helper()
	var out T
	r, e := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		t.Fatal(name, e)
	}
	b, e := json.Marshal(r.StructuredContent)
	if e != nil {
		t.Fatal(e)
	}
	var env struct {
		Data  json.RawMessage      `json:"data"`
		Error *mcpserver.ToolError `json:"error"`
	}
	if e = json.Unmarshal(b, &env); e != nil || r.IsError || env.Error != nil {
		t.Fatalf("%s: %v %+v", name, e, env.Error)
	}
	if e = json.Unmarshal(env.Data, &out); e != nil {
		t.Fatal(name, e)
	}
	return out
}
func nativeArtifact(t *testing.T, ctx context.Context, s *mcp.ClientSession, a mcpserver.Artifact) []byte {
	t.Helper()
	var b []byte
	for offset := int64(0); ; {
		p := nativeCall[mcpserver.ArtifactChunk](t, ctx, s, "qag_artifact_read", map[string]any{"id": a.ID, "offset": offset})
		part, e := base64.StdEncoding.DecodeString(p.DataBase64)
		if e != nil || p.Offset != offset || len(part) != p.Length {
			t.Fatal("artifact chunk")
		}
		b = append(b, part...)
		offset += int64(len(part))
		if p.EOF {
			break
		}
		if len(part) == 0 {
			t.Fatal("empty artifact")
		}
	}
	h := sha256.Sum256(b)
	if int64(len(b)) != a.Size || hex.EncodeToString(h[:]) != a.SHA256 {
		t.Fatal("artifact hash")
	}
	return b
}
func TestInstalledCLIFullYAMLLifecycle(t *testing.T) {
	binary := os.Getenv("QAG_CLI_BINARY")
	if binary == "" {
		t.Fatal("set QAG_CLI_BINARY to an installed binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dir := t.TempDir()
	file := filepath.Join(dir, "workspace.yaml")
	env := []string{}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(strings.ToUpper(k), "QAG_") && strings.ToUpper(k) != "PATH" {
			env = append(env, v)
		}
	}
	env = append(env, "PATH=", "QAG_DEMO_GRPC_ADDRESS=127.0.0.1:0")
	runCommand := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = dir
		cmd.Env = env
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("qag %s: %v %s", args[0], e, b)
		}
		return b
	}
	if !bytes.Contains(runCommand("--help"), []byte("--data-file")) {
		t.Fatal("help")
	}
	version := string(runCommand("--version"))
	t.Log(strings.TrimSpace(version))
	start := func(data string) (string, func()) {
		t.Helper()
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		addr := l.Addr().String()
		l.Close()
		var log bytes.Buffer
		cmd := exec.CommandContext(ctx, binary, "serve", "--data-file", data, "--address", addr)
		cmd.Dir = dir
		cmd.Env = env
		cmd.Stdout = &log
		cmd.Stderr = &log
		if e = cmd.Start(); e != nil {
			t.Fatal(e)
		}
		stopped := false
		stop := func() {
			if !stopped {
				stopped = true
				cmd.Process.Kill()
				cmd.Wait()
			}
		}
		t.Cleanup(stop)
		base := "http://" + addr
		client := &http.Client{Timeout: time.Second}
		ready := false
		for n := 0; n < 100; n++ {
			resp, e := client.Get(base + "/api/v1/health")
			if e == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !ready {
			stop()
			t.Fatal("native readiness", log.String())
		}
		for _, p := range []string{"/", "/favicon.ico", "/fonts/NotoSans-Regular.ttf"} {
			resp, e := client.Get(base + p)
			if e != nil {
				t.Fatal(e)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || len(b) == 0 {
				t.Fatal("embedded asset", p)
			}
		}
		if b := runCommand("healthcheck", "--address", addr); len(b) != 0 {
			t.Fatal("healthcheck output")
		}
		return base, stop
	}
	base, stop := start(file)
	client := mcp.NewClient(&mcp.Implementation{Name: "installed-qag-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	tools, e := session.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 50 {
		t.Fatal("MCP discovery", e)
	}
	if !strings.Contains(session.InitializeResult().Instructions, mcpserver.GuideURI) {
		t.Fatal("installed instructions missing")
	}
	guide, guideErr := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpserver.GuideURI})
	if guideErr != nil || len(guide.Contents) != 1 || !strings.Contains(guide.Contents[0].Text, "qag_run_step_execute") {
		t.Fatal("installed embedded guide", guideErr)
	}
	info := nativeCall[builtin.Info](t, ctx, session, "qag_demo_get", map[string]any{})
	tc := nativeCall[catalog.TestCase](t, ctx, session, "qag_test_case_get", map[string]any{"id": info.TestCaseID})
	jira := nativeCall[mcpserver.ExportResult](t, ctx, session, "qag_test_case_export", map[string]any{"id": tc.ID, "revision": tc.Revision, "format": "jira-wiki"})
	if len(jira.Files) != 1 || !bytes.Contains(nativeArtifact(t, ctx, session, jira.Files[0]), []byte("h1.")) {
		t.Fatal("installed Jira export")
	}
	selected := nativeCall[runs.Run](t, ctx, session, "qag_run_start", map[string]any{"testCaseId": tc.ID, "environmentId": info.EnvironmentID, "revision": tc.Revision, "historyPolicy": "unretained", "idempotencyKey": catalog.NewID()})
	nativeCall[runs.Batch](t, ctx, session, "qag_run_step_execute", map[string]any{"runId": selected.ID, "stepId": tc.Steps[0].ID, "idempotencyKey": catalog.NewID()})
	for i := 0; i < 100; i++ {
		selected = nativeCall[runs.Run](t, ctx, session, "qag_run_get", map[string]any{"id": selected.ID})
		if selected.Batches[0].State == "completed" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(selected.Attempts) != 1 || selected.Attempts[0].Result == nil || selected.Attempts[0].Result.Comparison.Status != "match" {
		t.Fatal("installed selected HTTP step")
	}
	selected = nativeCall[runs.Run](t, ctx, session, "qag_run_cancel", map[string]any{"id": selected.ID})
	if len(selected.RetentionReasons) != 0 || len(selected.Batches) != 1 || selected.Batches[0].StepID == "" {
		t.Fatal("installed selected-step policy")
	}
	run := nativeCall[runs.Run](t, ctx, session, "qag_run_start", map[string]any{"testCaseId": tc.ID, "environmentId": info.EnvironmentID, "revision": tc.Revision, "executeAll": true, "idempotencyKey": catalog.NewID()})
	done := false
	for i := 0; i < 100; i++ {
		run = nativeCall[runs.Run](t, ctx, session, "qag_run_get", map[string]any{"id": run.ID})
		if len(run.Batches) == 1 && run.Batches[0].State == "completed" {
			done = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !done || len(run.Attempts) != 5 {
		t.Fatal("demo queue", len(run.Attempts))
	}
	sqlErrors := 0
	for _, a := range run.Attempts {
		if a.Resource.Kind == "postgresql" {
			if a.State != "error" || a.Result == nil || a.Result.ErrorCode != "demo_sql_unavailable" {
				t.Fatal("SQL placeholder")
			}
			sqlErrors++
		} else if a.State != "completed" || a.Result == nil || a.Result.Comparison.Status != "match" {
			t.Fatalf("demo protocol: %+v", a)
		}
	}
	if sqlErrors != 1 {
		t.Fatal("SQL step disappeared")
	}
	steps := []runs.StepRevision{}
	for _, s := range run.StepStates {
		steps = append(steps, runs.StepRevision{StepID: s.StepID, Revision: s.Revision})
	}
	nativeCall[runs.Run](t, ctx, session, "qag_run_steps_pass_all", map[string]any{"runId": run.ID, "steps": steps})
	run = nativeCall[runs.Run](t, ctx, session, "qag_run_finish", map[string]any{"id": run.ID})
	if run.Status == "passed" || run.Status == "running" {
		t.Fatal("SQL error became success")
	}
	report := nativeCall[runs.Report](t, ctx, session, "qag_report_prepare", map[string]any{"runId": run.ID, "includeExpected": false})
	export := nativeCall[mcpserver.ExportResult](t, ctx, session, "qag_report_export", map[string]any{"runId": run.ID, "snapshotId": report.SnapshotID, "hash": report.Hash, "includeExpected": false, "format": "pdf"})
	if len(export.Files) != 1 {
		t.Fatal("PDF export")
	}
	pdf := nativeArtifact(t, ctx, session, export.Files[0])
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatal("PDF bytes")
	}
	var effects atomic.Int32
	entered := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		effects.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer target.Close()
	folder := nativeCall[catalog.Folder](t, ctx, session, "qag_folder_create", map[string]any{"name": "Восстановление"})
	userEnv := nativeCall[resources.Environment](t, ctx, session, "qag_environment_create", map[string]any{"name": "Внешний HTTP"})
	nativeCall[resources.Resource](t, ctx, session, "qag_resource_create", map[string]any{"environmentId": userEnv.ID, "name": "SLOW", "kind": "http", "config": resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}}})
	httpStatus := 200
	userCase := nativeCall[catalog.TestCase](t, ctx, session, "qag_test_case_create", map[string]any{"folderId": folder.ID, "name": "Прерывание"})
	userCase = nativeCall[catalog.TestCase](t, ctx, session, "qag_test_case_save", map[string]any{"id": userCase.ID, "revision": userCase.Revision, "steps": []catalog.Step{{ID: catalog.NewID(), Description: "Дождаться внешнего действия", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "http", Resource: "SLOW", TimeoutSeconds: 60, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/wait", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &httpStatus, BodyMode: "none"}}}}}})
	pendingArgs := map[string]any{"testCaseId": userCase.ID, "environmentId": userEnv.ID, "revision": userCase.Revision, "executeAll": true, "idempotencyKey": catalog.NewID()}
	pending := nativeCall[runs.Run](t, ctx, session, "qag_run_start", pendingArgs)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("slow target not reached")
	}
	session.Close()
	stop()
	backup := filepath.Join(dir, "backup.yaml")
	runCommand("data", "export", "--data-file", file, "--output", backup)
	copy := filepath.Join(dir, "copy.yaml")
	runCommand("data", "import", "--data-file", copy, "--input", backup, "--dry-run")
	if _, e = os.Stat(copy); !os.IsNotExist(e) {
		t.Fatal("dry run wrote data")
	}
	runCommand("data", "import", "--data-file", copy, "--input", backup)
	base, stop = start(copy)
	defer stop()
	session, e = client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	old := nativeCall[runs.Run](t, ctx, session, "qag_run_get", map[string]any{"id": run.ID})
	unretained := nativeCall[runs.Run](t, ctx, session, "qag_run_get", map[string]any{"id": selected.ID})
	if unretained.HistoryPolicy != "unretained" || len(unretained.RetentionReasons) != 0 || len(unretained.Attempts) != 1 || unretained.Batches[0].StepID != tc.Steps[0].ID {
		t.Fatal("unretained lost after import/restart")
	}
	newInfo := nativeCall[builtin.Info](t, ctx, session, "qag_demo_get", map[string]any{})
	if old.Status != run.Status || len(old.Attempts) != 5 || newInfo.TestCaseID != info.TestCaseID {
		t.Fatal("restart/import changed history")
	}
	recovered := nativeCall[runs.Run](t, ctx, session, "qag_run_get", map[string]any{"id": pending.ID})
	if recovered.Status != "interrupted" || len(recovered.Attempts) != 1 || recovered.Attempts[0].State != "interrupted" {
		t.Fatal("active state replayed after import")
	}
	retry := nativeCall[runs.Run](t, ctx, session, "qag_run_start", pendingArgs)
	if retry.ID != pending.ID || effects.Load() != 1 {
		t.Fatal("idempotency lost after import/restart")
	}
	t.Log(fmt.Sprintf("YAML: UI/MCP, 5 demo calls, SQL error, PDF %d bytes, export/import and restart verified", len(pdf)))
}
