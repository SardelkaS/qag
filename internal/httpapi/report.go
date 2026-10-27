package httpapi

import (
	"net/http"
	"qag/internal/clipboard"
	"qag/internal/problem"
	"qag/internal/reporting"
	"qag/internal/service"
	"strconv"
)

func (s *Server) registerReports(engine *service.Engine) {
	cache := reporting.NewCache()
	s.register("GET /api/v1/runs/{id}/report", func(r *http.Request) (any, error) {
		include := true
		if value := r.URL.Query().Get("includeExpected"); value != "" {
			var err error
			include, err = strconv.ParseBool(value)
			if err != nil {
				return nil, problem.Invalid("includeExpected", "Ожидается true или false")
			}
		}
		report, err := engine.Runs.ReportSnapshot(r.Context(), r.PathValue("id"), include)
		if err != nil {
			return nil, err
		}
		report.ClipboardNative = clipboard.Available()
		if err = cache.Put(&report); err != nil {
			return nil, err
		}
		return report, nil
	})
	s.register("POST /api/v1/runs/{id}/report/clipboard", func(r *http.Request) (any, error) {
		var body reporting.ClipboardWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		report, err := cache.Get(body.SnapshotID, body.Hash, r.PathValue("id"), body.IncludeExpected)
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
