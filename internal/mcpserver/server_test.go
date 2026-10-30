package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qag/internal/mcpserver"
)

func TestTransportMethodsAndRequestBounds(t *testing.T) {
	s := mcpserver.New(mcpserver.Options{})
	seen := 0
	s.SDK.AddTool(&mcp.Tool{Name: "large-test", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		seen++
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "received"}}}, nil
	})
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "limit-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "large-test", Arguments: map[string]any{"dataBase64": strings.Repeat("A", 14<<20)}}); err != nil || seen != 1 {
		t.Fatalf("10 MiB image frame did not reach handler: %v %d", err, seen)
	}
	for _, method := range []string{"GET", "DELETE"} {
		req, _ := http.NewRequest(method, host.URL, nil)
		req.Header.Set("Accept", "text/event-stream")
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != 405 {
			t.Fatalf("%s: %d", method, r.StatusCode)
		}
	}
	for _, tc := range []struct {
		body    []byte
		content string
		want    int
	}{{[]byte(`{}`), "text/plain", 415}, {bytes.Repeat([]byte(" "), (32<<20)+1), "application/json", 413}} {
		req, _ := http.NewRequest("POST", host.URL, bytes.NewReader(tc.body))
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", tc.content)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != tc.want || seen != 1 {
			t.Fatalf("bad transport input: %d effects %d", r.StatusCode, seen)
		}
	}
}

func TestBothProtocolsAndStrictPublicToolContracts(t *testing.T) {
	s := mcpserver.New(mcpserver.Options{})
	host := httptest.NewServer(s.Handler())
	defer host.Close()
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "qag-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: host.URL}, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if got := session.InitializeResult().ProtocolVersion; got != version {
				t.Fatalf("protocol %s", got)
			}
			tools, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(tools.Tools) != 43 {
				t.Fatalf("tools: %d", len(tools.Tools))
			}
			if version == "2026-07-28" && (tools.CacheScope != "private" || tools.TTLMs != 0) {
				t.Fatal("public result caching")
			}
			for _, tool := range tools.Tools {
				if tool.InputSchema == nil || tool.OutputSchema == nil || tool.Annotations == nil || tool.Description == "" {
					t.Fatalf("incomplete tool %s", tool.Name)
				}
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "qag_status", Arguments: map[string]any{}})
			if err != nil || result.IsError {
				t.Fatalf("status: %v %+v", err, result)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			if !strings.Contains(string(raw), `"ready"`) {
				t.Fatal("missing status")
			}
			_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "qag_status", Arguments: map[string]any{"unexpected": "sentinel-private-value"}})
			if err == nil || strings.Contains(err.Error(), "sentinel-private-value") {
				t.Fatal("unknown argument accepted or leaked")
			}
			_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "qag_test_case_get", Arguments: map[string]any{"id": "invalid"}})
			if err == nil {
				t.Fatal("invalid ID reached repository")
			}
		})
	}
}
