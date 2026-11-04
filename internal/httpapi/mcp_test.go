package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMCPOpenAccessAndBrowserSessionIsolation(t *testing.T) {
	for _, port := range []string{"8080", "18088"} {
		s, err := New(appruntime.Config{Address: "0.0.0.0:8080", PublicOrigin: "http://127.0.0.1:" + port}, fstest.MapFS{"index.html": {Data: []byte("SPA")}}, func(context.Context) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		s.RegisterMCP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) }))
		for _, tc := range []struct{ host, origin, authorization string }{{"127.0.0.1:" + port, "", ""}, {"client.invalid:" + port, "https://client.invalid", ""}, {"127.0.0.1:" + port, "https://client.invalid", "Bearer ignored"}} {
			for _, method := range []string{"POST", "GET", "DELETE"} {
				r := httptest.NewRequest(method, "http://"+tc.host+"/mcp", strings.NewReader("{}"))
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Authorization", tc.authorization)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
				w := httptest.NewRecorder()
				before := calls
				s.Handler().ServeHTTP(w, r)
				if w.Code != 204 || calls != before+1 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("WWW-Authenticate") != "" {
					t.Fatalf("MCP access: %s %d", method, w.Code)
				}
			}
		}
		r := httptest.NewRequest("OPTIONS", "http://client.invalid/mcp", nil)
		r.Header.Set("Origin", "https://client.invalid")
		r.Header.Set("Access-Control-Request-Headers", "content-type,mcp-protocol-version,x-client-header")
		w := httptest.NewRecorder()
		before := calls
		s.Handler().ServeHTTP(w, r)
		if w.Code != 204 || calls != before || w.Header().Get("Access-Control-Allow-Headers") != "content-type,mcp-protocol-version,x-client-header" {
			t.Fatal("CORS preflight rejected")
		}
		s.mux.HandleFunc("POST /api/v1/mcp-guard", func(http.ResponseWriter, *http.Request) { t.Error("MCP openness bypassed UI session") })
		for _, origin := range []string{"http://127.0.0.1:" + port, "https://client.invalid"} {
			r := httptest.NewRequest("POST", "http://127.0.0.1:"+port+"/api/v1/mcp-guard", strings.NewReader("{}"))
			r.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("UI browser protections changed")
			}
		}
	}
}
