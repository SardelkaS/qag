package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"qag/internal/catalog"
	"qag/internal/demo"
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
	store, err := storage.Open(startup, config.DatabaseURL, config.Namespace)
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
	cancel()
	demoConfig, demoSecrets, err := demo.DatabaseConfig(config.DatabaseURL, config.DemoDatabaseURL)
	if err != nil {
		return err
	}
	demoStartup, cancelDemoStartup := context.WithTimeout(ctx, 15*time.Second)
	defer cancelDemoStartup()
	demoPool, err := demo.OpenReader(demoStartup, store.Pool, demoConfig, demoSecrets)
	if err != nil {
		return err
	}
	defer demoPool.Close()
	grpcListener, err := net.Listen("tcp", config.DemoGRPCAddress)
	if err != nil {
		return fmt.Errorf("демонстрационный gRPC-порт недоступен; измените QAG_DEMO_GRPC_ADDRESS")
	}
	defer grpcListener.Close()
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return fmt.Errorf("локальный порт недоступен; измените QAG_ADDRESS")
	}
	defer listener.Close()
	vault, err := resources.NewVault(config.SecretKey)
	if err != nil {
		return fmt.Errorf("не удалось настроить хранилище секретов")
	}
	if _, err = demo.Bootstrap(demoStartup, store.Pool, vault, "http://"+listener.Addr().String(), grpcListener.Addr().String(), demoConfig, demoSecrets); err != nil {
		return err
	}
	if err = runRepo.Recover(demoStartup); err != nil {
		return err
	}
	demoService := &demo.Service{Pool: demoPool}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(64 << 10))
	demoService.RegisterGRPC(grpcServer)
	defer func() {
		finished := make(chan struct{})
		go func() { grpcServer.GracefulStop(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			grpcServer.Stop()
		}
	}()
	grpcDone := make(chan error, 1)
	var grpcLive atomic.Bool
	go func() { grpcLive.Store(true); defer grpcLive.Store(false); grpcDone <- grpcServer.Serve(grpcListener) }()
	api, err := httpapi.New(config, web.Assets(), func(c context.Context) error {
		if err := store.Ping(c); err != nil {
			return err
		}
		if !grpcLive.Load() {
			return fmt.Errorf("demo gRPC недоступен")
		}
		return demoService.Ready(c)
	})
	if err != nil {
		return err
	}
	catalogRepo := catalog.NewRepository(store.Pool)
	api.RegisterCatalog(catalogRepo)
	api.RegisterDemo(demoService, catalogRepo)
	resourceRepo := resources.NewRepository(store.Pool, vault)
	api.RegisterResources(resourceRepo)
	engine := service.New(ctx, catalogRepo, resourceRepo, runRepo)
	defer engine.Close()
	api.RegisterRuns(engine)
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("qag: http://%s\n", listener.Addr())
	select {
	case <-grpcDone:
		stop()
		shutdown, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		_ = server.Shutdown(shutdown)
		return fmt.Errorf("демонстрационный gRPC-сервер остановлен")
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
