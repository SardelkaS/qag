// Package testfixture provides local targets without using user services.
package testfixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	health "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

type Certificates struct {
	CA, Client, Key string
	Server          tls.Certificate
	Pool            *x509.CertPool
}

func NewCertificates() (Certificates, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificates{}, err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "qag test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return Certificates{}, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	issue := func(serial int64, client bool) (tls.Certificate, string, string, error) {
		k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return tls.Certificate{}, "", "", e
		}
		use := x509.ExtKeyUsageServerAuth
		if client {
			use = x509.ExtKeyUsageClientAuth
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{use}}
		d, e := x509.CreateCertificate(rand.Reader, template, ca, &k.PublicKey, key)
		if e != nil {
			return tls.Certificate{}, "", "", e
		}
		kb, e := x509.MarshalPKCS8PrivateKey(k)
		if e != nil {
			return tls.Certificate{}, "", "", e
		}
		cp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})
		kp := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
		pair, e := tls.X509KeyPair(cp, kp)
		return pair, string(cp), string(kp), e
	}
	server, _, _, err := issue(2, false)
	if err != nil {
		return Certificates{}, err
	}
	_, client, private, err := issue(3, true)
	return Certificates{string(caPEM), client, private, server, pool}, err
}
func (c Certificates) ServerTLS() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{c.Server}, ClientCAs: c.Pool, ClientAuth: tls.RequireAndVerifyClientCert}
}

type Targets struct {
	HTTP    *httptest.Server
	GRPC    string
	Calls   atomic.Int32
	Flaky   atomic.Int32
	Sources atomic.Int32
	grpc    *grpc.Server
}

func Start(cert *Certificates, withReflection bool) (*Targets, error) {
	t := &Targets{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stats" {
			_ = json.NewEncoder(w).Encode(map[string]int32{"calls": t.Calls.Load(), "sources": t.Sources.Load()})
			return
		}
		if r.URL.Path == "/token" {
			n := t.Sources.Add(1)
			if r.URL.Query().Get("delay") != "" {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(60 * time.Second):
				}
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Query().Get("result") {
			case "error":
				w.WriteHeader(401)
				return
			case "invalid":
				_, _ = w.Write([]byte("{broken"))
				return
			case "null":
				_, _ = w.Write([]byte(`{"tokens":[{"value":null}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tokens": []any{map[string]any{"value": "fixture-secret-token-" + big.NewInt(int64(n)).String()}}})
			return
		}
		t.Calls.Add(1)
		if r.URL.Path == "/api/flaky" && t.Flaky.Add(1)%2 == 1 {
			w.WriteHeader(503)
		}
		if r.URL.Path == "/api/slow" {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(60 * time.Second):
			}
		}
		if r.URL.Path == "/api/missing" {
			w.WriteHeader(404)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			OK     bool        `json:"ok"`
			Number json.Number `json:"number"`
			Token  string      `json:"token"`
		}{true, "9007199254740993123", r.Header.Get("Authorization")})
	})
	if cert == nil {
		t.HTTP = httptest.NewServer(handler)
	} else {
		t.HTTP = httptest.NewUnstartedServer(handler)
		t.HTTP.TLS = cert.ServerTLS()
		t.HTTP.StartTLS()
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.HTTP.Close()
		return nil, err
	}
	opts := []grpc.ServerOption{}
	if cert != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(cert.ServerTLS())))
	}
	t.grpc = grpc.NewServer(opts...)
	health.RegisterHealthServer(t.grpc, &healthService{owner: t})
	if withReflection {
		reflection.Register(t.grpc)
	}
	t.GRPC = l.Addr().String()
	go t.grpc.Serve(l)
	return t, nil
}
func (t *Targets) Close() { t.grpc.Stop(); t.HTTP.Close() }

type healthService struct {
	health.UnimplementedHealthServer
	owner *Targets
}

func (h *healthService) Check(ctx context.Context, r *health.HealthCheckRequest) (*health.HealthCheckResponse, error) {
	h.owner.Calls.Add(1)
	_ = grpc.SetHeader(ctx, metadata.Pairs("fixture", "qag"))
	grpc.SetTrailer(ctx, metadata.Pairs("finished", "yes"))
	if r.Service == "missing" {
		return nil, status.Error(codes.NotFound, "not here")
	}
	if r.Service == "slow" {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return &health.HealthCheckResponse{Status: health.HealthCheckResponse_SERVING}, nil
}

const HealthProto = `syntax="proto3";package grpc.health.v1;import "types.proto";service Health {rpc Check(HealthCheckRequest) returns(HealthCheckResponse);rpc Watch(HealthCheckRequest) returns(stream HealthCheckResponse);}`
const HealthTypesProto = `syntax="proto3";package grpc.health.v1;message HealthCheckRequest {string service=1;} message HealthCheckResponse {enum ServingStatus {UNKNOWN=0;SERVING=1;NOT_SERVING=2;SERVICE_UNKNOWN=3;} ServingStatus status=1;}`
