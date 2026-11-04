//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/testfixture"
)

func TestPostgresMTLS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	role, cert, err := fixture.EnableMTLS(ctx, os.Getenv("QAG_TEST_PKI_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(fixture.TargetURL)
	if err != nil {
		t.Fatal(err)
	}
	res := resources.Resolved{Resource: resources.Resource{Kind: "postgresql", Config: resources.Config{Address: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Database: "qag", Username: role, TLS: resources.TLSConfig{Mode: "mtls", CA: cert.CA, Certificate: cert.Client}}}, Secrets: map[string]string{"PRIVATE_KEY": cert.Key}}
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
	res.Resource.Config.TLS.Mode = "plaintext"
	if state, _ := runner.Execute(ctx, "plaintext", b, res); state == "completed" {
		t.Fatal("certificate role accepted plaintext")
	}
}
