package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestEnvironmentFormSourcesShareRunAcrossResources(t *testing.T) {
	secret := "a+b&c= d% проверка"
	rawToken := "private-token-${OPAQUE}"
	var sources, mains atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			sources.Add(1)
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("wrong content type")
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "secret=a%2Bb%26c%3D+d%25+") {
				t.Error("form not encoded once")
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			if err := r.ParseForm(); err != nil || r.PostForm.Get("secret") != secret || strings.Join(r.PostForm["scope"], ",") != "one,two" || r.PostForm.Get("empty") != "" {
				t.Error("form values lost")
			}
			if r.URL.RawQuery != "q=separate" || r.Header.Get("X-Target") != "" {
				t.Error("source inherited resource")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": rawToken})
			return
		}
		mains.Add(1)
		if r.Header.Get("Authorization") != "Bearer ["+rawToken+"]!" || r.URL.Query().Get("v") != "["+rawToken+"]" {
			t.Error("wrong substitution or affixes")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"raw": rawToken, "full": "[" + rawToken + "]"})
	}))
	defer server.Close()
	source := catalog.HTTPHeaderSource{Method: "POST", URL: server.URL + "/token?q=separate", BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "${AUTH_SECRET}"}, {Name: "scope", Value: "one"}, {Name: "scope", Value: "two"}, {Name: "empty"}}, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "[", Suffix: "]", FetchMode: "per_run"}
	res := resources.Resolved{EnvironmentID: catalog.NewID(), EnvironmentVersionID: catalog.NewID(), EnvironmentHTTPVariables: map[string]catalog.HTTPHeaderSource{"AUTH": source}, EnvironmentSecrets: map[string]string{"AUTH_SECRET": secret}, Secrets: map[string]string{"AUTH_SECRET": "resource-value-must-not-leak-into-source"}, Resource: resources.Resource{ID: catalog.NewID(), VersionID: catalog.NewID(), Config: resources.Config{BaseURL: server.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${AUTH}!"}, {Name: "X-Target", Value: "resource"}}}}}
	code := 200
	b := catalog.Block{Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/target", Query: []catalog.KeyValue{{Name: "v", Value: "${AUTH}"}}, BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	for i, scope := range []string{"run", "run", "another-run"} {
		res.Resource.VersionID = catalog.NewID()
		planned, prepared, err := res.Plan(b)
		if err != nil || !prepared.Deferred {
			t.Fatal("not a pure deferred plan", err)
		}
		state, result := runner.Execute(context.Background(), scope, planned, prepared)
		if state != "completed" || result.Comparison.Status != "match" {
			t.Fatal(state, result.Error)
		}
		if len(result.VariableSources) != 1 || result.VariableSources[0].Scope != "environment" {
			t.Fatal("missing owner trace")
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "private-token") {
			t.Fatal("raw token leaked")
		}
		if i == 1 && result.VariableSources[0].State != "reused" {
			t.Fatal("environment value refetched per resource")
		}
	}
	if sources.Load() != 2 || mains.Load() != 3 {
		t.Fatal("wrong scope cache", sources.Load(), mains.Load())
	}
	source.FetchMode = "per_attempt"
	res.EnvironmentHTTPVariables["AUTH"] = source
	for i := 0; i < 2; i++ {
		if state, result := runner.Execute(context.Background(), "run", b, res); state != "completed" {
			t.Fatal(state, result.Error)
		}
	}
	if sources.Load() != 4 {
		t.Fatal("per-attempt source reused")
	}
}

func TestHeaderSourceFormAndBothAffixes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/source" {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "sensitive-header-token"})
			return
		}
		if r.Header.Get("Authorization") != "Token [sensitive-header-token]" {
			t.Error("header suffix lost")
		}
		_, _ = w.Write([]byte("sensitive-header-token Token [sensitive-header-token]"))
	}))
	defer server.Close()
	source := &catalog.HTTPHeaderSource{Method: "POST", URL: server.URL + "/source", BodyMode: "form", Form: []catalog.KeyValue{{Name: "x", Value: "a+b"}}, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "Token [", Suffix: "]", FetchMode: "per_run"}
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: server.URL, TLS: catalog.TLSConfig{Mode: "plaintext"}, Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "Authorization", Source: source}}}}}
	code := 200
	b := catalog.Block{Kind: "http", Resource: "TARGET", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/target", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	for i := 0; i < 2; i++ {
		state, result := runner.Execute(context.Background(), "scope", b, res)
		if state != "completed" || strings.Contains(result.Body, "sensitive-header-token") {
			t.Fatal(state, result.Error, result.Body)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("header source refetched")
	}
}
