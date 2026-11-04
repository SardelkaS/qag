package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/SardelkaS/qag/internal/clipboard"
	"github.com/SardelkaS/qag/internal/httpapi"
	"github.com/SardelkaS/qag/internal/mcpserver"
	"github.com/SardelkaS/qag/internal/reporting"
	appruntime "github.com/SardelkaS/qag/internal/runtime"
	"github.com/SardelkaS/qag/internal/service"
	"github.com/SardelkaS/qag/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "qag:", err)
		code := 1
		var ce *commandError
		if errors.As(err, &ce) {
			code = ce.code
		}
		os.Exit(code)
	}
}

func run() error { return command(os.Args[1:], os.LookupEnv, os.Stdout) }

func serve(config appruntime.Config, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	store, err := openBackend(startup, config)
	cancel()
	if err != nil {
		return err
	}
	defer store.Close()
	runRepo := store.Runs
	demoService := store.Demo
	demoStartup, cancelDemoStartup := context.WithTimeout(ctx, 15*time.Second)
	defer cancelDemoStartup()
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
	if err = store.Bootstrap(demoStartup, "http://"+appruntime.ClientAddress(listener.Addr().String()), grpcListener.Addr().String()); err != nil {
		return err
	}
	if err = runRepo.Recover(demoStartup); err != nil {
		return err
	}
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
	catalogRepo := store.Catalog
	api.RegisterCatalog(catalogRepo)
	api.RegisterDemo(demoService, catalogRepo)
	resourceRepo := store.Resources
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
	fmt.Fprintf(out, "qag: http://%s\nMCP: http://%s/mcp\nХранилище: %s\n", appruntime.ClientAddress(listener.Addr().String()), appruntime.ClientAddress(listener.Addr().String()), config.Storage)
	if config.Storage == "yaml" {
		fmt.Fprintln(out, "YAML содержит открытые секреты; сохраняйте приватные права файла")
	}
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
		return fmt.Errorf("потеряно владение или подтверждение хранилища; приложение остановлено")
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

func healthcheck() error { return healthcheckAddress(os.Getenv("QAG_ADDRESS")) }
func healthcheckAddress(address string) error {
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
