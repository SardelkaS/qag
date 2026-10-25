package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	appruntime "qag/internal/runtime"
)

func testServer(t *testing.T, ping func(context.Context) error) *Server {
	t.Helper()
	s, err := New(appruntime.Config{Address: "127.0.0.1:8080"}, fstest.MapFS{"index.html": {Data: []byte("<html>qag</html>")}}, ping)
	if err != nil {
		t.Fatal(err)
	}
	s.mux.HandleFunc("POST /api/v1/test-command", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]bool{"executed": true}) })
	return s
}

func TestCommandRejectsUntrustedBrowserAndInvalidSession(t *testing.T) {
	s := testServer(t, func(context.Context) error { return nil })
	for _, tc := range []struct {
		name, host, origin, token, content string
		want                               int
	}{
		{"foreign origin", "127.0.0.1:8080", "https://evil.example", s.token, "application/json", 403},
		{"rebinding host", "evil.example:8080", "http://evil.example:8080", s.token, "application/json", 403},
		{"no origin", "127.0.0.1:8080", "", s.token, "application/json", 403},
		{"wrong token", "127.0.0.1:8080", "http://127.0.0.1:8080", "wrong", "application/json", 403},
		{"form submission", "127.0.0.1:8080", "http://127.0.0.1:8080", s.token, "text/plain", 415},
		{"local command", "127.0.0.1:8080", "http://127.0.0.1:8080", s.token, "application/json", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://"+tc.host+"/api/v1/test-command", strings.NewReader("{}"))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-QAG-CSRF", tc.token)
			r.Header.Set("Content-Type", tc.content)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestReadinessDoesNotExposeStorageError(t *testing.T) {
	s := testServer(t, func(context.Context) error { return errors.New("postgres://user:sentinel-password@localhost/qag") })
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080/api/v1/health", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "sentinel-password") {
		t.Fatalf("unsafe readiness: %d %s", w.Code, w.Body.String())
	}
	var apiError Error
	if err := json.Unmarshal(w.Body.Bytes(), &apiError); err != nil || apiError.RequestID == "" {
		t.Fatal("missing structured error")
	}
}

func TestEmbeddedFrontendAndSession(t *testing.T) {
	s := testServer(t, func(context.Context) error { return nil })
	for _, path := range []string{"/", "/cases/example", "/api/v1/session", "/api/v1/health"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
