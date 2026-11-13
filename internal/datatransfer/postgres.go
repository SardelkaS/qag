// Package datatransfer moves complete dataset graphs without starting an Engine.
package datatransfer

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/storage"
	"github.com/jackc/pgx/v5"
)

type PostgreSQL struct {
	Store *storage.Store
	Vault *resources.Vault
}

var tables = []string{"qag.folders", "qag.test_cases", "qag.environments", "qag.environment_versions", "qag.resources", "qag.resource_versions", "qag.runs", "qag.run_resource_versions", "qag.run_steps", "qag.call_attempts", "qag.expected_images", "qag.run_call_variants", "qag.run_call_variant_versions", "qag.run_batches", "qag.run_batch_items", "qag.builtin_demo", "qag_demo.items"}
var boundary1 = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
var boundary2 = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func column(name string) string {
	return strings.ToLower(boundary2.ReplaceAllString(boundary1.ReplaceAllString(name, `${1}_${2}`), `${1}_${2}`))
}
func nullable(name, table string) bool {
	return name == "RunID" && table == "qag.call_attempts" || name == "VariantID" || name == "VariantVersionID" || name == "BatchID" && table == "qag.call_attempts" || name == "AttemptID" || name == "StepID" && table == "qag.run_batches"
}

func (p *PostgreSQL) Snapshot(ctx context.Context) (dataset.Document, error) {
	ready, e := p.Store.CheckSchema(ctx)
	if e != nil {
		return dataset.Document{}, e
	}
	if !ready {
		return dataset.Empty(), nil
	}
	tx, e := p.Store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return dataset.Document{}, problem.Database(e)
	}
	defer tx.Rollback(context.Background())
	doc := dataset.Empty()
	groups := reflect.ValueOf(&doc.Data).Elem()
	for i, table := range tables {
		out := groups.Field(i)
		rt := out.Type().Elem()
		selects := []string{}
		for j := 0; j < rt.NumField(); j++ {
			f := rt.Field(j)
			col := column(f.Name)
			if f.Name == "BatchPosition" {
				selects = append(selects, "coalesce(batch_position,0)")
				continue
			}
			if f.Name == "Secrets" {
				selects = append(selects, "encrypted_secrets")
				continue
			}
			if f.Type == reflect.TypeFor[dataset.JSON]() {
				selects = append(selects, "coalesce("+col+"::text,'')")
				continue
			}
			if nullable(f.Name, table) {
				selects = append(selects, "coalesce("+col+"::text,'')")
				continue
			}
			if f.Name == "Amount" || strings.HasSuffix(f.Name, "ID") {
				col += "::text"
			}
			selects = append(selects, col)
		}
		rows, e := tx.Query(ctx, p.Store.Namespace.SQL("SELECT "+strings.Join(selects, ",")+" FROM "+table))
		if e != nil {
			return doc, problem.Database(e)
		}
		for rows.Next() {
			if out.Len() >= dataset.MaxRecords {
				rows.Close()
				return doc, problem.Invalid("data", "Слишком много записей для переносимого YAML")
			}
			row := reflect.New(rt).Elem()
			dest := []any{}
			var sealed []byte
			for j := 0; j < rt.NumField(); j++ {
				f := rt.Field(j)
				if f.Name == "Secrets" {
					dest = append(dest, &sealed)
				} else {
					dest = append(dest, row.Field(j).Addr().Interface())
				}
			}
			if e = rows.Scan(dest...); e != nil {
				rows.Close()
				return doc, problem.Database(e)
			}
			for k := 0; k < row.NumField(); k++ {
				f := row.Field(k)
				if f.Type() == reflect.TypeFor[time.Time]() {
					f.Set(reflect.ValueOf(f.Interface().(time.Time).UTC()))
				}
				if f.Type() == reflect.TypeFor[*time.Time]() && !f.IsNil() {
					v := f.Interface().(*time.Time).UTC()
					f.Set(reflect.ValueOf(&v))
				}
			}
			if f := row.FieldByName("Secrets"); f.IsValid() {
				owner := "EnvironmentID"
				if table == "qag.resource_versions" {
					owner = "ResourceID"
				}
				values, e := p.Vault.Open(row.FieldByName(owner).String()+":"+row.FieldByName("ID").String(), sealed)
				if e != nil {
					rows.Close()
					return doc, e
				}
				f.Set(reflect.ValueOf(values))
			}
			out.Set(reflect.Append(out, row))
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return doc, problem.Database(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return doc, problem.Database(e)
	}
	if e = dataset.Validate(doc); e != nil {
		return doc, e
	}
	return doc, nil
}

// Import requires the same instance lock as serve. No user record is changed
// before the full merge has passed validation.
func (p *PostgreSQL) Import(ctx context.Context, input dataset.Document, dry bool) (dataset.Summary, error) {
	ready, e := p.Store.CheckSchema(ctx)
	if e != nil {
		return nil, e
	}
	if e = p.Store.CheckImportPermissions(ctx, ready); e != nil {
		return nil, e
	}
	current, e := p.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	merged, summary, e := dataset.Merge(current, input)
	if e != nil || dry {
		return summary, e
	}
	added := 0
	for _, c := range summary {
		added += c.Added
	}
	if added == 0 {
		return summary, nil
	}
	if e = p.Store.Migrate(ctx); e != nil {
		return summary, e
	}
	tx, e := p.Store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if e != nil {
		return summary, problem.Database(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED"); e != nil {
		return summary, problem.Database(e)
	}
	if !ready && len(merged.Data.DemoItems) > 0 {
		if _, e = tx.Exec(ctx, p.Store.Namespace.SQL("DELETE FROM qag_demo.items")); e != nil {
			return summary, problem.Database(e)
		}
	}
	before, after := reflect.ValueOf(current.Data), reflect.ValueOf(merged.Data)
	for _, i := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 9, 15, 16} {
		table := tables[i]
		src, old := after.Field(i), before.Field(i)
		existing := map[string]bool{}
		for j := 0; j < old.Len(); j++ {
			existing[dataset.RecordKey(old.Index(j).Interface())] = true
		}
		for j := 0; j < src.Len(); j++ {
			row := src.Index(j)
			if existing[dataset.RecordKey(row.Interface())] {
				continue
			}
			columns := []string{}
			params := []string{}
			values := []any{}
			for k := 0; k < row.NumField(); k++ {
				f := row.Type().Field(k)
				v := row.Field(k)
				col := column(f.Name)
				var value any = v.Interface()
				param := fmt.Sprintf("$%d", len(values)+1)
				if f.Name == "Secrets" {
					col = "encrypted_secrets"
					owner := "EnvironmentID"
					if table == "qag.resource_versions" {
						owner = "ResourceID"
					}
					value, e = p.Vault.Seal(row.FieldByName(owner).String()+":"+row.FieldByName("ID").String(), value.(map[string]string))
					if e != nil {
						return summary, e
					}
				}
				if f.Type == reflect.TypeFor[dataset.JSON]() {
					if v.String() == "" {
						value = nil
					} else {
						value = json.RawMessage(v.String())
					}
				}
				if f.Type == reflect.TypeFor[dataset.Binary]() {
					value = []byte(value.(dataset.Binary))
				}
				if nullable(f.Name, table) && v.String() == "" {
					value = nil
				}
				if f.Name == "BatchPosition" && row.FieldByName("BatchID").String() == "" {
					value = nil
				}
				if f.Name == "Amount" {
					param += "::text::numeric"
				}
				columns = append(columns, col)
				params = append(params, param)
				values = append(values, value)
			}
			statement := "INSERT INTO " + table + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(params, ",") + ")"
			if _, e = tx.Exec(ctx, p.Store.Namespace.SQL(statement), values...); e != nil {
				return summary, problem.Database(e)
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return summary, problem.Database(e)
	}
	return summary, nil
}
