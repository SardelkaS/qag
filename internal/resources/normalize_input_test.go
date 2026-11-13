package resources

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnnamedRowsAreRemovedBeforeValueValidation(t *testing.T) {
	raw := []byte(`{"revision":9007199254740993123,"variables":{" ":123,"KEEP":""},"config":{"variables":{"":null," \t":{"mode":"invalid","source":123},"REAL":{"mode":"http","source":{"body":"{\"n\":9007199254740993123.125}"}}}},"secrets":{"replace":{" ":false,"KEY":"secret"},"clear":[""," ","OLD"]},"unknown":"must survive for strict rejection"}`)
	normalized := NormalizeVariableWriteJSON(raw)
	if !strings.Contains(string(normalized), `9007199254740993123`) || !strings.Contains(string(normalized), `9007199254740993123.125`) || !strings.Contains(string(normalized), `"unknown"`) {
		t.Fatal("wire values changed")
	}
	var input struct {
		Variables map[string]string
		Config    struct{ Variables map[string]VariableDefinition }
		Secrets   SecretChanges
	}
	if err := json.Unmarshal(normalized, &input); err != nil {
		t.Fatal(err)
	}
	if len(input.Variables) != 1 || len(input.Config.Variables) != 1 || len(input.Secrets.Replace) != 1 || strings.Join(input.Secrets.Clear, ",") != "OLD" {
		t.Fatal("blank names survived")
	}
}
