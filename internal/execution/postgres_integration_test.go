//go:build integration

package execution

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"qag/internal/catalog"
	"qag/internal/resources"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLTargetParametersTypesAndLimits(t *testing.T) {
	cfg, err := pgx.ParseConfig(os.Getenv("QAG_TEST_DATABASE_URL"))
	if err != nil || cfg.Database != "qag_test" {
		t.Fatal("isolated qag_test required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var exists bool
	if err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname='qag_target')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err = admin.Exec(ctx, `CREATE DATABASE qag_target`); err != nil {
			t.Fatal(err)
		}
	}
	targetCfg := cfg.Copy()
	targetCfg.Database = "qag_target"
	target, err := pgx.ConnectConfig(ctx, targetCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close(ctx)
	res := resources.Resolved{Resource: resources.Resource{Config: resources.Config{Address: cfg.Host + ":" + fmtPort(cfg.Port), Database: "qag_target", Username: cfg.User, TLS: resources.TLSConfig{Mode: "plaintext"}}}, Secrets: map[string]string{"PASSWORD": cfg.Password}}
	block := catalog.Block{Kind: "postgresql", SQL: &catalog.SQLRequest{Mode: "rows", Query: `SELECT $1::text AS name, NULL::text AS missing, 9007199254740993123.125::numeric AS amount, '{"n":9007199254740993123}'::jsonb AS payload`, Parameters: []catalog.TypedValue{{Type: "string", Value: "O'Reilly; DROP TABLE ignored"}}}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "name", Type: "text"}, {Name: "missing", Type: "text"}, {Name: "amount", Type: "numeric"}, {Name: "payload", Type: "jsonb"}}, Rows: [][]catalog.TypedValue{{{Type: "string", Value: "O'Reilly; DROP TABLE ignored"}, {Type: "null"}, {Type: "decimal", Value: "9007199254740993123.125"}, {Type: "json", Value: `{"n":9007199254740993123}`}}}}}
	runner := NewRunner()
	state, result := runner.Execute(ctx, "sql", block, res)
	if state != "completed" || result.Comparison.Status != "match" {
		t.Fatalf("typed SQL: %s %+v", state, result)
	}
	name := "fixture_" + removeDashes(catalog.NewID())
	if _, err = target.Exec(ctx, `CREATE TABLE `+name+`(value text)`); err != nil {
		t.Fatal(err)
	}
	defer target.Exec(context.Background(), `DROP TABLE `+name)
	expected := "1"
	block.SQL = &catalog.SQLRequest{Mode: "exec", Query: `INSERT INTO ` + name + `(value) VALUES($1)`, Parameters: []catalog.TypedValue{{Type: "string", Value: "persisted"}}}
	block.Expected = catalog.Expectation{BodyMode: "exec", RowsAffected: &expected}
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "completed" || result.Comparison.Status != "match" {
		t.Fatalf("SQL write: %+v", result)
	}
	var count int
	if err = target.QueryRow(ctx, `SELECT count(*) FROM `+name).Scan(&count); err != nil || count != 1 {
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
	if err = target.QueryRow(ctx, `SELECT to_regnamespace('qag') IS NOT NULL`).Scan(&metadataExists); err != nil || metadataExists {
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
	role := "fixture_role_" + removeDashes(catalog.NewID())
	password := catalog.NewID()
	if _, err = target.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN PASSWORD '`+password+`'`); err != nil {
		t.Fatal(err)
	}
	defer target.Exec(context.Background(), `DROP ROLE `+pgx.Identifier{role}.Sanitize())
	res.Resource.Config.Username = role
	res.Secrets = map[string]string{"PASSWORD": password}
	block.SQL.Query = `SELECT * FROM ` + name
	state, result = runner.Execute(ctx, "sql", block, res)
	if state != "error" || result.Comparison.Status != "not_compared" {
		t.Fatal("permission error passed")
	}
}

func fmtPort(value uint16) string      { return strconv.Itoa(int(value)) }
func removeDashes(value string) string { return strings.ReplaceAll(value, "-", "") }
