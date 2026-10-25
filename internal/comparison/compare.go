package comparison

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"

	"qag/internal/catalog"
)

type Actual struct {
	StatusCode       *int
	GRPCCode         string
	Headers          []catalog.KeyValue
	Body             string
	Columns          []catalog.Column
	Rows             [][]catalog.TypedValue
	RowsAffected     *string
	IncompleteReason string
}

func Compare(block catalog.Block, actual Actual) Result {
	result := Result{Status: "match", Differences: []Difference{}}
	if actual.IncompleteReason != "" {
		result.Status = "not_compared"
		result.Reason = actual.IncompleteReason
		return result
	}
	add := func(path, expected, got string) {
		result.Status = "mismatch"
		if len(result.Differences) < 100 {
			result.Differences = append(result.Differences, Difference{path, expected, got})
		} else {
			result.DifferencesTruncated = true
		}
	}
	if block.Kind == "http" {
		if actual.StatusCode == nil || block.Expected.StatusCode == nil {
			result.Status = "not_compared"
			result.Reason = "Нет полного HTTP-статуса"
			return result
		}
		if *block.Expected.StatusCode != *actual.StatusCode {
			add("status", fmt.Sprint(*block.Expected.StatusCode), fmt.Sprint(*actual.StatusCode))
		}
	} else if block.Kind == "grpc" {
		if block.Expected.GRPCCode != actual.GRPCCode {
			add("grpc.code", block.Expected.GRPCCode, actual.GRPCCode)
		}
	}
	for _, expected := range block.Expected.Headers {
		found := false
		for _, header := range actual.Headers {
			if strings.EqualFold(expected.Name, header.Name) && header.Value == expected.Value {
				found = true
				break
			}
		}
		if !found {
			add("headers."+expected.Name, expected.Value, "отсутствует или отличается")
		}
	}
	switch block.Expected.BodyMode {
	case "none":
	case "text":
		if normalizeText(block.Expected.Body) != normalizeText(actual.Body) {
			add("body", block.Expected.Body, actual.Body)
		}
	case "json":
		expected, err := decodeJSON(block.Expected.Body)
		if err != nil {
			result.Status = "not_compared"
			result.Reason = "Некорректный ожидаемый JSON"
			return result
		}
		got, err := decodeJSON(actual.Body)
		if err != nil {
			result.Status = "not_compared"
			result.Reason = "Ответ не является корректным JSON"
			return result
		}
		diffJSON("body", expected, got, add)
	case "exec":
		if block.Expected.RowsAffected == nil || actual.RowsAffected == nil {
			result.Status = "not_compared"
			result.Reason = "Нет числа затронутых строк"
			return result
		}
		if !sameNumber(*block.Expected.RowsAffected, *actual.RowsAffected) {
			add("rowsAffected", *block.Expected.RowsAffected, *actual.RowsAffected)
		}
	case "rows":
		if len(block.Expected.Columns) != len(actual.Columns) {
			add("columns.length", fmt.Sprint(len(block.Expected.Columns)), fmt.Sprint(len(actual.Columns)))
		}
		for i := 0; i < min(len(block.Expected.Columns), len(actual.Columns)); i++ {
			e, a := block.Expected.Columns[i], actual.Columns[i]
			if e != a {
				add(fmt.Sprintf("columns[%d]", i), e.Name+":"+e.Type, a.Name+":"+a.Type)
			}
		}
		if len(block.Expected.Rows) != len(actual.Rows) {
			add("rows.length", fmt.Sprint(len(block.Expected.Rows)), fmt.Sprint(len(actual.Rows)))
		}
		for row := 0; row < min(len(block.Expected.Rows), len(actual.Rows)); row++ {
			e, a := block.Expected.Rows[row], actual.Rows[row]
			if len(e) != len(a) {
				add(fmt.Sprintf("rows[%d].length", row), fmt.Sprint(len(e)), fmt.Sprint(len(a)))
			}
			for col := 0; col < min(len(e), len(a)); col++ {
				p := fmt.Sprintf("rows[%d][%d]", row, col)
				if e[col].Type != a[col].Type {
					add(p, show(e[col]), show(a[col]))
					continue
				}
				switch e[col].Type {
				case "integer", "decimal":
					if !sameNumber(e[col].Value, a[col].Value) {
						add(p, show(e[col]), show(a[col]))
					}
				case "json":
					ev, ee := decodeJSON(e[col].Value)
					av, ae := decodeJSON(a[col].Value)
					if ee != nil || ae != nil {
						add(p, show(e[col]), show(a[col]))
					} else {
						diffJSON(p, ev, av, add)
					}
				default:
					if e[col].Value != a[col].Value {
						add(p, show(e[col]), show(a[col]))
					}
				}
			}
		}
	default:
		result.Status = "not_compared"
		result.Reason = "Неизвестный формат ожидания"
	}
	return result
}

func show(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}
func normalizeText(text string) string { return strings.ReplaceAll(text, "\r\n", "\n") }
func decodeJSON(text string) (any, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("extra JSON data")
	}
	return result, nil
}
func sameNumber(a, b string) bool {
	first, ok1 := new(big.Rat).SetString(a)
	second, ok2 := new(big.Rat).SetString(b)
	return ok1 && ok2 && first.Cmp(second) == 0
}
func diffJSON(path string, expected, actual any, add func(string, string, string)) {
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			add(path, show(expected), show(actual))
			return
		}
		keys := []string{}
		seen := map[string]bool{}
		for key := range e {
			seen[key] = true
		}
		for key := range a {
			seen[key] = true
		}
		for key := range seen {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			ev, hasE := e[key]
			av, hasA := a[key]
			p := path + "." + key
			if !hasE {
				add(p, "отсутствует", show(av))
			} else if !hasA {
				add(p, show(ev), "отсутствует")
			} else {
				diffJSON(p, ev, av, add)
			}
		}
	case []any:
		a, ok := actual.([]any)
		if !ok {
			add(path, show(expected), show(actual))
			return
		}
		if len(e) != len(a) {
			add(path+".length", fmt.Sprint(len(e)), fmt.Sprint(len(a)))
		}
		for i := 0; i < min(len(e), len(a)); i++ {
			diffJSON(fmt.Sprintf("%s[%d]", path, i), e[i], a[i], add)
		}
	case json.Number:
		a, ok := actual.(json.Number)
		if !ok || !sameNumber(string(e), string(a)) {
			add(path, show(expected), show(actual))
		}
	default:
		if show(expected) != show(actual) {
			add(path, show(expected), show(actual))
		}
	}
}
