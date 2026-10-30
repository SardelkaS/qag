package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"qag/internal/catalog"
	"qag/internal/clipboard"
	"qag/internal/demo"
	"qag/internal/httpapi"
	"qag/internal/mcpserver"
	"qag/internal/reporting"
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
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return healthcheck()
	}
	config, err := appruntime.LoadConfigEnv(os.LookupEnv)
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
	if _, err = demo.Bootstrap(demoStartup, store.Pool, vault, "http://"+appruntime.ClientAddress(listener.Addr().String()), grpcListener.Addr().String(), demoConfig, demoSecrets); err != nil {
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
	reports := reporting.NewService(runRepo, catalogRepo, web.Assets(), clipboard.Available())
	api.RegisterRuns(engine, reports)
	mcp := mcpserver.New(mcpserver.Options{Engine: engine, Reports: reports, Logger: slog.Default(), Ping: func(c context.Context) error {
		if !grpcLive.Load() {
			return fmt.Errorf("demo gRPC недоступен")
		}
		if err := store.Ping(c); err != nil {
			return err
		}
		return demoService.Ready(c)
	}})
	api.RegisterMCP(mcp.Handler())
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

func healthcheck() error {
	address := os.Getenv("QAG_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8080"
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get("http://" + appruntime.ClientAddress(address) + "/api/v1/health")
	if err != nil {
		return fmt.Errorf("приложение не готово")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("приложение не готово")
	}
	return nil
}
