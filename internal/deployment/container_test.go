//go:build container

package deployment_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/httpapi"
	"github.com/SardelkaS/qag/internal/runs"
	"github.com/SardelkaS/qag/internal/testfixture"
)

// This test uses the SAME PostgreSQL and the normal manifest/limited-role
// fixture. It never starts a second PostgreSQL or touches production schemas.
func TestContainerRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	docker := os.Getenv("QAG_DOCKER")
	if docker == "" {
		docker = "docker"
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	name := "qag-smoke-" + fixture.ID
	command := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, docker, args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	t.Cleanup(func() { exec.Command(docker, "rm", "-f", name).Run() })
	rebase := func(dsn string) string {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		u.Host = "postgres:5432"
		return u.String()
	}
	env := []string{"QAG_DATABASE_URL=" + rebase(fixture.AppURL), "QAG_DEMO_DATABASE_URL=" + rebase(fixture.ReaderURL), "QAG_TEST_MODE=1", "QAG_SCHEMA=" + fixture.Namespace.App, "QAG_DEMO_SCHEMA=" + fixture.Namespace.Demo, "QAG_SECRET_KEY=" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{91}, 32)), "QAG_NETWORK_MODE=container", "QAG_ADDRESS=0.0.0.0:8080", "QAG_PUBLIC_ORIGIN=http://127.0.0.1:18088"}
	path := filepath.Join(t.TempDir(), "private.env")
	if err = os.WriteFile(path, []byte(strings.Join(env, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	create := func() {
		command("create", "--name", name, "--network", "qag_default", "--env-file", path, "-p", "127.0.0.1:18088:8080", "qag:local")
	}
	base := "http://127.0.0.1:18088"
	csrf := ""
	request := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var b io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			b = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, method, base+path, b)
		req.Header.Set("Origin", base)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-QAG-CSRF", csrf)
		req.Header.Set("Idempotency-Key", catalog.NewID())
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		out, _ := io.ReadAll(r.Body)
		return r.StatusCode, out
	}
	api := func(method, path string, body, out any) {
		t.Helper()
		status, raw := request(method, "/api/v1"+path, body)
		if status != 200 {
			t.Fatalf("%s %s: %d %s", method, path, status, raw)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatal(err)
			}
		}
	}
	ready := func() {
		t.Helper()
		for n := 0; n < 100; n++ {
			r, e := http.Get(base + "/api/v1/health")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					var session map[string]string
					api("GET", "/session", nil, &session)
					csrf = session["csrfToken"]
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("not ready: ", command("logs", name))
	}
	create()
	command("start", name)
	ready()
	if uid := command("exec", name, "id", "-u"); uid != "10001" {
		t.Fatal("runtime is privileged")
	}
	command("exec", name, "qag", "healthcheck")
	command("exec", name, "sh", "-c", "test ! -e /src && test ! -e /app/.local && test ! -e /usr/local/go && test ! -e /usr/local/bin/node && test -s /etc/ssl/certs/ca-certificates.crt")
	for _, asset := range []string{"/", "/favicon.ico", "/qag-32.png", "/fonts/NotoSans-Regular.ttf", "/fonts/OFL.txt"} {
		status, _ := request("GET", asset, nil)
		if status != 200 {
			t.Fatalf("asset %s: %d", asset, status)
		}
	}
	var folder catalog.Folder
	api("POST", "/folders", map[string]string{"name": "Container persistence"}, &folder)
	var doc catalog.TestCase
	api("POST", "/folders/"+folder.ID+"/test-cases", map[string]string{}, &doc)
	api("PUT", "/test-cases/"+doc.ID, httpapi.TestCaseWrite{Name: "Сохранённый в контейнере", Revision: doc.Revision}, &doc)
	var info builtin.Info
	api("GET", "/demo", nil, &info)
	var demo catalog.TestCase
	api("GET", "/test-cases/"+info.TestCaseID, nil, &demo)
	var run runs.Run
	expectedCalls := 0
	for _, step := range demo.Steps {
		expectedCalls += len(step.Blocks)
	}
	api("POST", "/test-cases/"+demo.ID+"/runs", httpapi.RunStart{EnvironmentID: info.EnvironmentID, Revision: demo.Revision, ExecuteAll: true}, &run)
	for n := 0; n < 100; n++ {
		api("GET", "/runs/"+run.ID, nil, &run)
		if len(run.Attempts) == expectedCalls && run.Attempts[expectedCalls-1].FinishedAt != nil && len(run.Batches) > 0 && run.Batches[0].State != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(run.Attempts) != expectedCalls {
		t.Fatal("three protocol attempts missing")
	}
	for _, a := range run.Attempts {
		if a.State != "completed" || a.Result == nil || a.Result.Comparison.Status != "match" {
			t.Fatalf("protocol block %s: state %s result %+v", a.BlockID, a.State, a.Result)
		}
	}
	// Each research protocol retains the actual response, without inheriting the
	// original expectation. Negative codes are facts, not failed comparisons.
	for index := 0; index < 3; index++ {
		block := demo.Steps[index].Blocks[0]
		request := catalog.RequestFromBlock(block, false)
		if request.HTTP != nil {
			copy := *request.HTTP
			copy.Path = "/items/missing-item"
			request.HTTP = &copy
		}
		if request.GRPC != nil {
			copy := *request.GRPC
			copy.Body = `{"id":"missing-item"}`
			request.GRPC = &copy
		}
		var variant runs.Variant
		api("POST", "/runs/"+run.ID+"/blocks/"+block.ID+"/variants", httpapi.VariantWrite{Name: "Исследование " + block.Kind}, &variant)
		if len(variant.Versions) != 1 || variant.Versions[0].Request.Expected != nil {
			t.Fatal("research version inherited expected")
		}
		api("PATCH", "/runs/"+run.ID+"/variants/"+variant.ID, httpapi.VariantWrite{Name: variant.Name, Revision: variant.Revision, Request: &request}, &variant)
		var attempt execution.Attempt
		api("POST", "/runs/"+run.ID+"/variants/"+variant.ID+"/attempts", httpapi.AttemptStart{Revision: variant.Revision}, &attempt)
		for n := 0; n < 100; n++ {
			api("GET", "/attempts/"+attempt.ID, nil, &attempt)
			if attempt.FinishedAt != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if attempt.State != "completed" || attempt.Result == nil || attempt.Request.Expected != nil || attempt.Result.Comparison.Status != "not_compared" || len(attempt.Result.Comparison.Differences) != 0 {
			t.Fatalf("research %s did not retain request-only actual result: %+v", block.Kind, attempt)
		}
		if index == 0 && (attempt.Result.StatusCode == nil || *attempt.Result.StatusCode != 404) {
			t.Fatal("HTTP actual status lost")
		}
		if index == 1 && attempt.Result.GRPCCode != "NOT_FOUND" {
			t.Fatal("gRPC actual status lost")
		}
		if index == 2 && (len(attempt.Result.Rows) == 0 || len(attempt.Result.Columns) == 0) {
			t.Fatal("SQL actual rows lost")
		}
		expectedCalls++
	}
	api("GET", "/runs/"+run.ID, nil, &run)
	bulk := httpapi.StepsPassAll{}
	for _, step := range run.StepStates {
		bulk.Steps = append(bulk.Steps, runs.StepRevision{StepID: step.StepID, Revision: step.Revision})
	}
	api("POST", "/runs/"+run.ID+"/steps/pass-all", bulk, &run)
	if run.Status != "running" || len(run.Attempts) != expectedCalls {
		t.Fatal("manual bulk command executed or completed a run")
	}
	api("POST", "/runs/"+run.ID+"/finish", map[string]any{}, &run)
	mcpRunID := smokeMCP(t, ctx, base, true)
	// The host browser checks the actual Linux capability and every export format.
	browser := exec.CommandContext(ctx, "node", filepath.Join(root, "web/scripts/container-smoke.mjs"), base, run.ID)
	browser.Dir = root
	if out, err := browser.CombinedOutput(); err != nil {
		t.Fatalf("host browser: %v: %s", err, out)
	} else {
		t.Log(strings.TrimSpace(string(out)))
	}
	// Keep a manual run with one completed call, then SIGTERM. Recovery must
	// interrupt the run without repeating the completed call or starting the rest.
	var pending runs.Run
	api("POST", "/test-cases/"+demo.ID+"/runs", httpapi.RunStart{EnvironmentID: info.EnvironmentID, Revision: demo.Revision}, &pending)
	var block string
	for _, step := range demo.Steps {
		if len(step.Blocks) > 0 {
			block = step.Blocks[0].ID
			break
		}
	}
	var attempt execution.Attempt
	api("POST", "/runs/"+pending.ID+"/blocks/"+block+"/attempts", map[string]any{}, &attempt)
	for n := 0; n < 100; n++ {
		api("GET", "/attempts/"+attempt.ID, nil, &attempt)
		if attempt.FinishedAt != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	command("stop", "--time", "30", name)
	if code := command("inspect", "--format", "{{.State.ExitCode}}", name); code != "0" {
		t.Fatalf("SIGTERM exit %s", code)
	}
	command("start", name)
	ready()
	api("GET", "/runs/"+pending.ID, nil, &pending)
	if pending.Status != "interrupted" || len(pending.Attempts) != 1 || pending.Attempts[0].ID != attempt.ID {
		t.Fatal("recovery repeated a call or lost result")
	}
	// Recreate using the exact same private key and fixture namespaces.
	command("rm", "-f", name)
	create()
	command("start", name)
	ready()
	var saved catalog.TestCase
	api("GET", "/test-cases/"+doc.ID, nil, &saved)
	if saved.Name != doc.Name {
		t.Fatal("case lost")
	}
	var same builtin.Info
	api("GET", "/demo", nil, &same)
	if same != info {
		t.Fatal("builtin identity changed")
	}
	api("GET", "/runs/"+run.ID, nil, &run)
	if len(run.Attempts) != expectedCalls || run.Status != "passed" {
		t.Fatal("history lost")
	}
	var mcpHistory runs.Run
	api("GET", "/runs/"+mcpRunID, nil, &mcpHistory)
	if mcpHistory.Status != "passed" || len(mcpHistory.Attempts) != 5 {
		t.Fatal("MCP history lost after recreate")
	}
	t.Log(fmt.Sprintf("one PostgreSQL; isolated %s; all protocols, browser exports, SIGTERM and recreate passed", fixture.Namespace.App))
}
