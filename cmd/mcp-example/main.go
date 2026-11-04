// mcp-example runs the built-in, read-only qag scenario using the public MCP API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/runs"
)

type client struct {
	ctx     context.Context
	session *mcp.ClientSession
}

func (c client) call(name string, args, out any) error {
	r, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err
	}
	var envelope struct {
		Data     json.RawMessage      `json:"data"`
		Artifact *mcpserver.Artifact  `json:"artifact"`
		Error    *mcpserver.ToolError `json:"error"`
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if envelope.Error != nil {
		return fmt.Errorf("%s: %s (requestId %s)", name, envelope.Error.Code, envelope.Error.RequestID)
	}
	if r.IsError {
		return fmt.Errorf("%s failed", name)
	}
	if envelope.Artifact != nil {
		envelope.Data, err = c.read(*envelope.Artifact)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(envelope.Data, out)
}
func (c client) read(a mcpserver.Artifact) ([]byte, error) {
	var out []byte
	for offset := int64(0); ; {
		var part mcpserver.ArtifactChunk
		if err := c.call("qag_artifact_read", map[string]any{"id": a.ID, "offset": offset}, &part); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(part.DataBase64)
		if err != nil {
			return nil, err
		}
		if part.Offset != offset || len(data) != part.Length {
			return nil, fmt.Errorf("invalid artifact chunk")
		}
		out = append(out, data...)
		offset += int64(len(data))
		if part.EOF {
			break
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("empty chunk")
		}
	}
	hash := sha256.Sum256(out)
	if int64(len(out)) != a.Size || hex.EncodeToString(hash[:]) != a.SHA256 {
		return nil, fmt.Errorf("artifact checksum mismatch")
	}
	return out, nil
}
func run() error {
	endpoint := flag.String("url", "http://127.0.0.1:8080/mcp", "qag MCP URL")
	version := flag.String("protocol", "2026-07-28", "MCP protocol: 2026-07-28 or 2025-11-25")
	format := flag.String("format", "pdf", "pdf, text, markdown or rich")
	directory := flag.String("out", "qag-mcp-report", "directory for exported files")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sdk := mcp.NewClient(&mcp.Implementation{Name: "qag-example", Version: "1"}, nil)
	session, err := sdk.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *endpoint}, &mcp.ClientSessionOptions{ProtocolVersion: *version})
	if err != nil {
		return err
	}
	defer session.Close()
	c := client{ctx, session}
	var info builtin.Info
	if err = c.call("qag_demo_get", map[string]any{}, &info); err != nil {
		return err
	}
	var doc catalog.TestCase
	if err = c.call("qag_test_case_get", map[string]any{"id": info.TestCaseID}, &doc); err != nil {
		return err
	}
	var run runs.Run
	if err = c.call("qag_run_start", map[string]any{"testCaseId": doc.ID, "environmentId": info.EnvironmentID, "revision": doc.Revision, "executeAll": true, "idempotencyKey": catalog.NewID()}, &run); err != nil {
		return err
	}
	for {
		if err = c.call("qag_run_get", map[string]any{"id": run.ID}, &run); err != nil {
			return err
		}
		if len(run.Batches) > 0 && run.Batches[0].State != "running" && run.Batches[0].State != "queued" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	// This sample explicitly gives all demo steps a manual passed assessment.
	revisions := []runs.StepRevision{}
	for _, st := range run.StepStates {
		revisions = append(revisions, runs.StepRevision{StepID: st.StepID, Revision: st.Revision})
	}
	if err = c.call("qag_run_steps_pass_all", map[string]any{"runId": run.ID, "steps": revisions}, &run); err != nil {
		return err
	}
	if err = c.call("qag_run_finish", map[string]any{"id": run.ID}, &run); err != nil {
		return err
	}
	var report runs.Report
	if err = c.call("qag_report_prepare", map[string]any{"runId": run.ID, "includeExpected": true}, &report); err != nil {
		return err
	}
	var exported mcpserver.ExportResult
	if err = c.call("qag_report_export", map[string]any{"runId": run.ID, "snapshotId": report.SnapshotID, "hash": report.Hash, "includeExpected": true, "format": *format}, &exported); err != nil {
		return err
	}
	if err = os.MkdirAll(*directory, 0700); err != nil {
		return err
	}
	for _, file := range exported.Files {
		data, e := c.read(file)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(*directory, filepath.Base(file.Name)), data, 0600); e != nil {
			return e
		}
	}
	fmt.Printf("Protocol %s; run %s: %s; %d files in %s\n", session.InitializeResult().ProtocolVersion, run.ID, run.Status, len(exported.Files), *directory)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
