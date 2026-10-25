package execution

import (
	"context"
	"qag/internal/catalog"
	"qag/internal/resources"
	"qag/internal/testfixture"
	"testing"
)

func TestMTLSAndImportedProto(t *testing.T) {
	cert, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	target, err := testfixture.Start(&cert, false)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	status := 200
	for _, kind := range []string{"http", "grpc"} {
		t.Run(kind, func(t *testing.T) {
			cfg := resources.Config{BaseURL: target.HTTP.URL + "/api", Address: target.GRPC, TLS: resources.TLSConfig{Mode: "tls", CA: cert.CA, Certificate: cert.Client}, ProtoFiles: map[string]string{"health.proto": testfixture.HealthProto, "types.proto": testfixture.HealthTypesProto}}
			res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Kind: kind, Config: cfg}, Secrets: map[string]string{"PRIVATE_KEY": cert.Key}}
			b := catalog.Block{Kind: kind, TimeoutSeconds: 2, Expected: catalog.Expectation{StatusCode: &status, GRPCCode: "OK", BodyMode: "none"}}
			if kind == "http" {
				b.HTTP = &catalog.HTTPRequest{Method: "GET", Path: "/task", BodyMode: "none"}
			} else {
				b.GRPC = &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{}`}
			}
			runner := NewRunner()
			state, result := runner.Execute(context.Background(), "mtls", b, res)
			if state != "completed" || result.Comparison.Status != "match" {
				t.Fatalf("mTLS failed: %s %+v", state, result)
			}
			res.Resource.Config.TLS.ServerName = "wrong.invalid"
			state, result = runner.Execute(context.Background(), "mtls", b, res)
			if state == "completed" || result.Comparison.Status == "match" {
				t.Fatal("hostname check bypassed")
			}
			res.Resource.Config.TLS.ServerName = ""
			other, err := testfixture.NewCertificates()
			if err != nil {
				t.Fatal(err)
			}
			res.Resource.Config.TLS.CA = other.CA
			state, _ = runner.Execute(context.Background(), "mtls", b, res)
			if state == "completed" {
				t.Fatal("wrong CA accepted")
			}
			res.Resource.Config.TLS.CA = cert.CA
			if resources.ValidateConfig(kind, res.Resource.Config, map[string]string{"PRIVATE_KEY": other.Key}) == nil {
				t.Fatal("mismatched certificate/key accepted")
			}
			res.Resource.Config.TLS.Certificate = ""
			res.Secrets = map[string]string{}
			state, _ = runner.Execute(context.Background(), "mtls", b, res)
			if state == "completed" {
				t.Fatal("missing client certificate accepted")
			}
			res.Resource.Config.TLS.Certificate = cert.Client
			res.Secrets = map[string]string{"PRIVATE_KEY": cert.Key}
			if kind == "grpc" {
				delete(res.Resource.Config.ProtoFiles, "types.proto")
				res.Resource.VersionID = catalog.NewID()
				before := target.Calls.Load()
				state, _ = runner.Execute(context.Background(), "mtls", b, res)
				if state != "error" || target.Calls.Load() != before {
					t.Fatal("missing import reached service")
				}
			}
		})
	}
	if resources.ValidateConfig("grpc", resources.Config{Address: target.GRPC, TLS: resources.TLSConfig{Mode: "plaintext"}, ProtoFiles: map[string]string{"../outside.proto": ""}}, nil) == nil {
		t.Fatal("unsafe import path accepted")
	}
}
