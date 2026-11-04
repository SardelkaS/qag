package main

import (
	"context"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/demo"
	"github.com/SardelkaS/qag/internal/filestore"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
	"github.com/SardelkaS/qag/internal/storage"
)

type backend struct {
	ports.Lifecycle
	Catalog     ports.Catalog
	Resources   ports.Resources
	Runs        ports.Runs
	Demo        *demo.Service
	Bootstrap   func(context.Context, string, string) error
	closeReader func()
}

func (b *backend) Close() {
	if b.closeReader != nil {
		b.closeReader()
	}
	b.Lifecycle.Close()
}
func openBackend(ctx context.Context, c appruntime.Config) (*backend, error) {
	if c.Storage == "yaml" {
		s, e := filestore.Open(c.DataFile, true)
		if e != nil {
			return nil, e
		}
		return &backend{Lifecycle: s, Catalog: &filestore.Catalog{Store: s}, Resources: &filestore.Resources{Store: s}, Runs: &filestore.Runs{Store: s}, Demo: &demo.Service{Reader: s}, Bootstrap: s.Bootstrap}, nil
	}
	s, e := storage.Open(ctx, c.DatabaseURL, c.Namespace)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			s.Close()
		}
	}()
	if e = s.AcquireInstance(ctx); e != nil {
		return nil, e
	}
	if e = s.Migrate(ctx); e != nil {
		return nil, e
	}
	vault, e := resources.NewVault(c.SecretKey)
	if e != nil {
		return nil, e
	}
	dc, secrets, e := demo.DatabaseConfig(c.DatabaseURL, c.DemoDatabaseURL)
	if e != nil {
		return nil, e
	}
	reader, e := demo.OpenReader(ctx, s.Pool, dc, secrets)
	if e != nil {
		return nil, e
	}
	b := &backend{Lifecycle: s, Catalog: catalog.NewRepository(s.Pool), Resources: resources.NewRepository(s.Pool, vault), Runs: runs.NewRepository(s.Pool), Demo: &demo.Service{Pool: reader}, closeReader: reader.Close}
	b.Bootstrap = func(ctx context.Context, httpURL, grpcAddress string) error {
		_, e := demo.Bootstrap(ctx, s.Pool, vault, httpURL, grpcAddress, dc, secrets)
		return e
	}
	ok = true
	return b, nil
}
