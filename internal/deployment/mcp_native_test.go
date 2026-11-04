//go:build integration

package deployment_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/testfixture"
)

func TestMCPNativeRuntime(t *testing.T) {
	binary := os.Getenv("QAG_NATIVE_BINARY")
	if binary == "" {
		t.Skip("run scripts/test-mcp.ps1 to build and select the native binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	base := "http://127.0.0.1:18088"
	config := filepath.Join(t.TempDir(), "installation.json")
	raw, _ := json.Marshal(map[string]any{"version": 1, "values": map[string]string{"QAG_MCP_TOKEN": "obsolete-token-ignored", "QAG_DATABASE_URL": fixture.AppURL, "QAG_DEMO_DATABASE_URL": fixture.ReaderURL, "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{92}, 32)), "QAG_TEST_MODE": "1", "QAG_SCHEMA": fixture.Namespace.App, "QAG_DEMO_SCHEMA": fixture.Namespace.Demo}})
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	start := func(disabled bool) (*exec.Cmd, func()) {
		t.Helper()
		cmd := exec.Command(binary)
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "QAG_") {
				cmd.Env = append(cmd.Env, v)
			}
		}
		cmd.Env = append(cmd.Env, "QAG_CONFIG_FILE="+config, "QAG_DATABASE_URL="+fixture.AppURL, "QAG_DEMO_DATABASE_URL="+fixture.ReaderURL, "QAG_TEST_MODE=1", "QAG_SCHEMA="+fixture.Namespace.App, "QAG_DEMO_SCHEMA="+fixture.Namespace.Demo, "QAG_SECRET_KEY="+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{92}, 32)), "QAG_ADDRESS=127.0.0.1:18088", "QAG_DEMO_GRPC_ADDRESS=127.0.0.1:0")
		if disabled {
			cmd.Env = append(cmd.Env, "QAG_MCP_TOKEN=")
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		stop := func() { cmd.Process.Kill(); <-done }
		for n := 0; n < 100; n++ {
			select {
			case err := <-done:
				t.Fatalf("native process stopped: %v", err)
			default:
			}
			r, err := http.Get(base + "/api/v1/health")
			if err == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return cmd, stop
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		stop()
		t.Fatal("native readiness")
		return nil, nil
	}
	process, stop := start(false)
	func() {
		defer stop()
		runID := smokeMCP(t, ctx, base, false)
		root, err := filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "go", "run", "./cmd/mcp-example", "-url", base+"/mcp", "-protocol", "2025-11-25", "-format", "rich", "-out", filepath.Join(root, ".local/verify/mcp-example"))
		cmd.Dir = root
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("SDK example: %v %s", err, out)
		}
		if runtime.GOOS == "windows" {
			check := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "scripts/test-mcp-clipboard.ps1"), "-RunId", runID, "-ApplicationPid", strconv.Itoa(process.Process.Pid), "-BinaryPath", binary)
			check.Env = os.Environ()
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("native MCP clipboard: %v %s", err, out)
			} else {
				t.Log(strings.TrimSpace(string(out)))
			}
		}
	}()
	_, stop = start(true)
	defer stop()
	req, _ := http.NewRequest("POST", base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"qag_status","arguments":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !bytes.Contains(body, []byte("ready")) {
		t.Fatal("MCP not enabled with empty obsolete token")
	}
}
