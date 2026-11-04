package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	appruntime "github.com/SardelkaS/qag/internal/runtime"
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

func TestContainerPublishedPortKeepsBrowserGuards(t *testing.T) {
	s, err := New(appruntime.Config{Address: "0.0.0.0:8080", PublicOrigin: "http://127.0.0.1:18088"}, fstest.MapFS{}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	effects := 0
	s.mux.HandleFunc("POST /api/v1/effect", func(w http.ResponseWriter, r *http.Request) { effects++; w.WriteHeader(204) })
	for _, tc := range []struct {
		host, origin, token, fetch string
		want                       int
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080", s.token, "", 204},
		{"localhost:18088", "http://localhost:18088", s.token, "same-origin", 204},
		{"[::1]:18088", "http://[::1]:18088", s.token, "", 204},
		{"evil.invalid:18088", "http://localhost:18088", s.token, "", 403},
		{"localhost:18088", "http://evil.invalid:18088", s.token, "", 403},
		{"localhost:18088", "http://localhost:18088", "bad", "", 403},
		{"localhost:18088", "http://localhost:18088", s.token, "cross-site", 403},
		{"localhost:18089", "http://localhost:18089", s.token, "", 403},
	} {
		before := effects
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/v1/effect", strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-QAG-CSRF", tc.token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", tc.fetch)
		r.Header.Set("X-Forwarded-Host", "localhost:18088")
		r.Header.Set("X-Forwarded-Proto", "http")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.want || (tc.want == 403 && effects != before) {
			t.Fatalf("%s %s: %d effects %d", tc.host, tc.origin, w.Code, effects)
		}
	}
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

func TestFrontendReloadAndVersionedIcons(t *testing.T) {
	assets := fstest.MapFS{"index.html": {Data: []byte("<html>qag</html>")}}
	for _, name := range []string{"qag-32.png", "favicon.ico"} {
		data, err := os.ReadFile("../../web/public/" + name)
		if err != nil {
			t.Fatal(err)
		}
		assets[name] = &fstest.MapFile{Data: data}
	}
	s, err := New(appruntime.Config{Address: "127.0.0.1:8080"}, assets, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/", "/index.html", "/cases/example"} {
		want := http.StatusOK
		if route == "/index.html" {
			want = http.StatusMovedPermanently
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080"+route, nil))
		if w.Code != want || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("HTML cannot revalidate: %s %d %v", route, w.Code, w.Header())
		}
	}
	for _, name := range []string{"qag-32.png", "favicon.ico"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080/"+name+"?v=content-version", nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/") || !bytes.Equal(w.Body.Bytes(), assets[name].Data) {
			t.Fatalf("Invalid favicon: %s %d %v", name, w.Code, w.Header())
		}
		if name == "qag-32.png" {
			image, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
			if err != nil || image.Bounds().Dx() != 32 || image.Bounds().Dy() != 32 {
				t.Fatal("Browser PNG must decode as 32x32", err)
			}
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "img-src 'self' data:") {
			t.Fatal("Image CSP changed")
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080/missing-icon.png", nil))
	if w.Code != 404 {
		t.Fatalf("Missing icon returned %d instead of 404", w.Code)
	}
}
