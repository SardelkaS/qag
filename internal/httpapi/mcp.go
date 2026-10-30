package httpapi

import (
	"net/http"
)

// RegisterMCP shares the application listener; MCP is open independently of
// browser session, Host and Origin checks applied to the UI API.
func (s *Server) RegisterMCP(handler http.Handler) {
	s.mcpHandler = handler
}
func (s *Server) serveMCP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	headers := r.Header.Get("Access-Control-Request-Headers")
	if headers == "" {
		headers = "Content-Type, Authorization, MCP-Protocol-Version, MCP-Session-Id, Last-Event-ID"
	}
	w.Header().Set("Access-Control-Allow-Headers", headers)
	w.Header().Set("Access-Control-Expose-Headers", "MCP-Protocol-Version, MCP-Session-Id")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.mcpHandler == nil {
		http.NotFound(w, r)
		return
	}
	s.mcpHandler.ServeHTTP(w, r)
}
