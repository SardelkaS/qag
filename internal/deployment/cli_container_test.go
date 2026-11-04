//go:build container

package deployment_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/runs"
)

func TestContainerYAMLWithoutPostgres(t *testing.T) {
	docker := os.Getenv("QAG_DOCKER")
	if docker == "" {
		docker = "docker"
	}
	name := "qag-yaml-smoke-" + catalog.NewID()
	volume := name + "-data"
	command := func(args ...string) string {
		t.Helper()
		b, e := exec.Command(docker, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("docker %s: %v %s", args[0], e, b)
		}
		return string(b)
	}
	command("volume", "create", volume)
	t.Cleanup(func() {
		exec.Command(docker, "rm", "-f", name).Run()
		exec.Command(docker, "volume", "rm", volume).Run()
	})
	command("run", "--rm", "--user", "0:0", "--entrypoint", "/usr/bin/install", "-v", volume+":/data", "qag:local", "-d", "-o", "10001", "-g", "10001", "-m", "0700", "/data")
	command("run", "-d", "--name", name, "-v", volume+":/data", "-p", "127.0.0.1:18089:8080", "-e", "QAG_STORAGE=yaml", "-e", "QAG_DATA_FILE=/data/qag.yaml", "-e", "QAG_NETWORK_MODE=container", "-e", "QAG_ADDRESS=0.0.0.0:8080", "-e", "QAG_PUBLIC_ORIGIN=http://127.0.0.1:18089", "-e", "QAG_DEMO_GRPC_ADDRESS=127.0.0.1:0", "qag:local")
	base := "http://127.0.0.1:18089"
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	for i := 0; i < 100; i++ {
		r, e := client.Get(base + "/api/v1/health")
		if e == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("YAML container not ready", command("logs", name))
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	resp, e := client.Get(base + "/api/v1/session")
	if e != nil {
		t.Fatal(e)
	}
	json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	api := func(method, path string, body any, out any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, e := http.NewRequest(method, base+"/api/v1"+path, bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Origin", base)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-QAG-CSRF", session.CSRFToken)
		req.Header.Set("Idempotency-Key", catalog.NewID())
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		b, _ = io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			t.Fatalf("API %s: %d %s", path, r.StatusCode, b)
		}
		if out != nil && json.Unmarshal(b, out) != nil {
			t.Fatal("API JSON")
		}
	}
	var info builtin.Info
	api("GET", "/demo", nil, &info)
	var tc catalog.TestCase
	api("GET", "/test-cases/"+info.TestCaseID, nil, &tc)
	var run runs.Run
	api("POST", "/test-cases/"+tc.ID+"/runs", map[string]any{"environmentId": info.EnvironmentID, "revision": tc.Revision, "executeAll": true}, &run)
	for i := 0; i < 100; i++ {
		api("GET", "/runs/"+run.ID, nil, &run)
		if len(run.Batches) == 1 && run.Batches[0].State == "completed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(run.Attempts) != 5 {
		t.Fatal("demo calls missing")
	}
	for _, a := range run.Attempts {
		if a.Resource.Kind == "postgresql" {
			if a.State != "error" || a.Result == nil || a.Result.ErrorCode != "demo_sql_unavailable" {
				t.Fatal("SQL absence")
			}
		} else if a.State != "completed" || a.Result == nil || a.Result.Comparison.Status != "match" {
			t.Fatal("demo protocol")
		}
	}
	if got := command("exec", name, "stat", "-c", "%u:%g:%a", "/data/qag.yaml"); got != "10001:10001:600\n" {
		t.Fatal("file privacy", got)
	}
	command("restart", name)
	ready = false
	for i := 0; i < 100; i++ {
		r, e := client.Get(base + "/api/v1/health")
		if e == nil {
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatal("restart readiness")
	}
	var restored runs.Run
	api("GET", "/runs/"+run.ID, nil, &restored)
	if restored.Status != "interrupted" || len(restored.Attempts) != 5 {
		t.Fatal("Linux YAML restart lost history or repeated queue")
	}
}
