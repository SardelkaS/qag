package resources

import (
	"qag/internal/catalog"
	"testing"
)

func TestSQLVariablesPreserveTypesAndHeadersRejectInjection(t *testing.T) {
	res := Resolved{Variables: map[string]string{"AMOUNT": "9007199254740993123.125"}, Secrets: map[string]string{"TOKEN": "token\r\nInjected: bad"}}
	b := catalog.Block{ID: catalog.NewID(), Resource: "DB", Kind: "postgresql", SQL: &catalog.SQLRequest{Mode: "rows", Query: "SELECT $1::numeric", Parameters: []catalog.TypedValue{{Type: "decimal", Value: "${AMOUNT}"}}}, Expected: catalog.Expectation{BodyMode: "rows"}}
	if err := catalog.ValidateBlock(&b, "block"); err != nil {
		t.Fatal("typed variable cannot be saved")
	}
	prepared, _, err := res.Prepare(b)
	if err != nil || prepared.SQL.Parameters[0].Value != "9007199254740993123.125" || prepared.SQL.Query != b.SQL.Query {
		t.Fatal("SQL parameter expansion lost precision or changed SQL")
	}
	res.Variables["AMOUNT"] = "1/2"
	if _, _, err = res.Prepare(b); err == nil {
		t.Fatal("nondecimal numeric accepted")
	}
	b.SQL.Query = "SELECT ${AMOUNT}"
	if err = catalog.ValidateBlock(&b, "block"); err == nil {
		t.Fatal("SQL interpolation accepted")
	}
	code := 200
	b = catalog.Block{ID: catalog.NewID(), Resource: "GW", Kind: "http", HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/", BodyMode: "none"}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "none"}}
	res.Resource.Config.Headers = []catalog.KeyValue{{Name: "Authorization", Value: "${TOKEN}"}}
	if _, _, err = res.Prepare(b); err == nil {
		t.Fatal("expanded resource header injection accepted")
	}
}
