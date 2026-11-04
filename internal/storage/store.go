package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/SardelkaS/qag/internal/dbscope"
)

var ErrUnavailable = errors.New("PostgreSQL приложения недоступна; проверьте подключение и учётные данные")

type Store struct {
	Pool      *pgxpool.Pool
	Namespace dbscope.Namespace
	instance  *instanceLock
}

type Namespace = dbscope.Namespace

func Open(ctx context.Context, databaseURL string, namespaces ...Namespace) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrUnavailable
	}
	config.MaxConns = 6
	ns := dbscope.Default
	if config.ConnConfig.RuntimeParams["qag.app_schema"] != "" || config.ConnConfig.RuntimeParams["qag.demo_schema"] != "" {
		ns = dbscope.Namespace{App: config.ConnConfig.RuntimeParams["qag.app_schema"], Demo: config.ConnConfig.RuntimeParams["qag.demo_schema"]}
	}
	if len(namespaces) > 0 {
		ns = namespaces[0]
	}
	if _, err := dbscope.New(ns.App, ns.Demo, ns != dbscope.Default); err != nil {
		return nil, err
	}
	dbscope.Configure(config.ConnConfig, ns)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, ErrUnavailable
	}
	return &Store{Pool: pool, Namespace: ns}, nil
}

func (s *Store) Close() { s.releaseInstance(); s.Pool.Close() }
func (s *Store) Ping(ctx context.Context) error {
	if err := s.Pool.Ping(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}
