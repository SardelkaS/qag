package httpapi

import (
	"github.com/SardelkaS/qag/internal/demo"
	"github.com/SardelkaS/qag/internal/ports"
	"net/http"
)

func (s *Server) RegisterDemo(service *demo.Service, c ports.Catalog) {
	s.mux.Handle("/demo/v1/", service.Handler())
	s.register("GET /api/v1/demo", func(r *http.Request) (any, error) { return c.DemoInfo(r.Context()) })
}
