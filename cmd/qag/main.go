package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qag/internal/catalog"
	"qag/internal/httpapi"
	"qag/internal/resources"
	"qag/internal/runs"
	appruntime "qag/internal/runtime"
	"qag/internal/service"
	"qag/internal/storage"
	"qag/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qag:", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := appruntime.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := storage.Open(startup, config.DatabaseURL)
	if err != nil {
		cancel()
		return err
	}
	defer store.Close()
	if err = store.AcquireInstance(startup); err != nil {
		cancel()
		return err
	}
	err = store.Migrate(startup)
	if err != nil {
		cancel()
		return err
	}
	runRepo := runs.NewRepository(store.Pool)
	if err = runRepo.Recover(startup); err != nil {
		cancel()
		return err
	}
	cancel()
	api, err := httpapi.New(config, web.Assets(), store.Ping)
	if err != nil {
		return err
	}
	catalogRepo := catalog.NewRepository(store.Pool)
	api.RegisterCatalog(catalogRepo)
	vault, err := resources.NewVault(config.SecretKey)
	if err != nil {
		return fmt.Errorf("не удалось настроить хранилище секретов")
	}
	resourceRepo := resources.NewRepository(store.Pool, vault)
	api.RegisterResources(resourceRepo)
	engine := service.New(ctx, catalogRepo, resourceRepo, runRepo)
	defer engine.Close()
	api.RegisterRuns(engine)
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return fmt.Errorf("локальный порт недоступен; измените QAG_ADDRESS")
	}
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("qag: http://%s\n", listener.Addr())
	select {
	case <-store.InstanceLost():
		stop()
		shutdown, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		_ = server.Shutdown(shutdown)
		return fmt.Errorf("потеряна блокировка PostgreSQL; приложение остановлено")
	case <-ctx.Done():
		shutdown, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		return server.Shutdown(shutdown)
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return fmt.Errorf("локальный сервер остановлен")
	}
}
