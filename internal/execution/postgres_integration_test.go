//go:build integration

package execution

import "qag/internal/dbscope"

import (
	"context"
	"github.com/jackc/pgx/v5"
	"qag/internal/testfixture"

	"qag/internal/catalog"
	"qag/internal/resources"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLTargetParametersTypesAndLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := testfixture.ForTest(t, ctx)
	targetCfg, err := pgx.ParseConfig(fixture.TargetURL)
	if err != nil {
		t.Fatal(err)
	}
	target, err := pgx.ConnectConfig(ctx, targetCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close(ctx)
	res := resources.Resolved{Resource: resources.Resource{Config: resources.Config{Address: targetCfg.Host + ":" + fmtPort(targetCfg.Port), Database: "qag", Username: targetCfg.User, TLS: resources.TLSConfig{Mode: "plaintext"}}}, Secrets: map[string]string{"PASSWORD": targetCfg.Password}}
	block := catalog.Block{Kind: "postgresql", SQL: &catalog.SQLRequest{Mode: "rows", Query: `SELECT $1::text AS name, NULL::text AS missing, 9007199254740993123.125::numeric AS amount, '{"n":9007199254740993123}'::jsonb AS payload`, Parameters: []catalog.TypedValue{{Type: "string", Value: "O'Reilly; DROP TABLE ignored"}}}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "name", Type: "text"}, {Name: "missing", Type: "text"}, {Name: "amount", Type: "numeric"}, {Name: "payload", Type: "jsonb"}}, Rows: [][]catalog.TypedValue{{{Type: "string", Value: "O'Reilly; DROP TABLE ignored"}, {Type: "null"}, {Type: "decimal", Value: "9007199254740993123.125"}, {Type: "json", Value: `{"n":9007199254740993123}`}}}}}
	runner := NewRunner()
	state, result := runner.Execute(ctx, "sql", block, res)
	if state != "completed" || result.Comparison.Status != "match" {
		t.Fatalf("typed SQL: %s %+v", state, result)
	}
	name := pgx.Identifier{fixture.TargetSchema, "values"}.Sanitize()
	if _, err = target.Exec(ctx, dbscope.Render(target, `CREATE TABLE `+name+`(value text)`)); err != nil {
		t.Fatal(err)
	}
	defer target.Exec(context.Background(), dbscope.Render(target, `DROP TABLE `+name))
	expected := "1"
	block.SQL = &catalog.SQLRequest{Mode: "exec", Query: `INSERT INTO ` + name + `(value) VALUES($1)`, Parameters: []catalog.TypedValue{{Type: "string", Value: "persisted"}}}
	block.Expected = catalog.Expectation{BodyMode: "exec", RowsAffected: &expected}
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "completed" || result.Comparison.Status != "match" {
		t.Fatalf("SQL write: %+v", result)
	}
	var count int
	if err = target.QueryRow(ctx, dbscope.Render(target, `SELECT count(*) FROM `+name)).Scan(&count); err != nil || count != 1 {
		t.Fatal("write did not persist")
	}
	block.SQL = &catalog.SQLRequest{Mode: "rows", Query: `SELECT generate_series(1,1001) AS n`}
	block.Expected = catalog.Expectation{BodyMode: "rows"}
	_, result = runner.Execute(ctx, "sql", block, res)
	if !result.Truncated || len(result.Rows) != 1000 || result.Comparison.Status != "not_compared" {
		t.Fatal("row limit failed")
	}
	block.SQL.Query = `SELECT 1; SELECT 2`
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "error" {
		t.Fatal("multiple statements accepted")
	}
	var metadataExists bool
	if err = target.QueryRow(ctx, dbscope.Render(target, `SELECT has_schema_privilege(current_user,'qag','USAGE')`)).Scan(&metadataExists); err != nil || metadataExists {
		t.Fatal("application migrations touched target DB")
	}
	block.SQL = &catalog.SQLRequest{Mode: "rows", Query: `SELECT $1::text AS sql_null, 'null'::jsonb AS json_null`, Parameters: []catalog.TypedValue{{Type: "null"}}}
	block.Expected = catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "sql_null", Type: "text"}, {Name: "json_null", Type: "jsonb"}}, Rows: [][]catalog.TypedValue{{{Type: "null"}, {Type: "json", Value: "null"}}}}
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "completed" || result.Comparison.Status != "match" {
		t.Fatalf("SQL NULL/JSON null lost: %+v", result)
	}
	block.SQL.Query = `SELECT repeat('x', 5242881)`
	block.SQL.Parameters = nil
	_, result = runner.Execute(ctx, "sql", block, res)
	if !result.Truncated || result.Comparison.Status != "not_compared" {
		t.Fatal("SQL byte limit passed")
	}
	block.SQL.Query = `SELECT pg_sleep(5)`
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	state, result = runner.Execute(short, "sql", block, res)
	stop()
	if state != "timed_out" || result.Comparison.Status != "not_compared" {
		t.Fatal("SQL timeout passed")
	}
	short, stop = context.WithCancel(ctx)
	stop()
	state, result = runner.Execute(short, "sql", block, res)
	if state != "cancelled" || result.Comparison.Status != "not_compared" {
		t.Fatal("SQL cancel passed")
	}
	readerCfg, _ := pgx.ParseConfig(fixture.ReaderURL)
	res.Resource.Config.Username = readerCfg.User
	res.Secrets = map[string]string{"PASSWORD": readerCfg.Password}
	block.SQL.Query = `SELECT * FROM ` + name
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "error" || result.Comparison.Status != "not_compared" {
		t.Fatal("permission error passed")
	}
}

func fmtPort(value uint16) string      { return strconv.Itoa(int(value)) }
func removeDashes(value string) string { return strings.ReplaceAll(value, "-", "") }
