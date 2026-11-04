package resources

import (
	"github.com/SardelkaS/qag/internal/catalog"
	"strings"
	"testing"
)

func TestPrepareTypedFieldsAndRejectMissingVariables(t *testing.T) {
	status := 200
	res := Resolved{Resource: Resource{Config: Config{}}, Variables: map[string]string{"TASK_ID": "a/b ?", "ALIAS": "${TASK_ID}"}, Secrets: map[string]string{"TOKEN": "test-token"}}
	block := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "AI_GW_SERVISE", HTTP: &catalog.HTTPRequest{Method: "POST", Path: "AI_GW_SERVISE/task/${ALIAS}", BodyMode: "json", Body: `{"id":"${TASK_ID}","n":9007199254740993123}`, Headers: []catalog.RequestHeader{{Name: "Authorization", Value: "Bearer ${TOKEN}"}}}, Expected: catalog.Expectation{StatusCode: &status, BodyMode: "none"}}
	prepared, _, err := res.Prepare(block)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HTTP.Path != "task/a%2Fb%20%3F" || prepared.HTTP.Headers[0].Value != "Bearer test-token" || !strings.Contains(prepared.HTTP.Body, "9007199254740993123") {
		t.Fatalf("bad preparation: %+v", prepared.HTTP)
	}
	if block.HTTP.Headers[0].Value != "Bearer ${TOKEN}" {
		t.Fatal("source snapshot mutated")
	}
	block.HTTP.Path = "/${MISSING}"
	if _, _, err = res.Prepare(block); err == nil {
		t.Fatal("missing variable accepted")
	}
	block.HTTP.Path = "/${TOKEN}"
	if _, _, err = res.Prepare(block); err == nil {
		t.Fatal("secret exposed in URL")
	}
	res.Variables["A"] = "${B}"
	res.Variables["B"] = "${A}"
	if _, err = res.Expand("${A}", false, nil); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestPrepareResearchIgnoresLegacyExpectationVariables(t *testing.T) {
	res := Resolved{Resource: Resource{Config: Config{}}, Variables: map[string]string{"ID": "task"}, Secrets: map[string]string{}}
	code := 200
	b := catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "DEMO", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/${ID}", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "json", Body: `{"value":"${EXPECTED_ONLY}"}`}}
	if _, _, err := res.Prepare(b); err == nil {
		t.Fatal("original missing expectation variable accepted")
	}
	prepared, _, err := res.PrepareRequest(b)
	if err != nil || prepared.HTTP.Path != "/task" || prepared.Expected.Body != "" {
		t.Fatal("research inherited expectation", err)
	}
	if b.Expected.Body == "" {
		t.Fatal("old snapshot rewritten")
	}
	b.HTTP.Path = "/${MISSING}"
	if _, _, err = res.PrepareRequest(b); err == nil {
		t.Fatal("missing request variable accepted")
	}
}
