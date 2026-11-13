package httpapi

import (
	"net/http"

	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/service"
)

type CallLogPage struct {
	Items      []execution.LogSummary `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

func (s *Server) registerDiagnostics(engine *service.Engine) {
	s.register("GET /api/v1/call-logs", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		q := r.URL.Query()
		return engine.ListLogs(r.Context(), execution.LogFilter{From: q.Get("from"), To: q.Get("to"), Protocol: q.Get("protocol"), Origin: q.Get("origin"), Stage: q.Get("stage"), State: q.Get("state"), EnvironmentID: q.Get("environmentId"), ResourceID: q.Get("resourceId"), TestCaseID: q.Get("testCaseId"), RunID: q.Get("runId"), AttemptID: q.Get("attemptId"), ProbeID: q.Get("probeId"), Limit: limit, Cursor: cursor})
	})
	s.register("GET /api/v1/call-logs/{id}", func(r *http.Request) (any, error) { return engine.GetLog(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/http-variable-probes", func(r *http.Request) (any, error) {
		var body execution.ProbeInput
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		if key := r.Header.Get("Idempotency-Key"); key != "" {
			if body.IdempotencyKey != "" && body.IdempotencyKey != key {
				return nil, problem.Invalid("idempotencyKey", "Ключ в заголовке отличается от тела")
			}
			body.IdempotencyKey = key
		}
		return engine.StartProbe(r.Context(), body)
	})
	s.register("GET /api/v1/http-variable-probes/{id}", func(r *http.Request) (any, error) { return engine.GetProbe(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/http-variable-probes/{id}/cancel", func(r *http.Request) (any, error) { return engine.CancelProbe(r.Context(), r.PathValue("id")) })
}
