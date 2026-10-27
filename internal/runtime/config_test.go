package runtime

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRejectsUnsafeBindingsAndDoesNotExposeSecrets(t *testing.T) {
	base := map[string]string{"QAG_DATABASE_URL": "postgres://user:sentinel-password@localhost:5432/qag", "QAG_DEMO_DATABASE_URL": "postgres://demo:reader@localhost:5432/qag", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32))}
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

func TestFileConfigOverridesAndFailClosed(t *testing.T) {
	values := map[string]string{"QAG_DATABASE_URL": "postgres://qag:secret@localhost:5432/qag?sslmode=disable", "QAG_DEMO_DATABASE_URL": "postgres://reader:secret@localhost:5432/qag?sslmode=disable", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	path := filepath.Join(t.TempDir(), "installation.json")
	raw, _ := json.Marshal(FileConfig{Version: 1, Values: values})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"QAG_CONFIG_FILE": path, "QAG_ADDRESS": "0.0.0.0:8080", "QAG_NETWORK_MODE": "container", "QAG_PUBLIC_ORIGIN": "http://127.0.0.1:18088"}
	load := func() (Config, error) {
		return LoadConfigEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	}
	c, err := load()
	if err != nil || c.PublicOrigin != env["QAG_PUBLIC_ORIGIN"] || len(c.SecretKey) != 32 {
		t.Fatal(c, err)
	}
	env["QAG_NETWORK_MODE"] = ""
	if _, err = load(); err == nil {
		t.Fatal("wildcard native accepted")
	}
	env["QAG_NETWORK_MODE"] = "container"
	for _, origin := range []string{"https://localhost:8080", "http://evil.invalid", "http://localhost:0", "http://localhost:65536", "http://u:p@localhost:8080", "http://localhost/a", "http://localhost/?a", "http://localhost/#a"} {
		env["QAG_PUBLIC_ORIGIN"] = origin
		if _, err = load(); err == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
	env["QAG_PUBLIC_ORIGIN"] = "http://localhost:18088"
	env["QAG_DATABASE_URL"] = ""
	if _, err = load(); err == nil {
		t.Fatal("explicit empty env ignored")
	}
	delete(env, "QAG_DATABASE_URL")
	env["QAG_TEST_MODE"] = "1"
	env["QAG_SCHEMA"] = "qag_test_0123456789abcdef0123456789abcdef"
	env["QAG_DEMO_SCHEMA"] = env["QAG_SCHEMA"] + "_demo"
	c, err = load()
	if err != nil || c.Namespace.App != env["QAG_SCHEMA"] {
		t.Fatal("file namespace override failed", err)
	}
	for _, raw := range []string{`{"version":1,"values":{},"unknown":"sentinel-private"}`, `sentinel-private`} {
		os.WriteFile(path, []byte(raw), 0600)
		_, err = load()
		if err == nil || strings.Contains(err.Error(), "sentinel-private") {
			t.Fatal("bad file accepted or exposed")
		}
	}
	// Valid process variables must not hide an invalid specified file.
	for k, v := range values {
		env[k] = v
	}
	os.WriteFile(path, []byte(`{"version":1,"values":{}}`), 0600)
	if _, err = load(); err == nil {
		t.Fatal("invalid file was silently repaired by environment")
	}
	os.Remove(path)
	if _, err = load(); err == nil {
		t.Fatal("missing file fell back")
	}
	if ClientAddress("[::]:8080") != "127.0.0.1:8080" {
		t.Fatal("wildcard client")
	}
}

func TestConfigRejectsRemoteDevelopmentOrigin(t *testing.T) {
	env := map[string]string{"QAG_DATABASE_URL": "postgres://user@localhost/qag", "QAG_DEMO_DATABASE_URL": "postgres://demo@localhost/qag", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32)), "QAG_DEV_ORIGIN": "http://evil.example"}
	if _, err := LoadConfig(func(key string) string { return env[key] }); err == nil {
		t.Fatal("remote dev origin accepted")
	}
}
