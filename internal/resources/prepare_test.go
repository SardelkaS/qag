package resources

import (
	"qag/internal/catalog"
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
