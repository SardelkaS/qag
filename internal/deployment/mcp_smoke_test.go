//go:build integration || container

package deployment_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/runs"
)

func smokeCall[T any](t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, args any) T {
	t.Helper()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(name, err)
	}
	raw, _ := json.Marshal(r.StructuredContent)
	var envelope struct {
		Data  json.RawMessage      `json:"data"`
		Error *mcpserver.ToolError `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil || r.IsError || envelope.Error != nil {
		t.Fatalf("%s: %+v", name, envelope.Error)
	}
	var out T
	if err = json.Unmarshal(envelope.Data, &out); err != nil {
		t.Fatal(name, err)
	}
	return out
}
func smokeDownload(t *testing.T, ctx context.Context, s *mcp.ClientSession, a mcpserver.Artifact) []byte {
	t.Helper()
	var out []byte
	for offset := int64(0); ; {
		chunk := smokeCall[mcpserver.ArtifactChunk](t, ctx, s, "qag_artifact_read", map[string]any{"id": a.ID, "offset": offset})
		data, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, data...)
		offset += int64(len(data))
		if chunk.EOF {
			break
		}
		if len(data) == 0 {
			t.Fatal("empty chunk")
		}
	}
	h := sha256.Sum256(out)
	if int64(len(out)) != a.Size || hex.EncodeToString(h[:]) != a.SHA256 {
		t.Fatal("file integrity")
	}
	return out
}
func smokeMCP(t *testing.T, ctx context.Context, base string, linux bool) string {
	t.Helper()
	lastRun := ""
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		sdk := mcp.NewClient(&mcp.Implementation{Name: "qag-runtime-smoke", Version: "1"}, nil)
		s, err := sdk.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, &mcp.ClientSessionOptions{ProtocolVersion: version})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.InitializeResult().ProtocolVersion != version {
			t.Fatal("protocol negotiation")
		}
		tools, err := s.ListTools(ctx, nil)
		if err != nil || len(tools.Tools) != 45 {
			t.Fatal("runtime tools", err)
		}
		info := smokeCall[builtin.Info](t, ctx, s, "qag_demo_get", map[string]any{})
		doc := smokeCall[catalog.TestCase](t, ctx, s, "qag_test_case_get", map[string]any{"id": info.TestCaseID})
		run := smokeCall[runs.Run](t, ctx, s, "qag_run_start", map[string]any{"testCaseId": doc.ID, "environmentId": info.EnvironmentID, "revision": doc.Revision, "executeAll": true, "idempotencyKey": catalog.NewID()})
		for n := 0; n < 200; n++ {
			run = smokeCall[runs.Run](t, ctx, s, "qag_run_get", map[string]any{"id": run.ID})
			if len(run.Batches) > 0 && run.Batches[0].State == "completed" {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if len(run.Attempts) != 5 || len(run.Batches) != 1 || run.Batches[0].State != "completed" {
			t.Fatal("MCP demo queue incomplete")
		}
		for _, a := range run.Attempts {
			if a.State != "completed" || a.Result == nil || a.Result.Comparison.Status != "match" {
				t.Fatal("MCP demo protocol mismatch")
			}
		}
		revisions := []runs.StepRevision{}
		for _, step := range run.StepStates {
			revisions = append(revisions, runs.StepRevision{StepID: step.StepID, Revision: step.Revision})
		}
		smokeCall[runs.Run](t, ctx, s, "qag_run_steps_pass_all", map[string]any{"runId": run.ID, "steps": revisions})
		run = smokeCall[runs.Run](t, ctx, s, "qag_run_finish", map[string]any{"id": run.ID})
		if run.Status != "passed" {
			t.Fatal("MCP demo did not pass")
		}
		lastRun = run.ID
		report := smokeCall[runs.Report](t, ctx, s, "qag_report_prepare", map[string]any{"runId": run.ID, "includeExpected": true})
		for _, format := range []string{"pdf", "text", "markdown", "rich"} {
			args := map[string]any{"runId": run.ID, "snapshotId": report.SnapshotID, "hash": report.Hash, "includeExpected": true, "format": format}
			exported := smokeCall[mcpserver.ExportResult](t, ctx, s, "qag_report_export", args)
			if len(exported.Files) != 1 && (format != "rich" || len(exported.Files) != 3) {
				t.Fatal("runtime export incomplete")
			}
			for _, a := range exported.Files {
				data := smokeDownload(t, ctx, s, a)
				if format == "pdf" && !bytes.HasPrefix(data, []byte("%PDF-")) {
					t.Fatal("invalid runtime PDF")
				}
				if a.MIMEType == "text/html; charset=utf-8" && !bytes.Contains(data, []byte("data:image/png;base64,")) {
					t.Fatal("rich missing inline PNG")
				}
			}
			if linux && format != "pdf" {
				copied := smokeCall[mcpserver.CopyResult](t, ctx, s, "qag_report_copy", args)
				if copied.Copied || copied.Code != "clipboard_unavailable" || len(copied.Files) != len(exported.Files) {
					t.Fatal("Linux clipboard falsely succeeded")
				}
			}
		}
		t.Log("MCP " + version + ": 45 tools, full demo, all server exports")
	}
	for _, auth := range []string{"", "Bearer wrong"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"qag_status","arguments":{}}}`))
		req.Header.Set("Authorization", auth)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Origin", "https://client.invalid")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Host = "mcp-client.invalid"
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != 200 || r.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatal("MCP still restricts anonymous Host/Origin access")
		}
	}
	return lastRun
}
