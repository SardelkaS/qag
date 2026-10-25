//go:build integration

package execution

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qag/internal/catalog"
	"qag/internal/resources"
	"qag/internal/testfixture"
)

// This target is deliberately separate from the application and qag_test servers.
// It has a fresh CA, a random loopback port and no persistent volume.
func TestPostgresMTLS(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		docker = filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "DockerDesktop", "resources", "bin", "docker.exe")
	}
	if _, err = os.Stat(docker); err != nil {
		t.Fatal("Docker is required for the isolated PostgreSQL mTLS target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	command := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, docker, args...).CombinedOutput()
	}
	cert, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.Server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"server.crt":  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Server.Certificate[0]}),
		"server.key":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
		"ca.crt":      []byte(cert.CA),
		"pg_hba.conf": []byte("local all all trust\nhostssl all all 0.0.0.0/0 cert\nhostssl all all ::/0 cert\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	name := "qag-mtls-" + catalog.NewID()
	startup := `mkdir -p /run/qag-pki && cp /tmp/qag-pki/* /run/qag-pki/ && chown -R postgres:postgres /run/qag-pki && chmod 600 /run/qag-pki/server.key && exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/run/qag-pki/server.crt -c ssl_key_file=/run/qag-pki/server.key -c ssl_ca_file=/run/qag-pki/ca.crt -c hba_file=/run/qag-pki/pg_hba.conf`
	if out, err := command("create", "--name", name, "--publish", "127.0.0.1::5432", "--env", "POSTGRES_HOST_AUTH_METHOD=trust", "--entrypoint", "/bin/sh", "postgres:18.6-alpine3.24", "-c", startup); err != nil {
		t.Fatalf("create isolated target: %s %v", out, err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if err := exec.CommandContext(cleanup, docker, "rm", "--force", "--volumes", name).Run(); err != nil {
			t.Errorf("cleanup isolated target: %v", err)
		}
	})
	if out, err := command("cp", dir, name+":/tmp/qag-pki"); err != nil {
		t.Fatalf("copy test PKI: %s %v", out, err)
	}
	if out, err := command("start", name); err != nil {
		t.Fatalf("start target: %s %v", out, err)
	}
	ready := false
	for ctx.Err() == nil {
		// The image's temporary initdb server listens only on its Unix socket.
		// Wait for TCP so a connection cannot race that server's shutdown.
		if _, err := command("exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "postgres"); err == nil {
			ready = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		t.Fatal("isolated PostgreSQL did not become ready")
	}
	if out, err := command("exec", name, "psql", "-U", "postgres", "-c", "CREATE ROLE localhost LOGIN"); err != nil {
		t.Fatalf("create test client role: %s %v", out, err)
	}
	out, err := command("port", name, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(out))
	if _, _, err := net.SplitHostPort(address); err != nil {
		t.Fatal(err)
	}
	res := resources.Resolved{Resource: resources.Resource{Kind: "postgresql", Config: resources.Config{Address: address, Database: "postgres", Username: "localhost", TLS: resources.TLSConfig{Mode: "mtls", CA: cert.CA, Certificate: cert.Client}}}, Secrets: map[string]string{"PRIVATE_KEY": cert.Key}}
	b := catalog.Block{Kind: "postgresql", TimeoutSeconds: 5, SQL: &catalog.SQLRequest{Query: "SELECT 42::integer AS answer", Mode: "rows"}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "answer", Type: "int4"}}, Rows: [][]catalog.TypedValue{{{Type: "integer", Value: "42"}}}}}
	runner := NewRunner()
	state, result := runner.Execute(ctx, "postgres-mtls", b, res)
	if state != "completed" || result.Comparison.Status != "match" {
		data, _ := json.Marshal(result)
		t.Fatalf("mTLS SQL: %s %s", state, data)
	}
	for _, mode := range []string{"tls", "mtls"} {
		// Old TLS-with-a-pair documents remain executable.
		res.Resource.Config.TLS.Mode = mode
		state, _ = runner.Execute(ctx, "legacy", b, res)
		if state != "completed" {
			t.Fatal("legacy TLS pair no longer works")
		}
	}
	res.Resource.Config.TLS.ServerName = "wrong.invalid"
	if state, _ := runner.Execute(ctx, "wrong-host", b, res); state == "completed" {
		t.Fatal("hostname validation bypassed")
	}
	res.Resource.Config.TLS.ServerName = ""
	other, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	res.Resource.Config.TLS.CA = other.CA
	if state, _ := runner.Execute(ctx, "wrong-ca", b, res); state == "completed" {
		t.Fatal("CA validation bypassed")
	}
	res.Resource.Config.TLS.CA = cert.CA
	res.Resource.Config.TLS.Certificate = other.Client
	res.Secrets = map[string]string{"PRIVATE_KEY": other.Key}
	if state, _ := runner.Execute(ctx, "untrusted-client", b, res); state == "completed" {
		t.Fatal("PostgreSQL accepted an untrusted client certificate")
	}
	res.Resource.Config.TLS.Certificate = cert.Client
	res.Secrets = map[string]string{"PRIVATE_KEY": other.Key}
	if state, _ := runner.Execute(ctx, "mismatched-pair", b, res); state == "completed" {
		t.Fatal("mismatched client pair accepted")
	}
	res.Resource.Config.TLS.Mode = "tls"
	res.Resource.Config.TLS.Certificate = ""
	res.Secrets = nil
	if state, _ := runner.Execute(ctx, "missing-pair", b, res); state == "completed" {
		t.Fatal("PostgreSQL accepted TLS without a required client pair")
	}
}
