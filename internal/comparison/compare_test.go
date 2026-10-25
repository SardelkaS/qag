package comparison

import (
	"qag/internal/catalog"
	"testing"
)

func TestPreciseJSONAndIncompleteResponses(t *testing.T) {
	status := 200
	for _, tc := range []struct{ name, expected, actual, want string }{
		{"keys", `{"a":1,"b":2}`, `{ "b":2,"a":1.0 }`, "match"},
		{"large integer", `{"n":9007199254740993123}`, `{"n":9007199254740993124}`, "mismatch"},
		{"types", `{"n":1}`, `{"n":"1"}`, "mismatch"},
		{"array order", `[1,2]`, `[2,1]`, "mismatch"},
		{"missing and null", `{"a":null}`, `{}`, "mismatch"},
		{"bad JSON", `{}`, `{broken`, "not_compared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			block := catalog.Block{Kind: "http", Expected: catalog.Expectation{StatusCode: &status, BodyMode: "json", Body: tc.expected}}
			result := Compare(block, Actual{StatusCode: &status, Body: tc.actual})
			if result.Status != tc.want {
				t.Fatalf("got %+v", result)
			}
		})
	}
	block := catalog.Block{Kind: "http", Expected: catalog.Expectation{StatusCode: &status, BodyMode: "none"}}
	if result := Compare(block, Actual{StatusCode: &status, IncompleteReason: "усечён"}); result.Status != "not_compared" {
		t.Fatal("truncated response passed")
	}
	other := 404
	if Compare(block, Actual{StatusCode: &other}).Status != "mismatch" {
		t.Fatal("status ignored")
	}
}

func TestSQLTypesAndExpectedGRPCError(t *testing.T) {
	block := catalog.Block{Kind: "postgresql", Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "value", Type: "text"}}, Rows: [][]catalog.TypedValue{{{Type: "string", Value: ""}}}}}
	actual := Actual{Columns: block.Expected.Columns, Rows: [][]catalog.TypedValue{{{Type: "null"}}}}
	if Compare(block, actual).Status != "mismatch" {
		t.Fatal("NULL equals empty string")
	}
	block.Expected.Rows = [][]catalog.TypedValue{{{Type: "decimal", Value: "9007199254740993123.125"}}}
	actual.Rows = [][]catalog.TypedValue{{{Type: "decimal", Value: "9007199254740993123.1250"}}}
	if Compare(block, actual).Status != "match" {
		t.Fatal("exact decimal comparison failed")
	}
	grpc := catalog.Block{Kind: "grpc", Expected: catalog.Expectation{GRPCCode: "NOT_FOUND", BodyMode: "none"}}
	if Compare(grpc, Actual{GRPCCode: "NOT_FOUND"}).Status != "match" {
		t.Fatal("expected gRPC error is not comparable")
	}
	expected, actualCount := "2", "1"
	if Compare(catalog.Block{Kind: "postgresql", Expected: catalog.Expectation{BodyMode: "exec", RowsAffected: &expected}}, Actual{RowsAffected: &actualCount}).Status != "mismatch" {
		t.Fatal("affected rows ignored")
	}
	actual.Rows = nil
	if Compare(block, actual).Status != "mismatch" {
		t.Fatal("SQL row count ignored")
	}
}
