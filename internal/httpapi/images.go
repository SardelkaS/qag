package httpapi

import (
	"context"
	"mime"
	"net/http"
	"qag/internal/catalog"
	"time"
)

func (s *Server) RegisterImages(repo *catalog.Repository) {
	s.register("POST /api/v1/test-cases/{id}/images", func(r *http.Request) (any, error) {
		var body catalog.ImageUpload
		if err := readJSON(r, &body); err != nil {
			return nil, err
		}
		return repo.UploadImage(r.Context(), r.PathValue("id"), body)
	})
	s.mux.HandleFunc("GET /api/v1/test-cases/{id}/images/{imageId}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		info, data, err := repo.ReadImage(ctx, r.PathValue("id"), r.PathValue("imageId"))
		if err != nil {
			s.operationError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", info.ContentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": info.Filename}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
}
