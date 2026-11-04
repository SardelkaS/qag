package execution

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	health "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"net"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/testfixture"
	"sync/atomic"
	"testing"
)

func TestIndependentSourceMTLSAndReflectionHeaders(t *testing.T) {
	sourcePair, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	targetPair, err := testfixture.NewCertificates()
	if err != nil {
		t.Fatal(err)
	}
	source, err := testfixture.Start(&sourcePair, true)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := testfixture.Start(&targetPair, true)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	var reflected atomic.Int32
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(ctx context.Context) error {
		md, _ := metadata.FromIncomingContext(ctx)
		if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer fixture-secret-token-2" {
			return status.Error(codes.Unauthenticated, "source header missing")
		}
		return nil
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(targetPair.ServerTLS())), grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if err := authorize(ctx); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}), grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		if err := authorize(stream.Context()); err != nil {
			return err
		}
		reflected.Add(1)
		return next(srv, stream)
	}))
	health.RegisterHealthServer(server, &fixtureHealth{})
	reflection.Register(server)
	go server.Serve(listener)
	defer server.Stop()
	code := 200
	for _, kind := range []string{"http", "grpc"} {
		src := &catalog.HTTPHeaderSource{Method: "POST", URL: source.HTTP.URL + "/token", BodyMode: "json", Body: `{"client":"${CLIENT_SECRET}"}`, JSONPointer: "/tokens/0/value", Prefix: "Bearer ", FetchMode: "per_attempt", TLS: catalog.TLSConfig{Mode: "mtls", CA: sourcePair.CA, Certificate: sourcePair.Client}, PrivateKeySecret: "SOURCE_KEY"}
		header := catalog.RequestHeader{ID: catalog.NewID(), Name: "Authorization", Source: src}
		cfg := resources.Config{BaseURL: target.HTTP.URL + "/api", Address: listener.Addr().String(), TLS: resources.TLSConfig{Mode: "mtls", CA: targetPair.CA, Certificate: targetPair.Client}, Headers: []catalog.RequestHeader{header}}
		res := resources.Resolved{Resource: resources.Resource{Kind: kind, VersionID: catalog.NewID(), Config: cfg}, Secrets: map[string]string{"PRIVATE_KEY": targetPair.Key, "SOURCE_KEY": sourcePair.Key, "CLIENT_SECRET": "source-client-secret"}}
		block := catalog.Block{Kind: kind, Resource: "GW", TimeoutSeconds: 5, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/task", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
		if kind == "grpc" {
			block.HTTP = nil
			block.GRPC = &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{}`}
			block.Expected = catalog.Expectation{GRPCCode: "OK", BodyMode: "none"}
		}
		b, resolved, err := res.Prepare(block)
		if err != nil {
			t.Fatal(err)
		}
		runner := NewRunner()
		state, result := runner.Execute(context.Background(), kind, b, resolved)
		if state != "completed" || result.Comparison.Status != "match" {
			t.Fatalf("independent %s mTLS: %s %s", kind, state, result.Error)
		}
		if kind == "grpc" && reflected.Load() == 0 {
			t.Fatal("reflection was not authenticated")
		}
		// A source signed by the target CA must be rejected before the target call.
		src.TLS.CA = targetPair.CA
		b, resolved, err = res.Prepare(block)
		if err != nil {
			t.Fatal(err)
		}
		state, result = runner.Execute(context.Background(), kind+"-invalid-source", b, resolved)
		if state != "error" || result.MainRequestStarted == nil || *result.MainRequestStarted {
			t.Fatal("source TLS failure reached main request")
		}
		runner.ClearHeaderScopes()
	}
}

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
