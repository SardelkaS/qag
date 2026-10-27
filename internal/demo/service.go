package demo

import "qag/internal/dbscope"

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	demopb "qag/internal/demo/proto"
)

//go:embed proto/demo.proto
var Proto string

type Service struct {
	demopb.UnimplementedDemoServiceServer
	Pool *pgxpool.Pool
}

func (s *Service) Ready(ctx context.Context) error {
	var ok int
	return s.Pool.QueryRow(ctx, dbscope.Render(s.Pool, `SELECT 1 FROM qag_demo.items WHERE id='example-001'`)).Scan(&ok)
}

type Item struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Amount string `json:"amount"`
}

func (s *Service) read(ctx context.Context, id string) (Item, error) {
	if len(id) > 128 {
		return Item{}, pgx.ErrNoRows
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var i Item
	err := s.Pool.QueryRow(ctx, dbscope.Render(s.Pool, `SELECT id,name,status,amount::text FROM qag_demo.items WHERE id=$1`), id).Scan(&i.ID, &i.Name, &i.Status, &i.Amount)
	return i, err
}
func (s *Service) GetItem(ctx context.Context, r *demopb.GetItemRequest) (*demopb.Item, error) {
	i, err := s.read(ctx, r.GetId())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "Демонстрационная запись не найдена")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "Демонстрационная БД недоступна")
	}
	return &demopb.Item{Id: i.ID, Name: i.Name, Status: i.Status, Amount: i.Amount}, nil
}
func (s *Service) RegisterGRPC(server *grpc.Server) {
	demopb.RegisterDemoServiceServer(server, s)
	reflection.Register(server)
}
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /demo/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		i, err := s.read(r.Context(), r.PathValue("id"))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "not_found", "message": "Демонстрационная запись не найдена"})
			return
		}
		if err != nil {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "unavailable", "message": "Демонстрационная БД недоступна"})
			return
		}
		_ = json.NewEncoder(w).Encode(i)
	})
	mux.HandleFunc("GET /demo/v1/demo.proto", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(Proto))
	})
	return mux
}
