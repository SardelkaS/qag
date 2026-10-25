package runtime

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDemoConfigurationIsSeparateAndLoopback(t *testing.T) {
	env := map[string]string{"QAG_DATABASE_URL": "postgresql://admin:secret@127.0.0.1:5432/qag?sslmode=disable", "QAG_DEMO_DATABASE_URL": "postgresql://reader:sentinel-demo-password@127.0.0.1:5432/qag?sslmode=disable", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	for _, address := range []string{"127.0.0.1:0", "127.0.0.1:9091", "[::1]:9091"} {
		env["QAG_DEMO_GRPC_ADDRESS"] = address
		if _, err := LoadConfig(func(k string) string { return env[k] }); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"0.0.0.0:9091", ":9091", "evil.example:9091", "127.0.0.1:65536"} {
		env["QAG_DEMO_GRPC_ADDRESS"] = address
		if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
			t.Fatal("nonlocal demo accepted")
		}
	}
	env["QAG_DEMO_GRPC_ADDRESS"] = "127.0.0.1:0"
	for _, value := range []string{"", "postgresql://admin:sentinel-demo-password@127.0.0.1:5432/qag?sslmode=disable", "postgresql://reader:sentinel-demo-password@127.0.0.1:5432/other?sslmode=disable"} {
		env["QAG_DEMO_DATABASE_URL"] = value
		_, err := LoadConfig(func(k string) string { return env[k] })
		if err == nil || strings.Contains(err.Error(), "sentinel-demo-password") {
			t.Fatal("unsafe demo configuration or secret disclosed")
		}
	}
}
