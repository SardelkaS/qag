// testfixture creates an isolated, disposable database and local protocol targets.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"qag/internal/catalog"
	"qag/internal/demo"
	"qag/internal/testfixture"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Не удалось запустить изолированный тестовый стенд")
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", ".local/e2e.json", "private configuration output")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	u, err := url.Parse(os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil || strings.Trim(u.Path, "/") != "qag_test" {
		return fmt.Errorf("test database required")
	}
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return err
	}
	defer admin.Close(context.Background())
	db := "qag_e2e_" + strings.ReplaceAll(catalog.NewID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
		return err
	}
	var demoRole string
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)")
		if demoRole != "" {
			_, _ = admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{demoRole}.Sanitize())
		}
	}()
	appURL := *u
	appURL.Path = "/" + db
	demoURL := appURL
	demoName := "qag_demo_e2e_" + strings.ReplaceAll(catalog.NewID(), "-", "")
	demoURL.User = url.UserPassword(demoName, catalog.NewID()+catalog.NewID())
	demoConfig, demoSecrets, err := demo.DatabaseConfig(appURL.String(), demoURL.String())
	if err != nil {
		return err
	}
	provision, err := pgxpool.New(ctx, u.String())
	if err != nil {
		return err
	}
	err = demo.PrepareRole(ctx, provision, demoConfig, demoSecrets)
	provision.Close()
	if err != nil {
		return err
	}
	demoRole = demoName
	cert, err := testfixture.NewCertificates()
	if err != nil {
		return err
	}
	plain, err := testfixture.Start(nil, true)
	if err != nil {
		return err
	}
	defer plain.Close()
	secure, err := testfixture.Start(&cert, false)
	if err != nil {
		return err
	}
	defer secure.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204); close(stop) })
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	go server.Serve(l)
	defer server.Close()
	config := map[string]any{"databaseUrl": appURL.String(), "http": plain.HTTP.URL, "grpc": plain.GRPC, "mtlsHttp": secure.HTTP.URL, "mtlsGrpc": secure.GRPC, "ca": cert.CA, "certificate": cert.Client, "privateKey": cert.Key, "shutdown": "http://" + l.Addr().String() + "/shutdown", "target": map[string]string{"address": u.Host, "database": "qag_test", "username": u.User.Username(), "password": func() string { p, _ := u.User.Password(); return p }()}}
	config["demoDatabaseUrl"] = demoURL.String()
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err = os.WriteFile(*path, data, 0600); err != nil {
		return err
	}
	defer os.Remove(*path)
	<-stop
	return nil
}
