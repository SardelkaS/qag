package runtime

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestConfigRejectsUnsafeBindingsAndDoesNotExposeSecrets(t *testing.T) {
	base := map[string]string{"QAG_DATABASE_URL": "postgres://user:sentinel-password@localhost:5432/qag", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	for _, address := range []string{"0.0.0.0:8080", ":8080", "example.org:8080", "127.0.0.1:0", "[::]:8080"} {
		t.Run(address, func(t *testing.T) {
			base["QAG_ADDRESS"] = address
			_, err := LoadConfig(func(key string) string { return base[key] })
			if err == nil {
				t.Fatal("unsafe address accepted")
			}
			if strings.Contains(err.Error(), "sentinel-password") {
				t.Fatal("secret exposed")
			}
		})
	}
	delete(base, "QAG_ADDRESS")
	config, err := LoadConfig(func(key string) string { return base[key] })
	if err != nil || config.Address != "127.0.0.1:8080" || len(config.SecretKey) != 32 {
		t.Fatalf("valid config rejected: %v", err)
	}
	base["QAG_DATABASE_URL"] = "postgres://user:sentinel-password@%invalid"
	_, err = LoadConfig(func(key string) string { return base[key] })
	if err == nil || strings.Contains(err.Error(), "sentinel-password") {
		t.Fatal("invalid DSN was accepted or exposed")
	}
}

func TestConfigRejectsRemoteDevelopmentOrigin(t *testing.T) {
	env := map[string]string{"QAG_DATABASE_URL": "postgres://localhost/qag", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)), "QAG_DEV_ORIGIN": "http://evil.example"}
	if _, err := LoadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("remote dev origin accepted")
	}
}
