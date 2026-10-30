//go:build integration

package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qag/internal/builtin"
	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/demo"
	"qag/internal/execution"
	"qag/internal/mcpserver"
	"qag/internal/reporting"
	"qag/internal/resources"
	"qag/internal/runs"
	"qag/internal/testfixture"
)

type loseResponseTransport struct{ dropped atomic.Bool }

func (d *loseResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	var err error
	if r.Body != nil {
		body, err = io.ReadAll(r.Body)
		r.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	r = r.Clone(r.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	response, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(body, []byte("qag_run_attempt_start")) && d.dropped.CompareAndSwap(false, true) {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, errors.New("simulated lost tool response")
	}
	return response, nil
}

func TestMCPNetworkResourcesProtoAndSQL(t *testing.T) {
	f := authoringApp(t)
	s := connectMCP(t, f, "2026-07-28")
	cert, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	targets, err := testfixture.Start(&cert, false)
	if err != nil {
		t.Fatal(err)
	}
	defer targets.Close()
	makeResource := func(name, kind string, cfg resources.Config, secrets map[string]string) resources.Resource {
		return mcpCall[resources.Resource](t, f, s, "qag_resource_create", mcpserver.ResourceCreate{EnvironmentID: f.env.ID, ResourceData: mcpserver.ResourceData{Name: name, Kind: kind, Config: cfg, Secrets: mcpserver.SecretChanges{Replace: secrets}}})
	}
	tlsConfig := catalog.TLSConfig{Mode: "mtls", CA: cert.CA, Certificate: cert.Client}
	httpRes := makeResource("TLS_HTTP", "http", resources.Config{BaseURL: targets.HTTP.URL, TLS: tlsConfig}, map[string]string{"PRIVATE_KEY": cert.Key})
	grpcSource := catalog.RequestHeader{ID: catalog.NewID(), Name: "authorization", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: targets.HTTP.URL + "/token", TLS: tlsConfig, PrivateKeySecret: "PRIVATE_KEY", JSONPointer: "/tokens/0/value", BodyMode: "none", FetchMode: "per_attempt", Prefix: "Bearer "}}
	grpcRes := makeResource("TLS_GRPC", "grpc", resources.Config{Address: targets.GRPC, TLS: tlsConfig, Headers: []catalog.RequestHeader{grpcSource}}, map[string]string{"PRIVATE_KEY": cert.Key})
	grpcRes = mcpCall[resources.Resource](t, f, s, "qag_proto_bundle_upload", map[string]any{"id": grpcRes.ID, "revision": grpcRes.Revision, "files": map[string]string{"health.proto": testfixture.HealthProto, "types.proto": testfixture.HealthTypesProto}})
	mcpError(t, f, s, "qag_proto_bundle_upload", map[string]any{"id": grpcRes.ID, "revision": grpcRes.Revision, "files": map[string]string{"../private.proto": "sentinel-private-value"}}, "invalid")
	role, sqlCert, err := f.db.EnableMTLS(f.ctx, os.Getenv("QAG_TEST_PKI_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	pg, err := pgx.ParseConfig(f.db.TargetURL)
	if err != nil {
		t.Fatal(err)
	}
	makeResource("TLS_SQL", "postgresql", resources.Config{Address: net.JoinHostPort(pg.Host, strconv.Itoa(int(pg.Port))), Database: pg.Database, Username: role, TLS: catalog.TLSConfig{Mode: "mtls", CA: sqlCert.CA, Certificate: sqlCert.Client}}, map[string]string{"PRIVATE_KEY": sqlCert.Key})
	c := f.caseInFolder(t)
	code := 200
	blocks := []catalog.Block{
		{ID: catalog.NewID(), Kind: "http", Resource: "TLS_HTTP", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/api/task", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}},
		{ID: catalog.NewID(), Kind: "grpc", Resource: "TLS_GRPC", GRPC: &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{}`}, Expected: catalog.Expectation{GRPCCode: "OK", BodyMode: "json", Body: `{"status":"SERVING"}`}},
		{ID: catalog.NewID(), Kind: "postgresql", Resource: "TLS_SQL", SQL: &catalog.SQLRequest{Query: "SELECT 9007199254740993123.125::numeric AS exact, NULL::text AS missing, ''::text AS empty", Mode: "rows"}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "exact", Type: "numeric"}, {Name: "missing", Type: "text"}, {Name: "empty", Type: "text"}}, Rows: [][]catalog.TypedValue{{{Type: "decimal", Value: "9007199254740993123.125"}, {Type: "null"}, {Type: "string"}}}}},
	}
	c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": []catalog.Step{{ID: catalog.NewID(), Description: "Все протоколы", Blocks: blocks}}})
	run := mcpCall[runs.Run](t, f, s, "qag_run_start", map[string]any{"testCaseId": c.ID, "environmentId": f.env.ID, "revision": c.Revision, "executeAll": true, "idempotencyKey": catalog.NewID()})
	run = waitBatch(t, f, run.ID)
	if len(run.Attempts) != 3 {
		t.Fatal("missing protocol attempts")
	}
	for _, a := range run.Attempts {
		if a.State != "completed" {
			t.Fatalf("%s: %s %+v", a.Resource.Kind, a.State, a.Result)
		}
		if strings.Contains(a.Result.Body, cert.Key) || strings.Contains(a.Result.Body, sqlCert.Key) {
			t.Fatal("private key leaked")
		}
	}
	if run.Attempts[1].Result.GRPCCode != "OK" || len(run.Attempts[1].Result.Trailers) == 0 || run.Attempts[2].Result.Rows[0][0].Value != "9007199254740993123.125" {
		t.Fatal("protocol detail/precision lost")
	}
	if targets.Sources.Load() != 1 || len(run.Attempts[1].Result.HeaderSources) != 1 {
		t.Fatal("gRPC source/mTLS missing")
	}
	// The same SQL entry points support password resources and request-only exec.
	pw := pg.Password
	makeResource("SQL_PASSWORD", "postgresql", resources.Config{Address: net.JoinHostPort(pg.Host, strconv.Itoa(int(pg.Port))), Database: pg.Database, Username: pg.User, TLS: catalog.TLSConfig{Mode: "plaintext"}}, map[string]string{"PASSWORD": pw})
	c.Steps[0].Blocks[2].Resource = "SQL_PASSWORD"
	c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": c.Steps})
	passwordAttempt := mcpCall[execution.Attempt](t, f, s, "qag_editor_attempt_start", map[string]any{"testCaseId": c.ID, "blockId": blocks[2].ID, "environmentId": f.env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
	if mcpAwait(t, f, s, passwordAttempt.ID).State != "completed" {
		t.Fatal("PostgreSQL password execution failed")
	}
	sqlVariant := mcpCall[runs.Variant](t, f, s, "qag_variant_create", map[string]any{"runId": run.ID, "blockId": blocks[2].ID})
	sqlVariant = mcpCall[runs.Variant](t, f, s, "qag_variant_update", map[string]any{"runId": run.ID, "variantId": sqlVariant.ID, "revision": sqlVariant.Revision, "request": map[string]any{"sql": catalog.SQLRequest{Query: "SELECT 42", Mode: "exec"}}})
	sqlAttempt := mcpCall[execution.Attempt](t, f, s, "qag_variant_attempt_start", map[string]any{"runId": run.ID, "variantId": sqlVariant.ID, "revision": sqlVariant.Revision, "idempotencyKey": catalog.NewID()})
	sqlAttempt = mcpAwait(t, f, s, sqlAttempt.ID)
	if sqlAttempt.State != "completed" || sqlAttempt.Result.RowsAffected == nil || sqlAttempt.Result.Comparison.Status != "not_compared" {
		t.Fatal("SQL research exec lost")
	}
	v := mcpCall[runs.Variant](t, f, s, "qag_variant_create", map[string]any{"runId": run.ID, "blockId": blocks[1].ID})
	v = mcpCall[runs.Variant](t, f, s, "qag_variant_update", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "request": map[string]any{"grpc": catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{"service":"missing"}`}}})
	a := mcpCall[execution.Attempt](t, f, s, "qag_variant_attempt_start", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "idempotencyKey": catalog.NewID()})
	a = mcpAwait(t, f, s, a.ID)
	if a.State != "completed" || a.Result.GRPCCode != "NOT_FOUND" || a.Result.Comparison.Status != "not_compared" {
		t.Fatal("negative grpc result lost")
	}
	// Descriptor validation rejects streaming before an application call.
	v = mcpCall[runs.Variant](t, f, s, "qag_variant_update", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "request": map[string]any{"grpc": catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Watch", Body: `{}`}}})
	before := targets.Calls.Load()
	a = mcpCall[execution.Attempt](t, f, s, "qag_variant_attempt_start", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "idempotencyKey": catalog.NewID()})
	a = mcpAwait(t, f, s, a.ID)
	if a.State != "error" || a.Result.Error == "" {
		t.Fatal("stream accepted")
	}
	if targets.Calls.Load() != before {
		t.Fatal("stream reached target")
	}
	// Wrong CA is an execution error and never becomes an apparent success.
	other, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	httpRes.Config.TLS.CA = other.CA
	mcpCall[resources.Resource](t, f, s, "qag_resource_update", mcpserver.ResourceUpdate{IDInput: mcpserver.IDInput{ID: httpRes.ID}, Revision: httpRes.Revision, ResourceData: mcpserver.ResourceData{Name: httpRes.Name, Kind: httpRes.Kind, Config: httpRes.Config}})
	a = mcpCall[execution.Attempt](t, f, s, "qag_editor_attempt_start", map[string]any{"testCaseId": c.ID, "blockId": blocks[0].ID, "environmentId": f.env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
	a = mcpAwait(t, f, s, a.ID)
	if a.State == "completed" || targets.Calls.Load() != before {
		t.Fatal("invalid TLS reached application")
	}
}

func TestMCPJobOwnershipCancellationAndStorageFailure(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := authoringApp(t)
			s := connectMCP(t, f, version)
			other := connectMCP(t, f, version)
			var calls atomic.Int32
			entered := make(chan struct{}, 4)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				entered <- struct{}{}
				<-r.Context().Done()
			}))
			defer target.Close()
			c := workflowCase(t, f, target.URL, []catalog.Block{{HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/slow", BodyMode: "none"}}})
			run := mcpCall[runs.Run](t, f, s, "qag_run_start", map[string]any{"testCaseId": c.ID, "environmentId": f.env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
			key := catalog.NewID()
			args := map[string]any{"runId": run.ID, "blockId": c.Steps[0].Blocks[0].ID, "idempotencyKey": key}
			a := mcpCall[execution.Attempt](t, f, s, "qag_run_attempt_start", args)
			<-entered
			s.Close()
			retry := mcpCall[execution.Attempt](t, f, other, "qag_run_attempt_start", args)
			if retry.ID != a.ID || retry.State != "running" || calls.Load() != 1 {
				t.Fatal("disconnect cancelled/repeated owned job")
			}
			request, _ := http.NewRequest("POST", f.server.URL+"/api/v1/runs/"+run.ID+"/blocks/"+c.Steps[0].Blocks[0].ID+"/attempts", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", f.server.URL)
			request.Header.Set("X-QAG-CSRF", f.csrf)
			request.Header.Set("Idempotency-Key", key)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			var uiAttempt execution.Attempt
			json.NewDecoder(response.Body).Decode(&uiAttempt)
			response.Body.Close()
			if response.StatusCode != 200 || uiAttempt.ID != a.ID || calls.Load() != 1 {
				t.Fatal("UI/MCP repeated same command")
			}
			cancelledCtx, stopWaiting := context.WithCancel(f.ctx)
			stopWaiting()
			if _, err := mcpResult(t, cancelledCtx, other, "qag_run_attempt_start", map[string]any{"runId": run.ID, "blockId": c.Steps[0].Blocks[0].ID, "idempotencyKey": catalog.NewID()}); err == nil {
				t.Fatal("pre-registration cancellation ignored")
			}
			mcpError(t, f, other, "qag_run_finish", map[string]any{"id": run.ID}, "conflict")
			mcpCall[execution.Attempt](t, f, other, "qag_attempt_cancel", map[string]any{"id": a.ID})
			if mcpAwait(t, f, other, a.ID).State != "cancelled" {
				t.Fatal("explicit cancel ignored")
			}
			args["idempotencyKey"] = catalog.NewID()
			a = mcpCall[execution.Attempt](t, f, other, "qag_run_attempt_start", args)
			<-entered
			mcpCall[runs.Run](t, f, other, "qag_run_cancel", map[string]any{"id": run.ID})
			if mcpAwait(t, f, other, a.ID).State != "cancelled" {
				t.Fatal("run did not cancel job")
			}
			// Refused writes return a safe error, with no row committed or target call.
			_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_mcp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture-auth-password'; END $$; CREATE TRIGGER reject_mcp BEFORE INSERT ON qag.folders FOR EACH ROW EXECUTE FUNCTION qag.reject_mcp()`), pgx.QueryExecModeSimpleProtocol)
			if err != nil {
				t.Fatal(err)
			}
			mcpError(t, f, other, "qag_folder_create", map[string]any{"name": "Refused"}, "storage_unavailable")
			var count int
			err = f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT count(*) FROM qag.folders WHERE name='Refused'`)).Scan(&count)
			if err != nil || count != 0 || calls.Load() != 2 {
				t.Fatal("failed write had effect")
			}
			if bytes.Contains(f.audit.Bytes(), []byte("fixture-auth-password")) || !bytes.Contains(f.audit.Bytes(), []byte("storage_unavailable")) {
				t.Fatal("audit leaks arguments or misses safe code")
			}
			// Lose an acknowledgement after registration, then retry from another
			// client. The persisted key owns one effect independently of RPC IDs.
			lost := &loseResponseTransport{}
			lostClient := mcp.NewClient(&mcp.Implementation{Name: "lost-response", Version: "1"}, nil)
			lostSession, err := lostClient.Connect(f.ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: lost}}, &mcp.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer lostSession.Close()
			newRun := mcpCall[runs.Run](t, f, other, "qag_run_start", map[string]any{"testCaseId": c.ID, "environmentId": f.env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
			args = map[string]any{"runId": newRun.ID, "blockId": c.Steps[0].Blocks[0].ID, "idempotencyKey": catalog.NewID()}
			_, _ = mcpResult(t, f.ctx, lostSession, "qag_run_attempt_start", args)
			<-entered
			a = mcpCall[execution.Attempt](t, f, other, "qag_run_attempt_start", args)
			if !lost.dropped.Load() || calls.Load() != 3 || a.State != "running" {
				t.Fatal("lost response repeated effect")
			}
			mcpCall[runs.Run](t, f, other, "qag_run_cancel", map[string]any{"id": newRun.ID})
		})
	}
}
func connectMCP(t *testing.T, f *authoringFixture, version string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "qag-integration", Version: "1"}, nil)
	s, err := c.Connect(f.ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp"}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type mcpEnvelope struct {
	Data     json.RawMessage      `json:"data"`
	Artifact *mcpserver.Artifact  `json:"artifact"`
	Error    *mcpserver.ToolError `json:"error"`
}

func mcpResult(t *testing.T, ctx context.Context, s *mcp.ClientSession, name string, in any) (mcpEnvelope, error) {
	t.Helper()
	r, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: in})
	if err != nil {
		return mcpEnvelope{}, err
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("fixture-auth-password")) || bytes.Contains(raw, []byte("derived-secret-sentinel")) {
		t.Fatal(name + " leaked secret")
	}
	var out mcpEnvelope
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}
func mcpCall[T any](t *testing.T, f *authoringFixture, s *mcp.ClientSession, name string, in any) T {
	t.Helper()
	out, err := mcpResult(t, f.ctx, s, name, in)
	if err != nil || out.Error != nil {
		t.Fatalf("%s: %v %+v", name, err, out.Error)
	}
	if out.Artifact != nil {
		out.Data = artifactBytes(t, f, s, *out.Artifact)
	}
	var value T
	if err = json.Unmarshal(out.Data, &value); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}
func mcpError(t *testing.T, f *authoringFixture, s *mcp.ClientSession, name string, in any, code string) {
	t.Helper()
	out, err := mcpResult(t, f.ctx, s, name, in)
	if err != nil || out.Error == nil || out.Error.Code != code || out.Error.RequestID == "" || len(out.Data) != 0 {
		t.Fatalf("%s expected %s: %v %+v", name, code, err, out.Error)
	}
}
func artifactBytes(t *testing.T, f *authoringFixture, s *mcp.ClientSession, a mcpserver.Artifact) []byte {
	t.Helper()
	var data []byte
	for offset := int64(0); ; {
		chunk := mcpCall[mcpserver.ArtifactChunk](t, f, s, "qag_artifact_read", map[string]any{"id": a.ID, "offset": offset})
		b, err := base64.StdEncoding.DecodeString(chunk.DataBase64)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, b...)
		offset += int64(len(b))
		if chunk.EOF {
			break
		}
		if len(b) == 0 {
			t.Fatal("empty chunk")
		}
	}
	h := sha256.Sum256(data)
	if int64(len(data)) != a.Size || hex.EncodeToString(h[:]) != a.SHA256 {
		t.Fatal("artifact integrity")
	}
	return data
}
func mcpAwait(t *testing.T, f *authoringFixture, s *mcp.ClientSession, id string) execution.Attempt {
	t.Helper()
	for i := 0; i < 300; i++ {
		a := mcpCall[execution.Attempt](t, f, s, "qag_attempt_get", map[string]any{"id": id})
		if a.State != "queued" && a.State != "running" {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("attempt stuck")
	return execution.Attempt{}
}
func mcpGetRun(t *testing.T, f *authoringFixture, s *mcp.ClientSession, id string) runs.Run {
	return mcpCall[runs.Run](t, f, s, "qag_run_get", map[string]any{"id": id})
}

func TestMCPUIParityAndLifecycle(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := authoringApp(t)
			s := connectMCP(t, f, version)
			other := connectMCP(t, f, version)
			status := mcpCall[mcpserver.Status](t, f, s, "qag_status", map[string]any{})
			if status.Status != "ready" {
				t.Fatal(status.Status)
			}
			folder := mcpCall[catalog.Folder](t, f, s, "qag_folder_create", map[string]any{"name": "MCP папка"})
			folder = mcpCall[catalog.Folder](t, f, s, "qag_folder_update", map[string]any{"id": folder.ID, "name": "MCP новая папка", "archived": true})
			archived := mcpCall[catalog.Page[catalog.Folder]](t, f, s, "qag_folders_list", map[string]any{"archived": true, "limit": 1})
			if len(archived.Items) != 1 || archived.Items[0].ID != folder.ID {
				t.Fatal("archive missing")
			}
			mcpCall[catalog.Folder](t, f, s, "qag_folder_update", map[string]any{"id": folder.ID, "archived": false})
			c := mcpCall[catalog.TestCase](t, f, s, "qag_test_case_create", map[string]any{"folderId": folder.ID})
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision})
			if c.Name == "" {
				t.Fatal("empty draft name")
			}
			draft := []catalog.Step{{ID: catalog.NewID(), Description: "", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "grpc", GRPC: &catalog.GRPCRequest{Body: `{"unfinished":`}}}}}
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "steps": draft})
			if len(c.ValidationIssues) == 0 || c.Steps[0].Blocks[0].GRPC.Body != `{"unfinished":` {
				t.Fatal("partial draft lost")
			}
			var ui catalog.TestCase
			if json.Unmarshal(f.api(t, "GET", "/test-cases/"+c.ID, nil, 200), &ui) != nil || ui.Revision != c.Revision {
				t.Fatal("UI cannot read MCP draft")
			}
			mcpError(t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision - 1}, "conflict")
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_update", map[string]any{"id": c.ID, "name": "Собственный сценарий", "archived": true})
			mcpCall[catalog.TestCase](t, f, s, "qag_test_case_update", map[string]any{"id": c.ID, "archived": false})
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_get", map[string]any{"id": c.ID})
			listed := mcpCall[catalog.Page[catalog.TestCase]](t, f, s, "qag_test_cases_list", map[string]any{"folderId": folder.ID})
			if len(listed.Items) != 1 {
				t.Fatal("case missing")
			}
			env := mcpCall[resources.Environment](t, f, s, "qag_environment_create", map[string]any{"name": "MCP окружение", "description": "metadata"})
			env = mcpCall[resources.Environment](t, f, s, "qag_environment_update", map[string]any{"id": env.ID, "revision": env.Revision, "name": env.Name, "description": "изменено"})
			env = mcpCall[resources.Environment](t, f, s, "qag_variables_set", map[string]any{"id": env.ID, "revision": env.Revision, "variables": map[string]string{"TASK": "42"}, "secrets": map[string]any{"replace": map[string]string{"TOKEN": "fixture-auth-password", "DELETE": "private-delete"}}})
			env = mcpCall[resources.Environment](t, f, s, "qag_variables_set", map[string]any{"id": env.ID, "revision": env.Revision, "variables": map[string]string{"TASK": "43"}, "secrets": map[string]any{"clear": []string{"DELETE"}}})
			if len(env.SecretNames) != 1 || env.Description != "изменено" {
				t.Fatal("secret retain/clear or metadata")
			}
			mcpCall[resources.Environment](t, f, s, "qag_environment_get", map[string]any{"id": env.ID})
			mcpCall[catalog.Page[resources.Environment]](t, f, s, "qag_environments_list", map[string]any{"limit": 1})
			var calls, sources atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sources.Add(1)
				if r.Method != "POST" || r.URL.Query().Get("task") != "43" || r.Header.Get("X-Input") != "yes" {
					t.Error("source form changed")
				}
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["secret"] != "fixture-auth-password" {
					t.Error("source secret")
				}
				w.Write([]byte(`{"tokens":[{"value":"derived-secret-sentinel"}]}`))
			}))
			defer source.Close()
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/bad" {
					w.WriteHeader(500)
				}
				if r.URL.Path == "/slow" {
					<-r.Context().Done()
					return
				}
				json.NewEncoder(w).Encode(map[string]string{"token": r.Header.Get("Authorization"), "number": "9007199254740993123.125"})
			}))
			defer target.Close()
			h := catalog.RequestHeader{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "POST", URL: source.URL, Query: []catalog.KeyValue{{Name: "task", Value: "${TASK}"}}, Headers: []catalog.KeyValue{{Name: "X-Input", Value: "yes"}}, BodyMode: "json", Body: `{"secret":"${TOKEN}"}`, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/tokens/0/value", Prefix: "Bearer ", FetchMode: "per_run"}}
			res := mcpCall[resources.Resource](t, f, s, "qag_resource_create", mcpserver.ResourceCreate{EnvironmentID: env.ID, ResourceData: mcpserver.ResourceData{Name: "GW", Kind: "http", Config: resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{h}}}})
			res = mcpCall[resources.Resource](t, f, s, "qag_resource_update", mcpserver.ResourceUpdate{IDInput: mcpserver.IDInput{ID: res.ID}, Revision: res.Revision, ResourceData: mcpserver.ResourceData{Name: res.Name, Kind: res.Kind, Config: res.Config}})
			mcpCall[resources.Resource](t, f, s, "qag_resource_get", map[string]any{"id": res.ID})
			mcpCall[catalog.Page[resources.Resource]](t, f, s, "qag_resources_list", map[string]any{"environmentId": env.ID})
			var imageData bytes.Buffer
			png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 8, 6)))
			im := mcpCall[catalog.ExpectedImage](t, f, s, "qag_image_upload", map[string]any{"testCaseId": c.ID, "filename": "reference.png", "dataBase64": base64.StdEncoding.EncodeToString(imageData.Bytes())})
			imageResult := mcpCall[mcpserver.ImageResult](t, f, s, "qag_image_get", map[string]any{"testCaseId": c.ID, "imageId": im.ID})
			if !bytes.Equal(artifactBytes(t, f, s, imageResult.Artifact), imageData.Bytes()) {
				t.Fatal("image changed")
			}
			resourceList, err := s.ListResources(f.ctx, nil)
			if err != nil || len(resourceList.Resources) == 0 {
				t.Fatal("issued resources missing", err)
			}
			contents, err := s.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: imageResult.Artifact.URI})
			if err != nil || !bytes.Equal(contents.Contents[0].Blob, imageData.Bytes()) {
				t.Fatal("resource read", err)
			}
			if _, err = s.ReadResource(f.ctx, &mcp.ReadResourceParams{URI: "file:///private"}); err == nil {
				t.Fatal("arbitrary resource")
			}
			code := 200
			block := func(path string) catalog.Block {
				return catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: path, BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
			}
			steps := []catalog.Step{{ID: catalog.NewID(), Description: "Автоматические вызовы", Blocks: []catalog.Block{block("/ok"), block("/bad")}, Expected: &catalog.StepExpectation{Text: "EXPECTED-SENTINEL", Images: []catalog.ImageRef{{ImageID: im.ID, Caption: "Эталон"}}}}, {ID: catalog.NewID(), Description: "Ручная проверка", Blocks: []catalog.Block{}}}
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": steps})
			second := steps[0]
			second.Blocks = []catalog.Block{steps[0].Blocks[1], steps[0].Blocks[0]}
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": []catalog.Step{steps[1], second}})
			if c.Steps[0].ID != steps[1].ID || c.Steps[1].Blocks[0].ID != steps[0].Blocks[1].ID {
				t.Fatal("author order lost")
			}
			c = mcpCall[catalog.TestCase](t, f, s, "qag_test_case_save", map[string]any{"id": c.ID, "revision": c.Revision, "name": c.Name, "steps": steps})
			trial := mcpCall[execution.Attempt](t, f, s, "qag_editor_attempt_start", map[string]any{"testCaseId": c.ID, "blockId": steps[0].Blocks[0].ID, "environmentId": env.ID, "revision": c.Revision, "idempotencyKey": catalog.NewID()})
			trial = mcpAwait(t, f, s, trial.ID)
			if trial.RunID != "" || trial.State != "completed" {
				t.Fatal("trial run created/failed")
			}
			key := catalog.NewID()
			args := map[string]any{"testCaseId": c.ID, "environmentId": env.ID, "revision": c.Revision, "idempotencyKey": key}
			run := mcpCall[runs.Run](t, f, s, "qag_run_start", args)
			repeat := mcpCall[runs.Run](t, f, other, "qag_run_start", args)
			if run.ID != repeat.ID || calls.Load() != 1 {
				t.Fatal("run idempotency/network")
			}
			args["executeAll"] = true
			mcpError(t, f, s, "qag_run_start", args, "conflict")
			delete(args, "executeAll")
			batch := mcpCall[runs.Batch](t, f, s, "qag_batch_start", map[string]any{"runId": run.ID, "idempotencyKey": catalog.NewID()})
			if len(batch.Items) != 2 {
				t.Fatal("manual step queued")
			}
			run = waitBatch(t, f, run.ID)
			if calls.Load() != 3 || sources.Load() != 2 || run.StepStates[0].Status != "not_run" {
				t.Fatal("batch ordering/source scope/manual rating", calls.Load(), sources.Load())
			}
			trial = mcpCall[execution.Attempt](t, f, s, "qag_run_attempt_start", map[string]any{"runId": run.ID, "blockId": steps[0].Blocks[0].ID, "idempotencyKey": catalog.NewID()})
			mcpAwait(t, f, s, trial.ID)
			v := mcpCall[runs.Variant](t, f, s, "qag_variant_create", map[string]any{"runId": run.ID, "blockId": steps[0].Blocks[0].ID, "name": "Исследование"})
			v = mcpCall[runs.Variant](t, f, s, "qag_variant_update", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "request": map[string]any{"http": catalog.HTTPRequest{Method: "GET", Path: "/bad", BodyMode: "none"}}})
			_, err = mcpResult(t, f.ctx, s, "qag_variant_update", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "request": map[string]any{"expected": map[string]any{}}})
			if err == nil {
				t.Fatal("variant expected accepted")
			}
			va := mcpCall[execution.Attempt](t, f, s, "qag_variant_attempt_start", map[string]any{"runId": run.ID, "variantId": v.ID, "revision": v.Revision, "idempotencyKey": catalog.NewID()})
			va = mcpAwait(t, f, s, va.ID)
			if va.Result.Comparison.Status != "not_compared" {
				t.Fatal("variant compared")
			}
			run = mcpGetRun(t, f, s, run.ID)
			run = mcpCall[runs.Run](t, f, s, "qag_run_step_update", map[string]any{"runId": run.ID, "stepId": steps[0].ID, "revision": run.StepStates[0].Revision, "status": "failed", "comment": "Комментарий"})
			revisions := []runs.StepRevision{}
			for _, st := range run.StepStates {
				revisions = append(revisions, runs.StepRevision{StepID: st.StepID, Revision: st.Revision})
			}
			run = mcpCall[runs.Run](t, f, s, "qag_run_steps_pass_all", map[string]any{"runId": run.ID, "steps": revisions})
			if run.StepStates[0].Comment != "Комментарий" {
				t.Fatal("comment lost")
			}
			mcpError(t, f, s, "qag_run_steps_pass_all", map[string]any{"runId": run.ID, "steps": revisions}, "conflict")
			run = mcpCall[runs.Run](t, f, s, "qag_run_finish", map[string]any{"id": run.ID})
			if run.Status != "failed" {
				t.Fatal("original mismatch lost")
			}
			mcpCall[runs.Run](t, f, s, "qag_run_save", map[string]any{"id": run.ID})
			mcpError(t, f, s, "qag_run_delete", map[string]any{"id": run.ID}, "conflict")
			mcpCall[catalog.Page[runs.Run]](t, f, s, "qag_runs_list", map[string]any{"testCaseId": c.ID, "retained": true})
			var uiRun runs.Run
			json.Unmarshal(f.api(t, "GET", "/runs/"+run.ID, nil, 200), &uiRun)
			if uiRun.ID != run.ID || len(uiRun.Attempts) != len(run.Attempts) {
				t.Fatal("UI history differs")
			}
			before := calls.Load()
			for _, include := range []bool{true, false} {
				report := mcpCall[runs.Report](t, f, s, "qag_report_prepare", map[string]any{"runId": run.ID, "includeExpected": include})
				if strings.Contains(reporting.Plain(report), "EXPECTED-SENTINEL") != include {
					t.Fatal("expected switch")
				}
				// UI and MCP share the cache: a report prepared by HTTP can be exported by MCP.
				if include {
					json.Unmarshal(f.api(t, "GET", "/runs/"+run.ID+"/report?includeExpected=true", nil, 200), &report)
				}
				for _, format := range []string{"pdf", "text", "markdown", "rich"} {
					exported := mcpCall[mcpserver.ExportResult](t, f, s, "qag_report_export", map[string]any{"runId": run.ID, "snapshotId": report.SnapshotID, "hash": report.Hash, "includeExpected": include, "format": format})
					for _, a := range exported.Files {
						data := artifactBytes(t, f, s, a)
						if len(data) == 0 {
							t.Fatal("empty export")
						}
						if format == "text" && bytes.Contains(data, []byte("EXPECTED-SENTINEL")) != include {
							t.Fatal("export expectation switch")
						}
					}
				}
				if report.OwnerCaseID != "" {
					t.Fatal("private owner exposed")
				}
			}
			if calls.Load() != before {
				t.Fatal("read/export caused network")
			}
			// Cancel and delete an unretained run; read-only demo identity is stable.
			args["idempotencyKey"] = catalog.NewID()
			cancelled := mcpCall[runs.Run](t, f, s, "qag_run_start", args)
			mcpCall[runs.Run](t, f, s, "qag_run_cancel", map[string]any{"id": cancelled.ID})
			mcpCall[mcpserver.Deleted](t, f, s, "qag_run_delete", map[string]any{"id": cancelled.ID})
			vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
			info, err := demo.Bootstrap(f.ctx, f.store.Pool, vault, f.server.URL, "127.0.0.1:19091", resources.Config{Address: "127.0.0.1:15433", Database: "qag", Username: "fixture_reader", TLS: resources.TLSConfig{Mode: "plaintext"}}, map[string]string{"PASSWORD": "fixture-only"})
			if err != nil {
				t.Fatal(err)
			}
			got := mcpCall[builtin.Info](t, f, s, "qag_demo_get", map[string]any{})
			if got.TestCaseID != info.TestCaseID {
				t.Fatal("demo identity")
			}
			mcpError(t, f, s, "qag_test_case_update", map[string]any{"id": info.TestCaseID, "name": "no"}, "read_only")
			mcpError(t, f, s, "qag_environment_update", map[string]any{"id": info.EnvironmentID, "revision": 1, "name": "no"}, "read_only")
		})
	}
}
