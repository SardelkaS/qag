package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"qag/internal/catalog"
	"qag/internal/problem"
)

type operation func(*http.Request) (any, error)

func (s *Server) register(pattern string, apply operation) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := apply(r.WithContext(ctx))
		if err != nil {
			s.operationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}
func (s *Server) operationError(w http.ResponseWriter, r *http.Request, err error) {
	var issue *problem.Error
	if !errors.As(err, &issue) {
		writeError(w, r, 500, "internal_error", "Не удалось выполнить действие")
		return
	}
	status := 400
	switch issue.Code {
	case "not_found":
		status = 404
	case "conflict":
		status = 409
	case "storage_unavailable":
		status = 503
	}
	writeJSON(w, status, Error{Code: issue.Code, Message: issue.Message, FieldErrors: issue.Fields, RequestID: w.Header().Get("X-Request-ID")})
}
func readJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return problem.Invalid("body", "Некорректный JSON или неизвестные поля запроса")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return problem.Invalid("body", "Ожидается один JSON-документ")
	}
	return nil
}
func pageOptions(r *http.Request) (int, string, error) {
	limit := 50
	if text := r.URL.Query().Get("limit"); text != "" {
		var err error
		limit, err = strconv.Atoi(text)
		if err != nil || limit < 1 || limit > 100 {
			return 0, "", problem.Invalid("limit", "Размер страницы должен быть от 1 до 100")
		}
	}
	return limit, r.URL.Query().Get("cursor"), nil
}

func (s *Server) RegisterCatalog(repo *catalog.Repository) {
	s.register("GET /api/v1/folders", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		archived := false
		if text := r.URL.Query().Get("archived"); text != "" {
			archived, err = strconv.ParseBool(text)
			if err != nil {
				return nil, problem.Invalid("archived", "Ожидается true или false")
			}
		}
		return repo.ListFolders(r.Context(), archived, limit, cursor)
	})
	s.register("POST /api/v1/folders", func(r *http.Request) (any, error) {
		var body CreateName
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.CreateFolder(r.Context(), body.Name)
	})
	s.register("PATCH /api/v1/folders/{id}", func(r *http.Request) (any, error) {
		var body ArchivePatch
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.PatchFolder(r.Context(), r.PathValue("id"), body.Name, body.Archived)
	})
	s.register("GET /api/v1/folders/{id}/test-cases", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		return repo.ListCases(r.Context(), r.PathValue("id"), limit, cursor)
	})
	s.register("POST /api/v1/folders/{id}/test-cases", func(r *http.Request) (any, error) {
		var body CreateName
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.CreateCase(r.Context(), r.PathValue("id"), body.Name)
	})
	s.register("GET /api/v1/test-cases/{id}", func(r *http.Request) (any, error) { return repo.GetCase(r.Context(), r.PathValue("id")) })
	s.register("PUT /api/v1/test-cases/{id}", func(r *http.Request) (any, error) {
		var body TestCaseWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveCase(r.Context(), r.PathValue("id"), body.Name, body.Revision, body.Steps)
	})
	s.register("PATCH /api/v1/test-cases/{id}", func(r *http.Request) (any, error) {
		var body ArchivePatch
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.PatchCase(r.Context(), r.PathValue("id"), body.Name, body.Archived)
	})
}
