package httpapi

import (
	"net/http"
	"qag/internal/resources"
)

func (s *Server) RegisterResources(repo *resources.Repository) {
	s.register("GET /api/v1/environments", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		return repo.ListEnvironments(r.Context(), limit, cursor)
	})
	s.register("POST /api/v1/environments", func(r *http.Request) (any, error) {
		var body EnvironmentWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveEnvironmentMetadata(r.Context(), "", body.Name, body.Description, 0)
	})
	s.register("PUT /api/v1/environments/{id}", func(r *http.Request) (any, error) {
		var body EnvironmentWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveEnvironmentMetadata(r.Context(), r.PathValue("id"), body.Name, body.Description, body.Revision)
	})
	s.register("PUT /api/v1/environments/{id}/variables", func(r *http.Request) (any, error) {
		var body VariablesWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveVariables(r.Context(), r.PathValue("id"), body.Revision, body.Variables, body.Secrets)
	})
	s.register("GET /api/v1/environments/{id}/resources", func(r *http.Request) (any, error) {
		limit, cursor, err := pageOptions(r)
		if err != nil {
			return nil, err
		}
		return repo.ListResources(r.Context(), r.PathValue("id"), limit, cursor)
	})
	s.register("POST /api/v1/environments/{id}/resources", func(r *http.Request) (any, error) {
		var body ResourceWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveResource(r.Context(), "", r.PathValue("id"), body.Name, body.Kind, 0, body.Config, body.Secrets)
	})
	s.register("PUT /api/v1/resources/{id}", func(r *http.Request) (any, error) {
		var body ResourceWrite
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.SaveResource(r.Context(), r.PathValue("id"), "", body.Name, body.Kind, body.Revision, body.Config, body.Secrets)
	})
	s.register("POST /api/v1/resources/{id}/proto-bundles", func(r *http.Request) (any, error) {
		var body ProtoBundle
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.UploadProto(r.Context(), r.PathValue("id"), body.Revision, body.Files)
	})
}
