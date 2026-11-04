package execution_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/resources"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeaderSourceModesIsolationAndRedaction(t *testing.T) {
	var tokens, main atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != "POST" || r.URL.Query().Get("q") != "space & slash/" || body["secret"] != "auth-password" {
			t.Error("source parameters lost")
		}
		fmt.Fprintf(w, `{"data":{"token":"secret-token-%d"}}`, tokens.Add(1))
	}))
	defer source.Close()
	actual := make(chan string, 16)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		main.Add(1)
		value := r.Header.Get("Authorization")
		actual <- value
		_ = json.NewEncoder(w).Encode(map[string]string{"reflected": value})
	}))
	defer target.Close()
	status := 200
	src := &catalog.HTTPHeaderSource{Method: "POST", URL: source.URL + "?q=space+%26+slash%2F", BodyMode: "json", Body: `{"secret":"${CLIENT_SECRET}"}`, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/data/token", Prefix: "Bearer ", FetchMode: "per_run"}
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "Authorization", Source: src}}}}, Secrets: map[string]string{"CLIENT_SECRET": "auth-password"}}
	block := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &status, BodyMode: "none"}}
	runner := execution.NewRunner()
	defer runner.ClearHeaderScopes()
	run := func(scope string) execution.Result {
		t.Helper()
		b, r, err := res.Prepare(block)
		if err != nil {
			t.Fatal(err)
		}
		state, result := runner.Execute(context.Background(), scope, b, r)
		if state != "completed" || result.Comparison.Status != "match" {
			t.Fatalf("execution: %s %s", state, result.Error)
		}
		value := <-actual
		if strings.Contains(result.Body, value) || strings.Contains(result.Body, strings.TrimPrefix(value, "Bearer ")) || !result.Redacted {
			t.Fatal("derived secret leaked")
		}
		return result
	}
	first := run("run-one")
	second := run("run-one")
	if tokens.Load() != 1 || first.HeaderSources[0].State != "fetched" || second.HeaderSources[0].State != "reused" {
		t.Fatal("per-run reuse failed")
	}
	run("run-two")
	run("trial-one")
	run("trial-two")
	if tokens.Load() != 4 {
		t.Fatal("scopes mixed")
	}
	src.FetchMode = "per_attempt"
	run("run-one")
	run("run-one")
	if tokens.Load() != 6 || main.Load() != 7 {
		t.Fatal("per-attempt reuse or duplication")
	}
	if src.Body != `{"secret":"${CLIENT_SECRET}"}` {
		t.Fatal("preparation mutated public source config")
	}
	// The overridden source contains an unknown variable. It must be ignored
	// before preparation as well as before network execution.
	src.Body = `{"secret":"${MISSING}"}`
	block.HTTP.Headers = []catalog.RequestHeader{{Name: "authorization", Value: "literal"}}
	b, r, err := res.Prepare(block)
	if err != nil {
		t.Fatal(err)
	}
	_, result := runner.Execute(context.Background(), "override", b, r)
	<-actual
	if tokens.Load() != 6 || result.MainRequestStarted != nil {
		t.Fatal("overridden source executed")
	}
}

func TestHeaderSourceFailureRetryConfigAndTimeout(t *testing.T) {
	var sources, main atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := sources.Add(1)
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintf(w, `{"token":"retry-secret-%d"}`, n)
	}))
	defer source.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { main.Add(1) }))
	defer target.Close()
	src := &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", FetchMode: "per_run"}
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}}}}
	code := 200
	block := catalog.Block{Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none", Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "Authorization", Source: src}}}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	runner := execution.NewRunner()
	defer runner.ClearHeaderScopes()
	execute := func(ctx context.Context) (string, execution.Result) {
		t.Helper()
		b, r, e := res.Prepare(block)
		if e != nil {
			t.Fatal(e)
		}
		return runner.Execute(ctx, "scope", b, r)
	}
	state, _ := execute(context.Background())
	if state != "error" || sources.Load() != 1 || main.Load() != 0 {
		t.Fatal("source failure reached main")
	}
	state, result := execute(context.Background())
	if state != "completed" || result.HeaderSources[0].State != "fetched" || sources.Load() != 2 {
		t.Fatal("failed source was cached")
	}
	execute(context.Background())
	if sources.Load() != 2 {
		t.Fatal("successful source not cached")
	}
	src.Prefix = "Bearer "
	execute(context.Background())
	if sources.Load() != 3 {
		t.Fatal("changed binding used old config's cached token")
	}
	runner.ForgetScope("scope")
	execute(context.Background())
	if sources.Load() != 4 {
		t.Fatal("terminal scope was retained")
	}
	// Every known configuration error is found before any source sends a request.
	block.HTTP.Headers = append(block.HTTP.Headers, catalog.RequestHeader{Name: "X-Invalid", Value: "${MISSING}"})
	if _, _, err := res.Prepare(block); err == nil || sources.Load() != 4 {
		t.Fatal("invalid effective headers were not rejected purely")
	}
	block.HTTP.Headers = block.HTTP.Headers[:1]
	src.URL = source.URL + "/wait"
	before := main.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	state, result = execute(ctx)
	if state != "timed_out" || main.Load() != before || result.MainRequestStarted == nil || *result.MainRequestStarted {
		t.Fatal("source timeout reached main or lost common deadline")
	}
}

func TestHeaderSourceErrorsDoNotCallMain(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"http401", `{"token":"do-not-save"}`, 401}, {"redirect", `{}`, 302}, {"bad-json", `{`, 200}, {"missing", `{}`, 200}, {"null", `{"token":null}`, 200}, {"object", `{"token":{}}`, 200}, {"crlf", `{"token":"x\r\ny"}`, 200}, {"oversize", strings.Repeat("x", 5<<20+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var main atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "http://must-not-follow.invalid")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer source.Close()
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { main.Add(1) }))
			defer target.Close()
			runner := execution.NewRunner()
			defer runner.ClearHeaderScopes()
			code := 200
			b := catalog.Block{Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none", Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", FetchMode: "per_attempt"}}}}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
			res := resources.Resolved{Resource: resources.Resource{Config: resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}}}}
			state, result := runner.Execute(context.Background(), tc.name, b, res)
			if state != "error" || main.Load() != 0 || len(result.HeaderSources) != 1 || result.MainRequestStarted == nil || *result.MainRequestStarted || strings.Contains(result.Error, "do-not-save") {
				t.Fatalf("main was called or unsafe failure: %s %s", state, result.Error)
			}
		})
	}
}

func TestHeaderJSONPointerPrecisionAndCancellation(t *testing.T) {
	for _, body := range []string{`{"a/b":{"~tokens":[9007199254740993123]}}`, `{"a/b":{"~tokens":[true]}}`} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		got := make(chan string, 1)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got <- r.Header.Get("X-Value") }))
		runner := execution.NewRunner()
		b := catalog.Block{Kind: "http", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none", Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "X-Value", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/a~1b/~0tokens/0", FetchMode: "per_attempt"}}}}}
		state, _ := runner.Execute(context.Background(), "precision", b, resources.Resolved{Resource: resources.Resource{Config: resources.Config{BaseURL: target.URL, TLS: resources.TLSConfig{Mode: "plaintext"}}}})
		if state != "completed" {
			t.Fatal("pointer execution failed")
		}
		value := <-got
		if value != "9007199254740993123" && value != "true" {
			t.Fatalf("precision lost: %s", value)
		}
		runner.ClearHeaderScopes()
		source.Close()
		target.Close()
	}
	started := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer source.Close()
	runner := execution.NewRunner()
	defer runner.ClearHeaderScopes()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := catalog.Block{Kind: "http", HTTP: &catalog.HTTPRequest{Method: "GET", BodyMode: "none", Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "X", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, FetchMode: "per_attempt"}}}}}
	done := make(chan string, 1)
	go func() { state, _ := runner.Execute(ctx, "cancel", b, resources.Resolved{}); done <- state }()
	<-started
	cancel()
	select {
	case state := <-done:
		if state != "cancelled" {
			t.Fatal(state)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source cancellation did not return")
	}
}
