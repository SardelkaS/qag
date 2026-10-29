package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequestValidationDoesNotRequireOrReadExpectations(t *testing.T) {
	cases := []Block{
		{Kind: "http", HTTP: &HTTPRequest{Method: "POST", Path: "/task", BodyMode: "json", Body: `{"n":9007199254740993}`}},
		{Kind: "grpc", GRPC: &GRPCRequest{Service: "demo.v1.Service", Method: "Get", Body: `{}`}},
		{Kind: "postgresql", SQL: &SQLRequest{Query: "SELECT $1", Mode: "rows", Parameters: []TypedValue{{Type: "decimal", Value: "9007199254740993.125"}}}},
		{Kind: "postgresql", SQL: &SQLRequest{Query: "UPDATE items SET value=$1", Mode: "exec", Parameters: []TypedValue{{Type: "string", Value: "value"}}}},
	}
	for _, b := range cases {
		t.Run(b.Kind+func() string {
			if b.SQL != nil {
				return b.SQL.Mode
			}
			return ""
		}(), func(t *testing.T) {
			b.ID = NewID()
			b.Resource = "DEMO"
			b.Expected = Expectation{BodyMode: "invalid", Body: "${UNUSED}", Headers: []KeyValue{{Name: "\ninvalid"}}}
			if err := ValidateRequest(&b, "request"); err != nil {
				t.Fatal(err)
			}
			if ValidateBlock(&b, "block") == nil {
				t.Fatal("original expectation checks weakened")
			}
			wire := RequestFromBlock(b, false)
			raw, _ := json.Marshal(wire)
			if strings.Contains(string(raw), `"expected"`) || strings.Contains(string(raw), "UNUSED") {
				t.Fatal("research wire contains expectation")
			}
			copy := wire.Block()
			if ValidateRequest(&copy, "request") != nil {
				t.Fatal("wire request invalid")
			}
		})
	}
	cases[0].Resource = "DEMO"
	cases[0].HTTP.Body = "{invalid"
	if ValidateRequest(&cases[0], "request") == nil {
		t.Fatal("invalid request accepted")
	}
}
