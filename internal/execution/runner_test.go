package execution

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	health "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

func TestHTTPCompareRedirectRedactionAndLimits(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/task":
			if r.Header.Get("Authorization") != "Bearer test-token" || r.URL.Query().Get("name") != "O'Reilly" {
				t.Error("request parameters lost")
			}
			fmt.Fprint(w, `{"token":"test-token","n":9007199254740993123}`)
		case "/api/redirect":
			w.Header().Set("Location", target.URL)
			w.WriteHeader(302)
		case "/api/missing":
			w.WriteHeader(404)
		case "/api/large":
			fmt.Fprint(w, strings.Repeat("a", MaxResponseBytes+1))
		case "/api/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		}
	}))
	defer server.Close()
	res := resources.Resolved{Resource: resources.Resource{Config: resources.Config{BaseURL: server.URL + "/api", TLS: resources.TLSConfig{Mode: "plaintext"}}}, Secrets: map[string]string{"TOKEN": "test-token"}}
	statusCode := 200
	b := catalog.Block{Kind: "http", Resource: "GW", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/task", BodyMode: "none", Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer test-token"}}, Query: []catalog.KeyValue{{Name: "name", Value: "O'Reilly"}}}, Expected: catalog.Expectation{StatusCode: &statusCode, BodyMode: "json", Body: `{"token":"test-token","n":9007199254740993123}`}}
	runner := NewRunner()
	state, result := runner.Execute(context.Background(), "test", b, res)
	if state != "completed" || result.Comparison.Status != "match" || strings.Contains(result.Body, "test-token") || !result.Redacted {
		t.Fatalf("bad HTTP result: %s %+v", state, result)
	}
	redirectCode := 302
	b.HTTP.Path = "/redirect"
	b.Expected = catalog.Expectation{StatusCode: &redirectCode, BodyMode: "none"}
	_, result = runner.Execute(context.Background(), "test", b, res)
	if result.Comparison.Status != "match" || redirected.Load() != 0 {
		t.Fatal("redirect followed or status lost")
	}
	missing := 404
	b.HTTP.Path = "/missing"
	b.Expected = catalog.Expectation{StatusCode: &missing, BodyMode: "none"}
	_, result = runner.Execute(context.Background(), "test", b, res)
	if result.Comparison.Status != "match" {
		t.Fatal("expected HTTP 404 failed")
	}
	b.HTTP.Path = "/large"
	b.Expected = catalog.Expectation{StatusCode: &statusCode, BodyMode: "none"}
	_, result = runner.Execute(context.Background(), "test", b, res)
	if !result.Truncated || result.Comparison.Status != "not_compared" || len(result.Body) > MaxResponseBytes {
		t.Fatal("response limit failed")
	}
	b.HTTP.Path = "/slow"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	state, result = runner.Execute(ctx, "test", b, res)
	if state != "timed_out" || result.Comparison.Status != "not_compared" {
		t.Fatal("timeout passed")
	}
}

type fixtureHealth struct {
	health.UnimplementedHealthServer
	calls atomic.Int32
}

func (h *fixtureHealth) Check(ctx context.Context, r *health.HealthCheckRequest) (*health.HealthCheckResponse, error) {
	h.calls.Add(1)
	if r.Service == "missing" {
		return nil, status.Error(codes.NotFound, "not here")
	}
	if r.Service == "slow" {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if r.Service == "large" {
		result := &health.HealthCheckResponse{Status: health.HealthCheckResponse_SERVING}
		result.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 100, protowire.BytesType), make([]byte, MaxResponseBytes+1)))
		return result, nil
	}
	return &health.HealthCheckResponse{Status: health.HealthCheckResponse_SERVING}, nil
}

const healthProto = `syntax="proto3";package grpc.health.v1;
message HealthCheckRequest {string service=1;}
message HealthCheckResponse {enum ServingStatus {UNKNOWN=0;SERVING=1;NOT_SERVING=2;SERVICE_UNKNOWN=3;} ServingStatus status=1;}
service Health {rpc Check(HealthCheckRequest) returns(HealthCheckResponse);rpc Watch(HealthCheckRequest) returns(stream HealthCheckResponse);}`

func TestGRPCReflectionProtoAndExpectedError(t *testing.T) {
	for _, withReflection := range []bool{true, false} {
		t.Run(fmt.Sprint(withReflection), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			fixture := &fixtureHealth{}
			health.RegisterHealthServer(server, fixture)
			if withReflection {
				reflection.Register(server)
			}
			go server.Serve(listener)
			defer server.Stop()
			config := resources.Config{Address: listener.Addr().String(), TLS: resources.TLSConfig{Mode: "plaintext"}}
			if !withReflection {
				config.ProtoFiles = map[string]string{"health.proto": healthProto}
			}
			res := resources.Resolved{Resource: resources.Resource{VersionID: catalog.NewID(), Config: config}}
			b := catalog.Block{Kind: "grpc", Resource: "HEALTH", GRPC: &catalog.GRPCRequest{Service: "grpc.health.v1.Health", Method: "Check", Body: `{}`}, Expected: catalog.Expectation{GRPCCode: "OK", BodyMode: "json", Body: `{"status":"SERVING"}`}}
			runner := NewRunner()
			state, result := runner.Execute(context.Background(), "test", b, res)
			if state != "completed" || result.Comparison.Status != "match" {
				t.Fatalf("gRPC: %s %+v", state, result)
			}
			b.GRPC.Body = `{"service":"missing"}`
			b.Expected = catalog.Expectation{GRPCCode: "NOT_FOUND", BodyMode: "none"}
			state, result = runner.Execute(context.Background(), "test", b, res)
			if state != "completed" || result.Comparison.Status != "match" {
				t.Fatalf("expected error: %s %+v", state, result)
			}
			b.GRPC.Method = "Watch"
			before := fixture.calls.Load()
			state, result = runner.Execute(context.Background(), "test", b, res)
			if state != "error" || !strings.Contains(result.Error, "unary") || fixture.calls.Load() != before {
				t.Fatal("streaming method not rejected")
			}
			b.GRPC.Method = "Check"
			b.GRPC.Body = `{"service":"large"}`
			b.Expected = catalog.Expectation{GRPCCode: "OK", BodyMode: "none"}
			state, result = runner.Execute(context.Background(), "test", b, res)
			if !result.Truncated || result.Comparison.Status != "not_compared" {
				t.Fatal("large gRPC response passed")
			}
			b.GRPC.Body = `{"service":"slow"}`
			ctx, done := context.WithTimeout(context.Background(), 30*time.Millisecond)
			state, result = runner.Execute(ctx, "test", b, res)
			done()
			if state != "timed_out" || result.Comparison.Status != "not_compared" {
				t.Fatal("gRPC timeout passed")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			state, result = runner.Execute(ctx, "test", b, res)
			if state != "cancelled" || result.Comparison.Status != "not_compared" {
				t.Fatal("gRPC cancel passed")
			}
		})
	}
}
