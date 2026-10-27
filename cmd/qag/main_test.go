package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckHasNoDatabaseOrProxyDependency(t *testing.T) {
	ready := true
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			t.Error("wrong path")
		}
		if !ready {
			w.WriteHeader(503)
		}
	}))
	defer s.Close()
	t.Setenv("QAG_ADDRESS", strings.TrimPrefix(s.URL, "http://"))
	t.Setenv("HTTP_PROXY", "http://invalid.invalid:1")
	if healthcheck() != nil {
		t.Fatal("healthy check failed")
	}
	ready = false
	if healthcheck() == nil {
		t.Fatal("unhealthy accepted")
	}
	s.Close()
	if healthcheck() == nil {
		t.Fatal("closed listener accepted")
	}
}
