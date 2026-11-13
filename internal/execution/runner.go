package execution

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/comparison"
	"github.com/SardelkaS/qag/internal/resources"
)

const MaxResponseBytes = 5 << 20

func (r *Runner) Execute(ctx context.Context, scope string, block catalog.Block, res resources.Resolved) (string, Result) {
	return r.execute(ctx, scope, block, res, false)
}

func (r *Runner) ExecuteResearch(ctx context.Context, scope string, block catalog.Block, res resources.Resolved) (string, Result) {
	return r.execute(ctx, scope, block, res, true)
}

func ResearchComparison() comparison.Result {
	return comparison.Result{Status: "not_compared", Reason: "Исследовательский вызов — без сравнения", Differences: []comparison.Difference{}}
}

func (r *Runner) execute(ctx context.Context, scope string, block catalog.Block, res resources.Resolved, research bool) (string, Result) {
	if block.Kind == "postgresql" && res.Resource.ReadOnly && res.Resource.Config.UnavailableReason == "demo_sql_unavailable" {
		message := "Демонстрационная PostgreSQL недоступна в YAML-режиме; пользовательские SQL-ресурсы могут использовать внешнюю БД"
		result := Result{Protocol: block.Kind, Headers: []catalog.KeyValue{}, ErrorCode: "demo_sql_unavailable", Error: message, Comparison: comparison.Result{Status: "not_compared", Reason: message, Differences: []comparison.Difference{}}}
		if research {
			result.Comparison = ResearchComparison()
		}
		return "error", result
	}
	seconds := block.TimeoutSeconds
	if seconds == 0 {
		seconds = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	var result Result
	var err error
	var variables []VariableSourceResult
	if (len(res.Resource.Config.Variables) > 0 || len(res.EnvironmentHTTPVariables) > 0) && !res.Prepared {
		if !res.Deferred {
			if research {
				block, res, err = res.PlanRequest(block)
			} else {
				block, res, err = res.Plan(block)
			}
		}
		if err == nil && !res.Prepared {
			block, res, variables, err = r.resolveVariables(ctx, scope, block, res)
		}
		if err == nil && !res.Prepared {
			if research {
				block, res, err = res.PrepareRequest(block)
			} else {
				block, res, err = res.Prepare(block)
			}
			if err != nil {
				r.invalidateVariableCache(scope, res, variables)
			}
		}
	}
	var traces []HeaderSourceResult
	if err == nil {
		block, res, traces, err = r.resolveHeaders(ctx, scope, block, res)
	}
	sourceFailed := err != nil
	if err == nil {
		switch block.Kind {
		case "http":
			result, err = r.http(ctx, block, res)
		case "grpc":
			result, err = r.grpc(ctx, scope, block, res)
		case "postgresql":
			result, err = r.sql(ctx, block, res)
		default:
			err = fmt.Errorf("неподдерживаемый протокол")
		}
	}
	result.HeaderSources = traces
	result.VariableSources = variables
	if sourceFailed && len(variables) > 0 {
		result.ErrorCode = "variable_source_failed"
	}
	if len(traces) > 0 || len(variables) > 0 {
		started := !sourceFailed
		result.MainRequestStarted = &started
	}
	result.Protocol = block.Kind
	if result.Headers == nil {
		result.Headers = []catalog.KeyValue{}
	}
	state := "completed"
	reason := ""
	if err != nil {
		state = "error"
		result.Error = err.Error()
		reason = "Вызов завершился ошибкой"
	}
	if ctx.Err() == context.DeadlineExceeded {
		state = "timed_out"
		result.Error = "Превышено время выполнения; внешнее действие могло уже выполниться"
		reason = "Тайм-аут"
	} else if ctx.Err() == context.Canceled {
		state = "cancelled"
		result.Error = "Вызов отменён; внешнее действие могло уже выполниться"
		reason = "Отмена"
	}
	if result.Truncated {
		reason = "Результат усечён: превышен лимит данных"
	}
	if research {
		result.Comparison = ResearchComparison()
	} else {
		result.Comparison = comparison.Compare(block, comparison.Actual{StatusCode: result.StatusCode, GRPCCode: result.GRPCCode, Headers: result.Headers, Body: result.Body, Columns: result.Columns, Rows: result.Rows, RowsAffected: result.RowsAffected, IncompleteReason: reason})
	}
	MaskResult(&result, resources.NewRedactor(res.Secrets))
	return state, result
}

func MaskBlock(block catalog.Block, redactor *resources.Redactor) catalog.Block {
	data, _ := json.Marshal(block)
	masked := redactor.Mask(string(data))
	var result catalog.Block
	if json.Unmarshal([]byte(masked), &result) != nil {
		return catalog.Block{ID: block.ID, Kind: block.Kind, Resource: block.Resource, Expected: catalog.Expectation{BodyMode: "none"}}
	}
	return result
}
func MaskResult(result *Result, redactor *resources.Redactor) {
	mask := func(value *string) {
		old := *value
		*value = redactor.Mask(old)
		if old != *value {
			result.Redacted = true
		}
	}
	mask(&result.Body)
	mask(&result.Error)
	mask(&result.StatusMessage)
	mask(&result.Comparison.Reason)
	if result.RowsAffected != nil {
		mask(result.RowsAffected)
	}
	for i := range result.Columns {
		mask(&result.Columns[i].Name)
		mask(&result.Columns[i].Type)
	}
	for i := range result.Headers {
		mask(&result.Headers[i].Name)
		mask(&result.Headers[i].Value)
	}
	for i := range result.Trailers {
		mask(&result.Trailers[i].Name)
		mask(&result.Trailers[i].Value)
	}
	for i := range result.Rows {
		for j := range result.Rows[i] {
			mask(&result.Rows[i][j].Value)
		}
	}
	for i := range result.Comparison.Differences {
		mask(&result.Comparison.Differences[i].Path)
		mask(&result.Comparison.Differences[i].Expected)
		mask(&result.Comparison.Differences[i].Actual)
	}
}

func mergedHeaders(base, override []catalog.RequestHeader) []catalog.RequestHeader {
	return catalog.MergeRequestHeaders(base, override)
}

func headers(values map[string][]string) []catalog.KeyValue {
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []catalog.KeyValue{}
	for _, key := range keys {
		for _, v := range values[key] {
			result = append(result, catalog.KeyValue{Name: key, Value: v})
		}
	}
	return result
}

func (r *Runner) http(ctx context.Context, b catalog.Block, res resources.Resolved) (Result, error) {
	result := Result{Protocol: "http"}
	if err := checkRequestGate(ctx); err != nil {
		return result, err
	}
	base, err := url.Parse(res.Resource.Config.BaseURL)
	if err != nil {
		return result, fmt.Errorf("некорректный HTTP-ресурс")
	}
	relative, err := url.Parse(b.HTTP.Path)
	if err != nil || relative.IsAbs() || relative.Host != "" {
		return result, fmt.Errorf("некорректный путь HTTP")
	}
	rawPath := strings.TrimRight(base.EscapedPath(), "/") + "/" + strings.TrimLeft(relative.EscapedPath(), "/")
	base.Path, err = url.PathUnescape(rawPath)
	if err != nil {
		return result, fmt.Errorf("некорректное кодирование пути")
	}
	base.RawPath = rawPath
	query := relative.Query()
	for _, v := range b.HTTP.Query {
		query.Add(v.Name, v.Value)
	}
	base.RawQuery = query.Encode()
	var body io.Reader
	if b.HTTP.BodyMode != "none" {
		body = strings.NewReader(b.HTTP.Body)
	}
	request, err := http.NewRequestWithContext(ctx, b.HTTP.Method, base.String(), body)
	if err != nil {
		return result, err
	}
	for _, h := range mergedHeaders(res.Resource.Config.Headers, b.HTTP.Headers) {
		request.Header.Add(h.Name, h.Value)
	}
	if b.HTTP.BodyMode == "json" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", "application/json")
	}
	tlsConfig, err := resources.TLS(res.Resource.Config, res.Secrets)
	if err != nil {
		return result, err
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.StatusCode = &response.StatusCode
	result.Headers = headers(response.Header)
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(response.Body)
		if err != nil {
			return result, err
		}
		defer gz.Close()
		reader = gz
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxResponseBytes+1))
	if len(data) > MaxResponseBytes {
		data = data[:MaxResponseBytes]
		result.Truncated = true
	}
	result.Body = string(data)
	return result, err
}
