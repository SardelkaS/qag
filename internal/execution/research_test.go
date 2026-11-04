package execution

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestResearchPreservesActualFailuresLimitsAndHeaderSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"token":"research-derived-secret"}`)
		case "/slow":
			<-r.Context().Done()
		case "/large":
			fmt.Fprint(w, strings.Repeat("x", MaxResponseBytes+1))
		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"token":%q,"n":9007199254740993.125}`, r.Header.Get("Authorization"))
		}
	}))
	defer server.Close()
	res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: resources.Config{BaseURL: server.URL, TLS: resources.TLSConfig{Mode: "plaintext"}}}}
	block := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/missing", BodyMode: "none", Headers: []catalog.RequestHeader{{ID: catalog.NewID(), Name: "Authorization", Source: &catalog.HTTPHeaderSource{Method: "GET", URL: server.URL + "/token", BodyMode: "none", TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", FetchMode: "per_attempt"}}}}, Expected: catalog.Expectation{BodyMode: "json", Body: `{"legacy":"never compare"}`}}
	runner := NewRunner()
	defer runner.ClearHeaderScopes()
	for _, scenario := range []struct {
		name, path, state string
		truncated         bool
	}{
		{"negative code", "/missing", "completed", false},
		{"limit", "/large", "completed", true},
		{"timeout", "/slow", "timed_out", false},
		{"cancel", "/slow", "cancelled", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			b := block
			h := *block.HTTP
			h.Path = scenario.path
			b.HTTP = &h
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			if scenario.state == "cancelled" {
				cancel()
			}
			defer cancel()
			prepared, resolved, err := res.PrepareRequest(b)
			if err != nil {
				t.Fatal(err)
			}
			state, result := runner.ExecuteResearch(ctx, catalog.NewID(), prepared, resolved)
			if state != scenario.state || result.Truncated != scenario.truncated || result.Comparison.Status != "not_compared" || len(result.Comparison.Differences) != 0 || result.Comparison.Reason != ResearchComparison().Reason {
				t.Fatalf("research lost actual state: %s %+v", state, result)
			}
			if scenario.path == "/missing" {
				if result.StatusCode == nil || *result.StatusCode != 404 || !result.Redacted || strings.Contains(result.Body, "research-derived-secret") || !strings.Contains(result.Body, "9007199254740993.125") || len(result.HeaderSources) != 1 || result.MainRequestStarted == nil || !*result.MainRequestStarted {
					t.Fatalf("actual response/source/redaction lost: %+v", result)
				}
			}
			if state != "completed" && result.Error == "" {
				t.Fatal("actual error missing")
			}
		})
	}
}
