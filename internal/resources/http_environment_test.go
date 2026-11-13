package resources

import (
	"encoding/json"
	"github.com/SardelkaS/qag/internal/catalog"
	"testing"
)

func TestLegacyEnvironmentVariableShape(t *testing.T) {
	var env Environment
	if err := json.Unmarshal([]byte(`{"id":"legacy","variables":{"ITEM":"abc"},"secretNames":[]}`), &env); err != nil {
		t.Fatal(err)
	}
	if env.Variables["ITEM"] != "abc" || len(env.HTTPVariables) != 0 {
		t.Fatalf("changed legacy environment: %+v", env)
	}
	b, _ := json.Marshal(env)
	var value map[string]json.RawMessage
	_ = json.Unmarshal(b, &value)
	var variables map[string]string
	if err := json.Unmarshal(value["variables"], &variables); err != nil || variables["ITEM"] != "abc" {
		t.Fatalf("changed string map: %s", b)
	}
}

func TestEnvironmentHTTPNamesDependenciesAndOptionalWrite(t *testing.T) {
	source := catalog.HTTPHeaderSource{Method: "POST", URL: "https://auth.example.test/token", TLS: TLSConfig{Mode: "tls"}, BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "${AUTH_SECRET}"}}, JSONPointer: "/token", FetchMode: "per_run"}
	sources := map[string]catalog.HTTPHeaderSource{"AUTH": source}
	secrets := map[string]string{"AUTH_SECRET": "private"}
	if err := EnvironmentVariableContext(nil, sources, secrets); err != nil {
		t.Fatal(err)
	}
	if EnvironmentVariableContext(map[string]string{"AUTH": "literal"}, sources, secrets) == nil {
		t.Fatal("literal/http collision")
	}
	if EnvironmentVariableContext(nil, sources, map[string]string{"AUTH": "secret", "AUTH_SECRET": "private"}) == nil {
		t.Fatal("http/secret collision")
	}
	if VariableContext(Config{Variables: map[string]VariableDefinition{"AUTH": {Mode: "literal"}}}, nil, secrets, sources) == nil {
		t.Fatal("local/http collision")
	}
	if VariableContext(Config{}, nil, map[string]string{"AUTH": "local secret"}, sources) == nil {
		t.Fatal("local secret/http collision")
	}
	copy := source
	copy.Form = []catalog.KeyValue{{Name: "dependency", Value: "${AUTH}"}}
	if EnvironmentVariableContext(nil, map[string]catalog.HTTPHeaderSource{"AUTH": copy}, nil) == nil {
		t.Fatal("direct HTTP dependency")
	}
	copy.Form[0].Value = "${ALIAS}"
	if EnvironmentVariableContext(map[string]string{"ALIAS": "${AUTH}"}, map[string]catalog.HTTPHeaderSource{"AUTH": copy}, nil) == nil {
		t.Fatal("transitive HTTP dependency")
	}
	copy.Form[0].Value = "${RESOURCE_ONLY}"
	if EnvironmentVariableContext(nil, map[string]catalog.HTTPHeaderSource{"AUTH": copy}, nil) == nil {
		t.Fatal("resource-only secret accepted")
	}
	var write struct {
		HTTPVariables HTTPVariables `json:"httpVariables,omitempty"`
	}
	if json.Unmarshal([]byte(`{}`), &write) != nil || write.HTTPVariables != nil {
		t.Fatal("omitted write changed")
	}
	if json.Unmarshal([]byte(`{"httpVariables":{}}`), &write) != nil || write.HTTPVariables == nil {
		t.Fatal("empty write indistinguishable")
	}
	if json.Unmarshal([]byte(`{"httpVariables":null}`), &write) == nil {
		t.Fatal("null accepted")
	}
	raw := NormalizeVariableWriteJSON([]byte(`{"httpVariables":{" ":123}}`))
	if json.Unmarshal(raw, &write) != nil || len(write.HTTPVariables) != 0 {
		t.Fatal("unnamed source validated")
	}
}
