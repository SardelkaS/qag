package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUnavailable = errors.New("PostgreSQL приложения недоступна; проверьте подключение и учётные данные")

type Store struct {
	Pool     *pgxpool.Pool
	instance *instanceLock
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrUnavailable
	}
	config.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, ErrUnavailable
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.releaseInstance(); s.Pool.Close() }
func (s *Store) Ping(ctx context.Context) error {
	if err := s.Pool.Ping(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
