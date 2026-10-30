//go:build integration

package service_test

import "qag/internal/dbscope"

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"qag/internal/testfixture"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"qag/internal/catalog"
	"qag/internal/demo"
	"qag/internal/execution"
	"qag/internal/httpapi"
	"qag/internal/mcpserver"
	"qag/internal/reporting"
	"qag/internal/resources"
	"qag/internal/runs"
	appruntime "qag/internal/runtime"
	"qag/internal/service"
	"qag/internal/storage"
	"qag/web"
)

type authoringFixture struct {
	ctx     context.Context
	store   *storage.Store
	c       *catalog.Repository
	res     *resources.Repository
	engine  *service.Engine
	env     resources.Environment
	server  *httptest.Server
	csrf    string
	db      *testfixture.Database
	reports *reporting.Service
	mcp     *mcpserver.Server
	audit   *bytes.Buffer
}

func authoringApp(t *testing.T) *authoringFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	dbFixture := testfixture.ForTest(t, ctx)
	store, err := storage.Open(ctx, dbFixture.AppURL)
	if err != nil {
		t.Fatal(err)
	}
	f := &authoringFixture{ctx: ctx, store: store, db: dbFixture}
	t.Cleanup(func() {
		if f.server != nil {
			f.server.Close()
		}
		if f.engine != nil {
			f.engine.Close()
		}
		store.Close()
		clean, done := context.WithTimeout(context.Background(), 8*time.Second)
		defer done()
		_ = clean
		cancel()
	})
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
	f.c = catalog.NewRepository(store.Pool)
	f.res = resources.NewRepository(store.Pool, vault)
	f.engine = service.New(ctx, f.c, f.res, runs.NewRepository(store.Pool))
	f.env, err = f.res.SaveEnvironmentMetadata(ctx, "", "Test environment", "Original description", 0)
	if err != nil {
		t.Fatal(err)
	}
	f.env, err = f.res.SaveVariables(ctx, f.env.ID, f.env.Revision, map[string]string{"TASK_ID": "old"}, resources.SecretChanges{Replace: map[string]string{"CLIENT_SECRET": "fixture-auth-password"}})
	if err != nil {
		t.Fatal(err)
	}
	f.server = httptest.NewUnstartedServer(nil)
	api, err := httpapi.New(appruntime.Config{Address: f.server.Listener.Addr().String()}, web.Assets(), store.Ping)
	if err != nil {
		t.Fatal(err)
	}
	api.RegisterCatalog(f.c)
	api.RegisterResources(f.res)
	f.reports = reporting.NewService(f.engine.Runs, f.c, web.Assets(), false)
	api.RegisterRuns(f.engine, f.reports)
	f.audit = &bytes.Buffer{}
	f.mcp = mcpserver.New(mcpserver.Options{Engine: f.engine, Reports: f.reports, Ping: store.Ping, Logger: slog.New(slog.NewJSONHandler(f.audit, nil))})
	api.RegisterMCP(f.mcp.Handler())
	f.server.Config.Handler = api.Handler()
	f.server.Start()
	response, err := http.Get(f.server.URL + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	f.csrf = session.CSRFToken
	return f
}
func (f *authoringFixture) api(t *testing.T, method, path string, body any, want int) []byte {
	t.Helper()
	data, _ := json.Marshal(body)
	request, err := http.NewRequest(method, f.server.URL+"/api/v1"+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.server.URL)
	request.Header.Set("X-QAG-CSRF", f.csrf)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	value, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s status %d, wanted %d: %s", method, path, response.StatusCode, want, value)
	}
	return value
}
func (f *authoringFixture) caseInFolder(t *testing.T) catalog.TestCase {
	t.Helper()
	folder, err := f.c.CreateFolder(f.ctx, "Folder "+catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.c.CreateCase(f.ctx, folder.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func awaitAuthoringAttempt(t *testing.T, f *authoringFixture, a execution.Attempt) execution.Attempt {
	t.Helper()
	for n := 0; n < 300; n++ {
		a, _ = f.engine.GetAttempt(f.ctx, a.ID)
		if a.State != "queued" && a.State != "running" {
			return a
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("attempt not completed")
	return a
}

func TestAuthoringDraftImagesMetadataAndHistory(t *testing.T) {
	f := authoringApp(t)
	c := f.caseInFolder(t)
	// Empty names are assigned atomically; a partial JSON body survives storage.
	names := make(chan string, 2)
	for n := 0; n < 2; n++ {
		go func() {
			other, err := f.c.CreateCase(f.ctx, c.FolderID, "")
			if err != nil {
				names <- "error"
			} else {
				names <- other.Name
			}
		}()
	}
	one, two := <-names, <-names
	if one == two || one == "error" || two == "error" {
		t.Fatal("automatic name collision")
	}
	draft := []catalog.Step{{ID: catalog.NewID(), Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "grpc", GRPC: &catalog.GRPCRequest{Body: `{"half":`}}}}}
	saved, err := f.c.SaveCase(f.ctx, c.ID, "", c.Revision, draft)
	if err != nil || len(saved.ValidationIssues) == 0 || saved.Steps[0].Blocks[0].GRPC.Body != `{"half":` {
		t.Fatalf("draft lost: %v", err)
	}
	if _, err = f.engine.StartRun(f.ctx, c.ID, f.env.ID, saved.Revision, catalog.NewID()); err == nil {
		t.Fatal("draft started")
	}
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	upload := catalog.ImageUpload{Filename: "reference.png", DataBase64: base64.StdEncoding.EncodeToString(img.Bytes())}
	raw := f.api(t, "POST", "/test-cases/"+c.ID+"/images", upload, 200)
	var picture catalog.ExpectedImage
	if json.Unmarshal(raw, &picture) != nil || picture.ID == "" {
		t.Fatal("upload response")
	}
	downloaded := f.api(t, "GET", "/test-cases/"+c.ID+"/images/"+picture.ID, nil, 200)
	if !bytes.Equal(downloaded, img.Bytes()) {
		t.Fatal("download differs")
	}
	steps := []catalog.Step{{ID: catalog.NewID(), Expected: &catalog.StepExpectation{Text: "Compare manually", Images: []catalog.ImageRef{{ImageID: picture.ID, Caption: "Original"}}}}}
	saved, err = f.c.SaveCase(f.ctx, c.ID, saved.Name, saved.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.engine.StartRun(f.ctx, c.ID, f.env.ID, saved.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.Runs.UpdateStep(f.ctx, run.ID, steps[0].ID, "passed", "Image checked", 1); err != nil {
		t.Fatal(err)
	}
	run, err = f.engine.FinishRun(f.ctx, run.ID)
	if err != nil || run.Status != "passed" || len(run.RetentionReasons) != 2 {
		t.Fatalf("manual image run failed: %v", err)
	}
	steps[0].Expected.Images = nil
	saved, err = f.c.SaveCase(f.ctx, c.ID, saved.Name, saved.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.engine.GetRun(f.ctx, run.ID)
	if err != nil || old.Snapshot.Steps[0].Expected.Images[0].Caption != "Original" {
		t.Fatal("image history changed")
	}
	other, err := f.c.CreateCase(f.ctx, c.FolderID, "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign := []catalog.Step{{ID: catalog.NewID(), Expected: &catalog.StepExpectation{Images: []catalog.ImageRef{{ImageID: picture.ID}}}}}
	if _, err = f.c.SaveCase(f.ctx, other.ID, other.Name, other.Revision, foreign); err == nil {
		t.Fatal("foreign image accepted")
	}
	f.api(t, "GET", "/test-cases/"+other.ID+"/images/"+picture.ID, nil, 404)
	// Metadata is independent of the versioned variable/secret payload.
	raw = f.api(t, "PUT", "/environments/"+f.env.ID, map[string]any{"name": f.env.Name, "description": "Changed description", "revision": f.env.Revision}, 200)
	var env resources.Environment
	_ = json.Unmarshal(raw, &env)
	if env.Description != "Changed description" || env.Variables["TASK_ID"] != "old" || len(env.SecretNames) != 1 {
		t.Fatal("metadata discarded variables")
	}
	f.api(t, "PUT", "/environments/"+env.ID+"/variables", map[string]any{"revision": f.env.Revision, "variables": map[string]string{}}, 409)
	f.api(t, "POST", "/environments", map[string]any{"name": "Wrong contract", "variables": map[string]string{"X": "x"}}, 400)
	raw = f.api(t, "POST", "/environments", map[string]string{"name": "Metadata only", "description": "Optional"}, 200)
	var freshEnv resources.Environment
	_ = json.Unmarshal(raw, &freshEnv)
	if len(freshEnv.Variables) != 0 {
		t.Fatal("new env not empty")
	}
	archived := true
	if _, err = f.c.PatchCase(f.ctx, c.ID, nil, &archived); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/test-cases/"+c.ID+"/images", upload, 409)
	f.api(t, "GET", "/test-cases/"+c.ID+"/images/"+picture.ID, nil, 200)
	if _, err = f.c.PatchFolder(f.ctx, c.FolderID, nil, &archived); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/test-cases/"+other.ID+"/images", upload, 409)
	vault, _ := resources.NewVault(bytes.Repeat([]byte{8}, 32))
	info, err := demo.Bootstrap(f.ctx, f.store.Pool, vault, f.server.URL, "127.0.0.1:19091", resources.Config{Address: "127.0.0.1:15433", Database: "fixture", Username: "fixture_reader", TLS: resources.TLSConfig{Mode: "plaintext"}}, map[string]string{"PASSWORD": "fixture-only"})
	if err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/test-cases/"+info.TestCaseID+"/images", upload, 409)
	f.api(t, "PUT", "/environments/"+info.EnvironmentID+"/variables", map[string]any{"revision": 1, "variables": map[string]string{"X": "x"}}, 409)
}

func TestHeaderSourcesRespectDurabilityVersionsAndRunScope(t *testing.T) {
	f := authoringApp(t)
	var tokens, requests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["secret"] != "fixture-auth-password" {
			t.Error("source secret missing")
		}
		fmt.Fprintf(w, `{"token":"derived-secret-%d"}`, tokens.Add(1))
	}))
	defer source.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"reflected": r.Header.Get("Authorization")})
	}))
	defer target.Close()
	header := catalog.RequestHeader{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "POST", URL: source.URL, BodyMode: "json", Body: `{"secret":"${CLIENT_SECRET}"}`, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "Bearer ", FetchMode: "per_run"}}
	_, err := f.res.SaveResource(f.ctx, "", f.env.ID, "GW", "http", 0, resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{header}}, resources.SecretChanges{})
	if err != nil {
		t.Fatal(err)
	}
	c := f.caseInFolder(t)
	var imageBytes bytes.Buffer
	_ = png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	picture, err := f.c.UploadImage(f.ctx, c.ID, catalog.ImageUpload{Filename: "trial.png", DataBase64: base64.StdEncoding.EncodeToString(imageBytes.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	code := 200
	block := func() catalog.Block {
		return catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	}
	steps := []catalog.Step{{ID: catalog.NewID(), Blocks: []catalog.Block{block(), block()}}, {ID: catalog.NewID()}}
	steps[0].Blocks[0].Expected.Images = []catalog.ImageRef{{ImageID: picture.ID, Caption: "Trial original"}}
	c, err = f.c.SaveCase(f.ctx, c.ID, c.Name, c.Revision, steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.StartRun(f.ctx, c.ID, f.env.ID, c.Revision, catalog.NewID()); err == nil || tokens.Load() != 0 {
		t.Fatal("invalid full run performed network")
	}
	key := catalog.NewID()
	a, err := f.engine.EditorAttempt(f.ctx, c.ID, steps[0].Blocks[0].ID, f.env.ID, c.Revision, key)
	if err != nil {
		t.Fatal(err)
	}
	a = awaitAuthoringAttempt(t, f, a)
	if a.State != "completed" || !a.Result.Redacted || tokens.Load() != 1 || requests.Load() != 1 {
		t.Fatal("valid isolated block failed")
	}
	repeat, err := f.engine.EditorAttempt(f.ctx, c.ID, steps[0].Blocks[0].ID, f.env.ID, c.Revision, key)
	if err != nil || repeat.ID != a.ID || tokens.Load() != 1 {
		t.Fatal("delivery repeated source")
	}
	var stored string
	if err = f.store.Pool.QueryRow(f.ctx, dbscope.Render(f.store.Pool, `SELECT result::text||safe_request::text FROM qag.call_attempts WHERE id=$1`), a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "derived-secret-1") || strings.Contains(stored, "fixture-auth-password") {
		t.Fatal("source secret persisted")
	}
	steps[0].Blocks[0].Expected.Images = nil
	c, err = f.c.SaveCase(f.ctx, c.ID, c.Name, c.Revision, steps[:1])
	if err != nil {
		t.Fatal(err)
	}
	oldTrial, err := f.engine.GetAttempt(f.ctx, a.ID)
	if err != nil || oldTrial.Request.Expected.Images[0].Caption != "Trial original" {
		t.Fatal("old trial image changed")
	}
	run, err := f.engine.StartRun(f.ctx, c.ID, f.env.ID, c.Revision, catalog.NewID())
	if err != nil || tokens.Load() != 1 {
		t.Fatalf("start fetched source: %v", err)
	}
	for index, b := range steps[0].Blocks {
		a, err = f.engine.RunAttempt(f.ctx, run.ID, b.ID, catalog.NewID())
		if err != nil {
			t.Fatal(err)
		}
		a = awaitAuthoringAttempt(t, f, a)
		if a.State != "completed" {
			t.Fatal(a.State)
		}
		if index == 0 {
			resource, err := f.res.ListResources(f.ctx, f.env.ID, 100, "")
			if err != nil {
				t.Fatal(err)
			}
			updated := resource.Items[0]
			updated.Config.Headers[0].Source.FetchMode = "per_attempt"
			updated.Config.Headers[0].Source.Prefix = "Changed "
			if _, err = f.res.SaveResource(f.ctx, updated.ID, "", updated.Name, updated.Kind, updated.Revision, updated.Config, resources.SecretChanges{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if tokens.Load() != 2 || requests.Load() != 3 {
		t.Fatal("run cache not reused")
	}
	current, err := f.engine.GetRun(f.ctx, run.ID)
	if err != nil || current.Resources[0].Config.Headers[0].Source.Body != `{"secret":"${CLIENT_SECRET}"}` || current.Resources[0].Config.Headers[0].Source.FetchMode != "per_run" {
		t.Fatal("public snapshot mutated")
	}
	// A lost result write must retry only persistence, including after a source
	// has already issued a one-time token.
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_authoring_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state NOT IN ('queued','running') THEN RAISE EXCEPTION 'result write refused'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_authoring_result BEFORE UPDATE ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.reject_authoring_result()`), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.engine.EditorAttempt(f.ctx, c.ID, steps[0].Blocks[0].ID, f.env.ID, c.Revision, catalog.NewID())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending, err = f.engine.GetAttempt(f.ctx, pending.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pending.PersistencePending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !pending.PersistencePending || tokens.Load() != 3 || requests.Load() != 4 {
		t.Fatal("source result was not visibly pending or network repeated")
	}
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `DROP TRIGGER reject_authoring_result ON qag.call_attempts; DROP FUNCTION qag.reject_authoring_result()`), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	pending = awaitAuthoringAttempt(t, f, pending)
	if pending.State != "completed" || tokens.Load() != 3 || requests.Load() != 4 {
		t.Fatal("source or main request replayed after persistence recovery")
	}
	// Simulate failed durable registration: there must be no token request.
	_, err = f.store.Pool.Exec(f.ctx, dbscope.Render(f.store.Pool, `CREATE FUNCTION qag.reject_authoring_attempt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test begin rejected'; END $$; CREATE TRIGGER reject_authoring_attempt BEFORE INSERT ON qag.call_attempts FOR EACH ROW EXECUTE FUNCTION qag.reject_authoring_attempt()`), pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.engine.EditorAttempt(f.ctx, c.ID, steps[0].Blocks[0].ID, f.env.ID, c.Revision, catalog.NewID()); err == nil || tokens.Load() != 3 || requests.Load() != 4 {
		t.Fatal("registration failure performed network")
	}
}
