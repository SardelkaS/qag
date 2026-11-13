package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestResourceHTTPVariablesPlanCachingAndOpaqueValues(t *testing.T) {
	value := `private / "quoted" & ${OPAQUE}`
	var sourceCalls, mainCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			sourceCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"token": value}})
			return
		case "/unused":
			t.Error("unused source called")
			w.WriteHeader(500)
			return
		}
		mainCalls.Add(1)
		if r.URL.Query().Get("v") != value || r.Header.Get("X-Shared") != value {
			t.Error("incorrect query/header substitution")
		}
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["v"] != value {
			t.Error("incorrect JSON substitution")
		}
		if r.URL.Query().Get("raw") == "1" {
			encoded, _ := json.Marshal(value)
			w.Write(append([]byte("incomplete JSON: "), encoded...))
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"value": value, "escaped": url.QueryEscape(value)})
	}))
	defer server.Close()
	source := func(p string) *catalog.HTTPHeaderSource {
		return &catalog.HTTPHeaderSource{Method: "GET", URL: server.URL + p, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/data/token", FetchMode: "per_run"}
	}
	res := resources.Resolved{Resource: resources.Resource{ID: catalog.NewID(), VersionID: catalog.NewID(), Config: resources.Config{BaseURL: server.URL, TLS: resources.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "X-Shared", Value: "${V}"}}, Variables: map[string]resources.VariableDefinition{"V": {Mode: "http", Source: source("/token")}, "UNUSED": {Mode: "http", Source: source("/unused")}, "ALIAS": {Mode: "literal", Value: "${V}"}}}}}
	code := 200
	b := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "POST", Path: "/items/${ALIAS}", Query: []catalog.KeyValue{{Name: "v", Value: "${V}"}}, BodyMode: "json", Body: `{"v":"${ALIAS}"}`}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	planned, prepared, err := res.Plan(b)
	if err != nil || !prepared.Deferred || sourceCalls.Load() != 0 {
		t.Fatal("not a pure plan", err)
	}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	for _, scope := range []string{"one", "one", "two"} {
		state, result := runner.Execute(context.Background(), scope, planned, prepared)
		if state != "completed" || result.Comparison.Status != "match" || !result.Redacted {
			t.Fatal("execution", state, result.Error, result.Comparison)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "private") || strings.Contains(string(raw), url.QueryEscape(value)) {
			t.Fatal("HTTP value leaked")
		}
		if len(result.VariableSources) != 1 {
			t.Fatal("wrong source selection")
		}
	}
	if sourceCalls.Load() != 2 || mainCalls.Load() != 3 {
		t.Fatal("cache isolation", sourceCalls.Load(), mainCalls.Load())
	}
	def := res.Resource.Config.Variables["V"]
	def.Source.FetchMode = "per_attempt"
	res.Resource.Config.Variables["V"] = def
	for i := 0; i < 2; i++ {
		if state, _ := runner.Execute(context.Background(), "one", b, res); state != "completed" {
			t.Fatal("per-attempt execution")
		}
	}
	if sourceCalls.Load() != 4 || mainCalls.Load() != 5 {
		t.Fatal("per-attempt value reused")
	}
	b.HTTP.Query = append(b.HTTP.Query, catalog.KeyValue{Name: "raw", Value: "1"})
	if state, result := runner.Execute(context.Background(), "one", b, res); state != "completed" || strings.Contains(result.Body, "quoted") || !result.Redacted {
		t.Fatal("escaped value leaked in a non-JSON response")
	}
}

func TestResourceHTTPVariableErrorsAndRegistrationGate(t *testing.T) {
	var sources, mains atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/source" {
			sources.Add(1)
			w.Write([]byte(`{"n":null}`))
			return
		}
		mains.Add(1)
	}))
	defer server.Close()
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: server.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Variables: map[string]resources.VariableDefinition{"N": {Mode: "http", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: server.URL + "/source", BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/n", FetchMode: "per_run"}}}}}}
	code := 200
	b := catalog.Block{Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/${N}", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	for i := 0; i < 2; i++ {
		state, result := runner.Execute(context.Background(), "scope", b, res)
		if state != "error" || result.ErrorCode != "variable_source_failed" || result.MainRequestStarted == nil || *result.MainRequestStarted {
			t.Fatal("missing source error", state, result)
		}
	}
	if sources.Load() != 2 || mains.Load() != 0 {
		t.Fatal("error cached or main invoked")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Execute(cancelled, "scope", b, res)
	if sources.Load() != 2 {
		t.Fatal("cancelled source invoked")
	}
}

func TestResourceVariablePreflightAndSQLPlanning(t *testing.T) {
	makeSource := func(u string) *catalog.HTTPHeaderSource {
		return &catalog.HTTPHeaderSource{Method: "GET", URL: u, BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/n", FetchMode: "per_attempt"}
	}
	cfg := resources.Config{Variables: map[string]resources.VariableDefinition{"N": {Mode: "http", Source: makeSource("http://127.0.0.1:1/n")}}}
	res := resources.Resolved{Resource: resources.Resource{Config: cfg}}
	b := catalog.Block{Kind: "postgresql", Resource: "DB", SQL: &catalog.SQLRequest{Mode: "rows", Query: "SELECT $1::numeric", Parameters: []catalog.TypedValue{{Type: "decimal", Value: "${N}"}}}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "n", Type: "numeric"}}}}
	if _, p, err := res.Plan(b); err != nil || !p.Deferred {
		t.Fatal("numeric placeholder eagerly validated", err)
	}
	b.Expected.Headers = []catalog.KeyValue{{Name: "X-Value", Value: "${N}"}}
	if _, _, err := res.Plan(b); err == nil {
		t.Fatal("dynamic expectation accepted")
	}
	b.Expected.Headers = nil
	b.SQL.Query = "SELECT ${N}"
	if _, _, err := res.Plan(b); err == nil {
		t.Fatal("dynamic SQL text accepted")
	}
	b.SQL.Query = "SELECT $1::numeric"
	cfg.Variables["N"] = resources.VariableDefinition{Mode: "http", Source: makeSource("http://127.0.0.1:1/?q=${N}")}
	if _, _, err := res.Plan(b); err == nil {
		t.Fatal("dynamic source dependency accepted")
	}
	delete(cfg.Variables, "N")
	if _, _, err := res.Plan(b); err == nil {
		t.Fatal("unknown variable accepted")
	}
}

func TestResourceVariableScalarsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		valid            bool
	}{
		{"number", `{"n":9007199254740993123.125}`, "9007199254740993123.125", 200, true},
		{"boolean", `{"n":true}`, "true", 200, true},
		{"empty", `{"n":""}`, "", 200, true},
		{"null", `{"n":null}`, "", 200, false}, {"object", `{"n":{}}`, "", 200, false}, {"array", `{"n":[]}`, "", 200, false},
		{"missing", `{}`, "", 200, false}, {"status", `{"n":"x"}`, "", 401, false},
		{"value-limit", `{"n":"` + strings.Repeat("x", (64<<10)+1) + `"}`, "", 200, false},
		{"body-limit", `{"n":"x","padding":"` + strings.Repeat("y", (5<<20)+1) + `"}`, "", 200, false},
		{"utf8", "{\"n\":\"\xff\"}", "", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer source.Close()
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("v") != tc.want {
					t.Error("scalar changed")
				}
				json.NewEncoder(w).Encode(map[string]string{"value": tc.want})
			}))
			defer target.Close()
			res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Variables: map[string]resources.VariableDefinition{"N": {Mode: "http", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "none", JSONPointer: "/n", FetchMode: "per_attempt"}}}}}}
			code := 200
			b := catalog.Block{Kind: "http", Resource: "TARGET", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", Query: []catalog.KeyValue{{Name: "v", Value: "${N}"}}, BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
			runner := NewRunner()
			defer runner.ClearHeaderScopes()
			state, result := runner.Execute(context.Background(), catalog.NewID(), b, res)
			if tc.valid {
				if state != "completed" || calls.Load() != 1 {
					t.Fatal("valid scalar", state, result.Error)
				}
				if tc.want != "" && strings.Contains(result.Body, tc.want) {
					t.Fatal("scalar leaked in response")
				}
			} else if state != "error" || calls.Load() != 0 || result.ErrorCode != "variable_source_failed" {
				t.Fatal("invalid scalar reached target", state, result.ErrorCode)
			}
		})
	}
}

func TestInvalidVariableBindingCanFetchAgain(t *testing.T) {
	var sources, mains atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := "fixed"
		if sources.Add(1) == 1 {
			value = "invalid\r\nheader"
		}
		json.NewEncoder(w).Encode(map[string]string{"v": value})
	}))
	defer source.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mains.Add(1)
		if r.Header.Get("X-Value") != "fixed" {
			t.Error("wrong variable")
		}
	}))
	defer target.Close()
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: target.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Variables: map[string]resources.VariableDefinition{"V": {Mode: "http", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: source.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, BodyMode: "none", JSONPointer: "/v", FetchMode: "per_run"}}}}}}
	code := 200
	b := catalog.Block{Kind: "http", Resource: "TARGET", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none", Headers: []catalog.RequestHeader{{Name: "X-Value", Value: "${V}"}}}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	if state, _ := runner.Execute(context.Background(), "run", b, res); state != "error" || mains.Load() != 0 {
		t.Fatal("invalid header reached target")
	}
	if state, _ := runner.Execute(context.Background(), "run", b, res); state != "completed" || sources.Load() != 2 || mains.Load() != 1 {
		t.Fatal("invalid binding was cached")
	}
}
