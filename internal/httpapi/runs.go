package httpapi

import (
	"net/http"
	"qag/internal/problem"
	"qag/internal/reporting"
	"qag/internal/service"
	"strconv"
)

func (s *Server) RegisterRuns(engine *service.Engine, reports ...*reporting.Service) {
	s.registerReports(engine, reports...)
	s.register("POST /api/v1/test-cases/{id}/runs", func(r *http.Request) (any, error) {
		var body RunStart
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.StartRun(r.Context(), r.PathValue("id"), body.EnvironmentID, body.Revision, r.Header.Get("Idempotency-Key"), body.ExecuteAll)
	})
	s.register("GET /api/v1/test-cases/{id}/runs", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		var retained *bool
		if text := r.URL.Query().Get("retained"); text != "" {
			value, err := strconv.ParseBool(text)
			if err != nil {
				return nil, problem.Invalid("retained", "Ожидается true или false")
			}
			retained = &value
		}
		active := false
		if value := r.URL.Query().Get("active"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, problem.Invalid("active", "Ожидается true или false")
			}
			active = parsed
		}
		return engine.Runs.List(r.Context(), r.PathValue("id"), retained, limit, cursor, active)
	})
	s.register("GET /api/v1/runs/{id}", func(r *http.Request) (any, error) { return engine.GetRun(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/runs/{id}/batches", func(r *http.Request) (any, error) {
		return engine.StartBatch(r.Context(), r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	})
	s.register("POST /api/v1/runs/{id}/blocks/{blockId}/variants", func(r *http.Request) (any, error) {
		var body VariantWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.Runs.SaveVariant(r.Context(), r.PathValue("id"), r.PathValue("blockId"), "", body.Name, 0, nil)
	})
	s.register("PATCH /api/v1/runs/{id}/variants/{variantId}", func(r *http.Request) (any, error) {
		var body VariantWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		if body.Request == nil {
			return nil, problem.Invalid("request", "Нужны параметры варианта")
		}
		return engine.Runs.SaveVariant(r.Context(), r.PathValue("id"), "", r.PathValue("variantId"), body.Name, body.Revision, body.Request)
	})
	s.register("POST /api/v1/runs/{id}/variants/{variantId}/attempts", func(r *http.Request) (any, error) {
		var body AttemptStart
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.VariantAttempt(r.Context(), r.PathValue("id"), r.PathValue("variantId"), body.Revision, r.Header.Get("Idempotency-Key"))
	})
	s.register("POST /api/v1/runs/{id}/steps/pass-all", func(r *http.Request) (any, error) {
		var body StepsPassAll
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.Runs.PassAll(r.Context(), r.PathValue("id"), body.Steps)
	})
	s.register("PATCH /api/v1/runs/{id}/steps/{stepId}", func(r *http.Request) (any, error) {
		var body StepWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.Runs.UpdateStep(r.Context(), r.PathValue("id"), r.PathValue("stepId"), body.Status, body.Comment, body.Revision)
	})
	s.register("POST /api/v1/runs/{id}/finish", func(r *http.Request) (any, error) { return engine.FinishRun(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/runs/{id}/cancel", func(r *http.Request) (any, error) { return engine.CancelRun(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/runs/{id}/save", func(r *http.Request) (any, error) { return engine.Runs.Save(r.Context(), r.PathValue("id")) })
	s.register("DELETE /api/v1/runs/{id}", func(r *http.Request) (any, error) {
		err := engine.Runs.Delete(r.Context(), r.PathValue("id"))
		return map[string]bool{"deleted": err == nil}, err
	})
	s.register("POST /api/v1/test-cases/{id}/blocks/{blockId}/attempts", func(r *http.Request) (any, error) {
		var body AttemptStart
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return engine.EditorAttempt(r.Context(), r.PathValue("id"), r.PathValue("blockId"), body.EnvironmentID, body.Revision, r.Header.Get("Idempotency-Key"))
	})
	s.register("POST /api/v1/runs/{id}/blocks/{blockId}/attempts", func(r *http.Request) (any, error) {
		return engine.RunAttempt(r.Context(), r.PathValue("id"), r.PathValue("blockId"), r.Header.Get("Idempotency-Key"))
	})
	s.register("GET /api/v1/attempts/{id}", func(r *http.Request) (any, error) { return engine.GetAttempt(r.Context(), r.PathValue("id")) })
	s.register("POST /api/v1/attempts/{id}/cancel", func(r *http.Request) (any, error) { return engine.CancelAttempt(r.Context(), r.PathValue("id")) })
}
