//go:build integration

package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/datatransfer"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func TestMCPEnvironmentHTTPFormAcrossProtocols(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := authoringApp(t)
			session := connectMCP(t, f, version)
			var sourceCalls, httpCalls, grpcCalls atomic.Int32
			token := "environment-private-sentinel"
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceCalls.Add(1)
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Error("wrong source method/type")
				}
				if r.ParseForm() != nil || r.PostForm.Get("client_secret") != "fixture-auth-password" || strings.Join(r.PostForm["scope"], ",") != "one,two" {
					t.Error("source form/secret")
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"access_token": token})
			}))
			defer source.Close()
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				httpCalls.Add(1)
				if r.Header.Get("Authorization") != "Bearer "+token+"!" {
					t.Error("effective authorization")
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"echo": token})
			}))
			defer target.Close()
			definition := catalog.HTTPHeaderSource{Method: "POST", URL: source.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "form", Form: []catalog.KeyValue{{Name: "client_secret", Value: "${CLIENT_SECRET}"}, {Name: "scope", Value: "one"}, {Name: "scope", Value: "two"}}, JSONPointer: "/access_token", FetchMode: "per_run"}
			env := mcpCall[resources.Environment](t, f, session, "qag_variables_set", map[string]any{"id": f.env.ID, "revision": f.env.Revision, "variables": f.env.Variables, "httpVariables": map[string]any{"SHARED": definition, " ": 123}})
			if len(env.HTTPVariables) != 1 || sourceCalls.Load() != 0 {
				t.Fatal("source executed or unnamed row retained")
			}
			env = mcpCall[resources.Environment](t, f, session, "qag_variables_set", map[string]any{"id": env.ID, "revision": env.Revision, "variables": env.Variables})
			if len(env.HTTPVariables) != 1 {
				t.Fatal("old MCP client cleared definitions")
			}
			f.api(t, "PUT", "/environments/"+env.ID+"/variables", map[string]any{"revision": env.Revision, "variables": env.Variables, "httpVariables": nil}, 400)
			_, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "qag_variables_set", Arguments: map[string]any{"id": env.ID, "revision": env.Revision, "variables": env.Variables, "httpVariables": nil}})
			if err == nil {
				t.Fatal("null MCP write accepted")
			}
			makeResource := func(name, kind string, cfg resources.Config, secrets map[string]string) resources.Resource {
				return mcpCall[resources.Resource](t, f, session, "qag_resource_create", mcpserver.ResourceCreate{EnvironmentID: env.ID, ResourceData: mcpserver.ResourceData{Name: name, Kind: kind, Config: cfg, Secrets: mcpserver.SecretChanges{Replace: secrets}}})
			}
			httpResource := makeResource("HTTP_TARGET", "http", resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${SHARED}!"}}}, nil)
			ownSource := definition
			ownSource.Suffix = "]"
			httpResource.Config.Variables = map[string]resources.VariableDefinition{"OWN": {Mode: "http", Source: &ownSource}}
			httpResource = mcpCall[resources.Resource](t, f, session, "qag_resource_update", map[string]any{"id": httpResource.ID, "revision": httpResource.Revision, "name": httpResource.Name, "kind": httpResource.Kind, "config": httpResource.Config})
			read := mcpCall[resources.Resource](t, f, session, "qag_resource_get", map[string]any{"id": httpResource.ID})
			if read.Config.Variables["OWN"].Source.Suffix != "]" || len(read.Config.Variables["OWN"].Source.Form) != 3 || len(read.Config.Headers) != 1 || sourceCalls.Load() != 0 {
				t.Fatal("full MCP resource save lost settings or fetched a source")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			grpcServer := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
				if info.FullMethod == "/grpc.health.v1.Health/Check" {
					grpcCalls.Add(1)
					values, _ := metadata.FromIncomingContext(ctx)
					if strings.Join(values.Get("x-token"), "") != token {
						t.Error("gRPC environment value lost")
					}
					if req.(*grpc_health_v1.HealthCheckRequest).Service == "echo" {
						return nil, status.Error(codes.InvalidArgument, token)
					}
				}
				return next(ctx, req)
			}))
			grpc_health_v1.RegisterHealthServer(grpcServer, health.NewServer())
			reflection.Register(grpcServer)
			go grpcServer.Serve(listener)
			defer grpcServer.Stop()
			makeResource("GRPC_TARGET", "grpc", resources.Config{Address: listener.Addr().String(), TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "x-token", Value: "${SHARED}"}}}, nil)
			role, cert, err := f.db.EnableMTLS(f.ctx, os.Getenv("QAG_TEST_PKI_DIR"))
			if err != nil {
				t.Fatal(err)
			}
			pg, err := pgx.ParseConfig(f.db.TargetURL)
			if err != nil {
				t.Fatal(err)
			}
			makeResource("SQL_TARGET", "postgresql", resources.Config{Address: net.JoinHostPort(pg.Host, strconv.Itoa(int(pg.Port))), Database: pg.Database, Username: role, TLS: catalog.TLSConfig{Mode: "mtls", CA: cert.CA, Certificate: cert.Client}}, map[string]string{"PRIVATE_KEY": cert.Key})
			c := f.caseInFolder(t)
			code := 200
			blocks := []catalog.Block{
				{ID: catalog.NewID(), Kind: "http", Resource: httpResource.Name, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}},
				{ID: catalog.NewID(), Kind: "grpc", Resource: "GRPC_TARGET", GRPC: &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{"service":""}`}, Expected: catalog.Expectation{GRPCCode: "OK", BodyMode: "none"}},
				{ID: catalog.NewID(), Kind: "postgresql", Resource: "SQL_TARGET", SQL: &catalog.SQLRequest{Mode: "rows", Query: "SELECT length($1::text)::numeric AS n", Parameters: []catalog.TypedValue{{Type: "string", Value: "${SHARED}"}}}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "n", Type: "numeric"}}, Rows: [][]catalog.TypedValue{{{Type: "decimal", Value: strconv.Itoa(len(token))}}}}},
			}
			steps := []catalog.Step{{ID: catalog.NewID(), Description: "Общий источник", Blocks: blocks}}
			c = mcpCall[catalog.TestCase](t, f, session, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "steps": steps})
			run := mcpCall[runs.Run](t, f, session, "qag_run_start", mcpserver.RunStart{TestCaseID: c.ID, EnvironmentID: env.ID, Revision: c.Revision, ExecuteAll: true, HistoryPolicy: "unretained", IdempotencyKey: catalog.NewID()})
			run = waitBatch(t, f, run.ID)
			if len(run.Attempts) != 3 || sourceCalls.Load() != 1 || httpCalls.Load() != 1 || grpcCalls.Load() != 1 {
				t.Fatal("cross-protocol cache", sourceCalls.Load(), httpCalls.Load(), grpcCalls.Load(), len(run.Attempts))
			}
			for _, attempt := range run.Attempts {
				if attempt.State != "completed" || attempt.Result == nil || len(attempt.Result.VariableSources) != 1 || attempt.Result.VariableSources[0].Scope != "environment" {
					t.Fatal("protocol failed", attempt.State, attempt.Result)
				}
			}
			data, _ := json.Marshal(run)
			if strings.Contains(string(data), token) || strings.Contains(f.audit.String(), token) {
				t.Fatal("dynamic value leaked")
			}
			args := map[string]any{"runId": run.ID, "stepId": steps[0].ID, "idempotencyKey": catalog.NewID()}
			first := mcpCall[runs.Batch](t, f, session, "qag_run_step_execute", args)
			_ = waitBatch(t, f, run.ID)
			again := mcpCall[runs.Batch](t, f, session, "qag_run_step_execute", args)
			if first.ID != again.ID || sourceCalls.Load() != 1 {
				t.Fatal("step replay refetched source")
			}
			for _, index := range []int{1, 2} {
				variant := mcpCall[runs.Variant](t, f, session, "qag_variant_create", mcpserver.VariantCreate{RunID: run.ID, BlockID: blocks[index].ID, Name: "Проверка маскирования"})
				request := mcpserver.ResearchRequest{}
				if index == 1 {
					request.GRPC = &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{"service":"echo"}`}
				} else {
					request.SQL = &catalog.SQLRequest{Mode: "rows", Query: "SELECT $1::text AS token", Parameters: []catalog.TypedValue{{Type: "string", Value: "${SHARED}"}}}
				}
				variant = mcpCall[runs.Variant](t, f, session, "qag_variant_update", mcpserver.VariantUpdate{RunID: run.ID, VariantID: variant.ID, Revision: variant.Revision, Name: variant.Name, Request: request})
				attempt := mcpCall[execution.Attempt](t, f, session, "qag_variant_attempt_start", mcpserver.VariantAttemptStart{RunID: run.ID, VariantID: variant.ID, Revision: variant.Revision, IdempotencyKey: catalog.NewID()})
				attempt = awaitAuthoringAttempt(t, f, attempt)
				if attempt.Result == nil || !attempt.Result.Redacted {
					t.Fatal("unmasked reflected protocol response")
				}
				if index == 2 && (len(attempt.Result.Rows) != 1 || attempt.Result.Rows[0][0].Value != "[скрыто]") {
					t.Fatal("SQL token not masked")
				}
			}
			report := mcpCall[runs.Report](t, f, session, "qag_report_prepare", mcpserver.ReportPrepare{RunID: run.ID})
			encodedReport, _ := json.Marshal(report)
			if strings.Contains(string(encodedReport), token) {
				t.Fatal("report leaked dynamic value")
			}
			if f.fileStore != nil {
				doc, err := f.fileStore.Snapshot(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(doc)
				if strings.Contains(string(encoded), token) {
					t.Fatal("YAML persisted dynamic value")
				}
			} else {
				vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
				doc, err := (&datatransfer.PostgreSQL{Store: f.store, Vault: vault}).Snapshot(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(doc)
				if strings.Contains(string(encoded), token) {
					t.Fatal("PostgreSQL/export persisted dynamic value")
				}
			}
			mcpCall[runs.Run](t, f, session, "qag_run_cancel", map[string]any{"id": run.ID})
			if sourceCalls.Load() != 1 {
				t.Fatal("reading/cancel fetched source")
			}
			raw := f.api(t, "PUT", "/environments/"+env.ID+"/variables", map[string]any{"revision": env.Revision, "variables": env.Variables, "httpVariables": map[string]any{}}, 200)
			var cleared resources.Environment
			if json.Unmarshal(raw, &cleared) != nil || len(cleared.HTTPVariables) != 0 {
				t.Fatal("explicit API clear")
			}
			env = cleared
			var unknown map[string]any
			encoded, _ := json.Marshal(definition)
			_ = json.Unmarshal(encoded, &unknown)
			unknown["unsupportedField"] = "must reject"
			bad := map[string]any{"id": env.ID, "revision": env.Revision, "variables": env.Variables, "httpVariables": map[string]any{"SHARED": unknown}}
			f.api(t, "PUT", "/environments/"+env.ID+"/variables", map[string]any{"revision": env.Revision, "variables": env.Variables, "httpVariables": map[string]any{"SHARED": unknown}}, 400)
			if _, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "qag_variables_set", Arguments: bad}); err == nil {
				t.Fatal("unknown nested MCP field accepted")
			}
		})
	}
}
