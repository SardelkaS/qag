package runtime

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyMCPTokenDoesNotRestrictStartup(t *testing.T) {
	values := map[string]string{"QAG_DATABASE_URL": "postgres://qag:test@127.0.0.1:15432/qag", "QAG_DEMO_DATABASE_URL": "postgres://reader:test@127.0.0.1:15432/qag", "QAG_SECRET_KEY": base64.StdEncoding.EncodeToString(make([]byte, 32))}
	for _, value := range []string{"", "old-token", values["QAG_SECRET_KEY"]} {
		values["QAG_MCP_TOKEN"] = value
		if _, err := LoadConfig(func(k string) string { return values[k] }); err != nil {
			t.Fatal("obsolete token changed startup", err)
		}
	}
	raw, _ := json.Marshal(FileConfig{Version: 1, Values: values})
	file := filepath.Join(t.TempDir(), "installation.json")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfigEnv(func(k string) (string, bool) {
		if k == "QAG_CONFIG_FILE" {
			return file, true
		}
		if k == "QAG_MCP_TOKEN" {
			return "obsolete", true
		}
		return "", false
	}); err != nil {
		t.Fatal("old config token still required", err)
	}
}
