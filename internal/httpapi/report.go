package httpapi

import (
	"net/http"
	"qag/internal/clipboard"
	"qag/internal/problem"
	"qag/internal/reporting"
	"qag/internal/service"
	"strconv"
)

func (s *Server) registerReports(engine *service.Engine, provided ...*reporting.Service) {
	reports := reporting.NewService(engine.Runs, engine.Catalog, nil, clipboard.Available())
	if len(provided) > 0 && provided[0] != nil {
		reports = provided[0]
	}
	s.register("GET /api/v1/runs/{id}/report", func(r *http.Request) (any, error) {
		include := true
		if value := r.URL.Query().Get("includeExpected"); value != "" {
			var err error
			include, err = strconv.ParseBool(value)
			if err != nil {
				return nil, problem.Invalid("includeExpected", "Ожидается true или false")
			}
		}
		report, err := reports.Prepare(r.Context(), r.PathValue("id"), include)
		if err != nil {
			return nil, err
		}
		return report, nil
	})
	s.register("POST /api/v1/runs/{id}/report/clipboard", func(r *http.Request) (any, error) {
		var body reporting.ClipboardWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		report, err := reports.Cached(reporting.Reference{SnapshotID: body.SnapshotID, Hash: body.Hash, RunID: r.PathValue("id"), IncludeExpected: body.IncludeExpected})
		if err != nil {
			return nil, err
		}
		content, err := reporting.Rich(report, body.Parts)
		if err != nil {
			return nil, err
		}
		if err = clipboard.Write(content); err != nil {
			return nil, problem.Invalid("clipboard", err.Error())
		}
		return reporting.ClipboardResult{Copied: true, Native: true}, nil
	})
}
