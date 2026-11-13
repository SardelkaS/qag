// testfixture creates isolated disposable schemas and local protocol targets.
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

	"time"

	"github.com/SardelkaS/qag/internal/testfixture"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	f, err := testfixture.NewDatabase(ctx, os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer func() { clean, done := testfixture.CleanupContext(); defer done(); _ = f.Close(clean) }()
	targetPool, err := pgxpool.New(ctx, f.TargetURL)
	if err != nil {
		return err
	}
	defer targetPool.Close()
	sequence := pgx.Identifier{f.TargetSchema, "effect_sequence"}.Sanitize()
	if _, err = targetPool.Exec(ctx, `CREATE SEQUENCE `+sequence); err != nil {
		return err
	}
	targetURL, _ := url.Parse(f.TargetURL)
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
	mux.HandleFunc("GET /sql-effect", func(w http.ResponseWriter, r *http.Request) {
		var called bool
		var value int64
		if err := targetPool.QueryRow(r.Context(), `SELECT last_value,is_called FROM `+sequence).Scan(&value, &called); err != nil {
			http.Error(w, "fixture target unavailable", 503)
			return
		}
		if !called {
			value = 0
		}
		_ = json.NewEncoder(w).Encode(map[string]int64{"effects": value})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204); close(stop) })
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	go server.Serve(l)
	defer server.Close()
	config := map[string]any{"databaseUrl": f.AppURL, "http": plain.HTTP.URL, "grpc": plain.GRPC, "mtlsHttp": secure.HTTP.URL, "mtlsGrpc": secure.GRPC, "ca": cert.CA, "certificate": cert.Client, "privateKey": cert.Key, "shutdown": "http://" + l.Addr().String() + "/shutdown", "target": map[string]string{"address": targetURL.Host, "database": "qag", "username": targetURL.User.Username(), "password": func() string { p, _ := targetURL.User.Password(); return p }()}}
	if dir := os.Getenv("QAG_TEST_PKI_DIR"); dir != "" {
		role, sqlCert, err := f.EnableMTLS(ctx, dir)
		if err != nil {
			return err
		}
		config["mtlsSQL"] = map[string]string{"username": role, "ca": sqlCert.CA, "certificate": sqlCert.Client, "privateKey": sqlCert.Key}
	}
	config["demoDatabaseUrl"] = f.ReaderURL
	config["appSchema"] = f.Namespace.App
	config["demoSchema"] = f.Namespace.Demo
	config["targetSchema"] = f.TargetSchema
	config["sqlEffectURL"] = "http://" + l.Addr().String() + "/sql-effect"
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
