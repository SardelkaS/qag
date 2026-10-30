package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	appruntime "qag/internal/runtime"
)

type Error struct {
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	RequestID   string            `json:"requestId"`
}

type Server struct {
	mux        *http.ServeMux
	token      string
	hosts      map[string]bool
	origins    map[string]bool
	mcpHandler http.Handler
}

func New(config appruntime.Config, assets fs.FS, ping func(context.Context) error) (*Server, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, fmt.Errorf("не удалось создать локальную сессию")
	}
	_, port, err := net.SplitHostPort(config.Address)
	if err != nil {
		return nil, fmt.Errorf("некорректный адрес сервера")
	}
	s := &Server{mux: http.NewServeMux(), token: hex.EncodeToString(token), hosts: map[string]bool{}, origins: map[string]bool{}}
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		authority := net.JoinHostPort(host, port)
		s.hosts[authority] = true
		s.origins["http://"+authority] = true
	}
	if config.PublicOrigin != "" {
		public, err := url.Parse(config.PublicOrigin)
		if err != nil {
			return nil, fmt.Errorf("некорректный public origin")
		}
		s.hosts[public.Host] = true
		s.origins[config.PublicOrigin] = true
		publicPort := public.Port()
		if publicPort == "" {
			publicPort = "80"
		}
		for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
			authority := net.JoinHostPort(host, publicPort)
			s.hosts[authority] = true
			s.origins["http://"+authority] = true
			if publicPort == "80" {
				authority = host
				if host == "::1" {
					authority = "[::1]"
				}
				s.hosts[authority] = true
				s.origins["http://"+authority] = true
			}
		}
	}
	if config.DevOrigin != "" {
		origin, err := url.Parse(config.DevOrigin)
		if err != nil {
			return nil, fmt.Errorf("некорректный dev origin")
		}
		s.hosts[origin.Host] = true
		s.origins[config.DevOrigin] = true
	}
	s.mux.HandleFunc("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"csrfToken": s.token})
	})
	s.mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := ping(ctx); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, "storage_unavailable", "PostgreSQL приложения недоступна")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "Маршрут не найден")
	})
	files := http.FileServer(http.FS(assets))
	s.mux.HandleFunc("/mcp", s.serveMCP)
	s.mux.HandleFunc("/mcp/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if name == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if _, err := fs.Stat(assets, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			index, err := fs.ReadFile(assets, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			if r.Method != http.MethodHead {
				_, _ = w.Write(index)
			}
			return
		}
		files.ServeHTTP(w, r)
	})
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := make([]byte, 12)
		if _, err := rand.Read(id); err != nil {
			http.Error(w, "session unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Request-ID", hex.EncodeToString(id))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.URL.Path == "/mcp" {
			s.mux.ServeHTTP(w, r)
			return
		}
		if !s.hosts[strings.ToLower(r.Host)] {
			writeError(w, r, http.StatusForbidden, "invalid_host", "Недопустимый адрес локального приложения")
			return
		}
		origin := r.Header.Get("Origin")
		if (origin != "" && !s.origins[origin]) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, r, http.StatusForbidden, "invalid_origin", "Команды с постороннего сайта запрещены")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if !s.origins[origin] || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-QAG-CSRF")), []byte(s.token)) != 1 {
					writeError(w, r, http.StatusForbidden, "invalid_session", "Обновите локальную сессию приложения")
					return
				}
				mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || mediaType != "application/json" {
					writeError(w, r, http.StatusUnsupportedMediaType, "invalid_content_type", "Ожидается application/json")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, _ *http.Request, status int, code, message string) {
	writeJSON(w, status, Error{Code: code, Message: message, RequestID: w.Header().Get("X-Request-ID")})
}
