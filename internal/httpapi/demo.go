package httpapi

import (
	"net/http"
	"qag/internal/catalog"
	"qag/internal/demo"
)

func (s *Server) RegisterDemo(service *demo.Service, c *catalog.Repository) {
	s.mux.Handle("/demo/v1/", service.Handler())
	s.register("GET /api/v1/demo", func(r *http.Request) (any, error) { return c.DemoInfo(r.Context()) })
}
