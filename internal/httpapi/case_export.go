package httpapi

import (
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/reporting"
	"net/http"
	"strconv"
)

func (s *Server) registerCaseExport(repo ports.Catalog) {
	s.register("GET /api/v1/test-cases/{id}/export", func(r *http.Request) (any, error) {
		revision, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
		if err != nil || revision < 1 {
			return nil, problem.Invalid("revision", "Нужна сохранённая версия сценария")
		}
		format := r.URL.Query().Get("format")
		if format != "" && format != "jira-wiki" {
			return nil, problem.Invalid("format", "Выберите jira-wiki")
		}
		include := true
		if v := r.URL.Query().Get("includeExpected"); v != "" {
			include, err = strconv.ParseBool(v)
			if err != nil {
				return nil, problem.Invalid("includeExpected", "Ожидается true или false")
			}
		}
		return reporting.ExportCase(r.Context(), repo, r.PathValue("id"), revision, include)
	})
}
