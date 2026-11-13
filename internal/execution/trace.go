package execution

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/comparison"
	"github.com/SardelkaS/qag/internal/resources"
)

// TraceRecorder receives only already-masked projections.
type TraceRecorder interface {
	BeginTrace(context.Context, *CallLog) error
	EndTrace(context.Context, CallLog) error
}
type traceKey struct{}
type sourceTraceKey struct{}
type traceSession struct {
	recorder TraceRecorder
	mu       sync.Mutex
	known    map[string]string
}
type SourceTrace struct {
	Stage     string
	Name      string
	Source    *catalog.HTTPHeaderSource
	LogID     string
	ValueType string
}

func WithTraceRecorder(ctx context.Context, r TraceRecorder) context.Context {
	return context.WithValue(ctx, traceKey{}, &traceSession{recorder: r, known: map[string]string{}})
}

func addTraceSecrets(ctx context.Context, values map[string]string) {
	if s, _ := ctx.Value(traceKey{}).(*traceSession); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k, v := range values {
			s.known[k] = v
		}
	}
}
func traceSecrets(ctx context.Context, values map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		out[k] = v
	}
	if s, _ := ctx.Value(traceKey{}).(*traceSession); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		for k, v := range s.known {
			out["trace-known-"+k] = v
		}
	}
	return out
}
func WithSourceTrace(ctx context.Context, s *SourceTrace) context.Context {
	return context.WithValue(ctx, sourceTraceKey{}, s)
}

func diagnosticTime() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
func clipDiagnostic(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	s = s[:limit]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s, true
}
func safeHeaders(values []catalog.KeyValue, redactor *resources.Redactor) []catalog.KeyValue {
	out := make([]catalog.KeyValue, 0, len(values))
	for _, h := range values {
		v := redactor.Mask(h.Value)
		switch strings.ToLower(h.Name) {
		case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
			v = "[скрыто]"
		}
		out = append(out, catalog.KeyValue{Name: redactor.Mask(h.Name), Value: v})
	}
	return out
}

func safeLog(l CallLog, secrets map[string]string) CallLog {
	b, _ := json.Marshal(l)
	var out CallLog
	_ = json.Unmarshal(b, &out)
	r := resources.NewRedactor(secrets)
	q := &out.Request
	q.Address = r.Mask(q.Address)
	q.Method = r.Mask(q.Method)
	q.URL = r.Mask(q.URL)
	q.Headers = safeHeaders(q.Headers, r)
	q.Body = r.Mask(q.Body)
	q.SQL = r.Mask(q.SQL)
	for i := range q.Parameters {
		q.Parameters[i].Value = r.Mask(q.Parameters[i].Value)
	}
	out.Summary.SourceName = r.Mask(out.Summary.SourceName)
	out.Summary.EnvironmentName = r.Mask(out.Summary.EnvironmentName)
	out.Summary.ResourceName = r.Mask(out.Summary.ResourceName)
	if out.Response != nil {
		MaskResult(out.Response, r)
		out.Response.Headers = safeHeaders(out.Response.Headers, r)
		out.Response.Trailers = safeHeaders(out.Response.Trailers, r)
	}
	a, _ := json.Marshal(out)
	out.Summary.Redacted = out.Summary.Redacted || string(a) != string(b)
	q.Body, q.Truncated = clipDiagnostic(q.Body, 1<<20)
	if len(q.SQL) > 1<<20 {
		q.SQL, _ = clipDiagnostic(q.SQL, 1<<20)
		q.Truncated = true
	}
	if out.Response != nil {
		body, truncated := clipDiagnostic(out.Response.Body, MaxResponseBytes)
		out.Response.Body = body
		out.Response.Truncated = out.Response.Truncated || truncated
	}
	return out
}

// beginExchange is called once the concrete request has been prepared, before
// transport I/O. Source response masking happens after extraction, in memory.
func beginExchange(ctx context.Context, request LogRequest, secrets map[string]string) (func(), func(Result, error) error, error) {
	session, _ := ctx.Value(traceKey{}).(*traceSession)
	if session == nil {
		return func() {}, func(Result, error) error { return nil }, nil
	}
	recorder := session.recorder
	secrets = traceSecrets(ctx, secrets)
	source, _ := ctx.Value(sourceTraceKey{}).(*SourceTrace)
	stage, name := "main", ""
	if source != nil {
		stage, name = source.Stage, source.Name
	}
	l := CallLog{Summary: LogSummary{ID: catalog.NewID(), Protocol: request.Protocol, Stage: stage, SourceName: name, State: "running", StartedAt: diagnosticTime()}, Request: request}
	l = safeLog(l, secrets)
	if err := recorder.BeginTrace(ctx, &l); err != nil {
		return nil, nil, err
	}
	if source != nil {
		source.LogID = l.Summary.ID
	}
	sent := false
	finish := func(result Result, err error) error {
		l.Summary.RequestSent = sent
		t := diagnosticTime()
		l.Summary.FinishedAt = &t
		l.Summary.DurationMS = t.Sub(l.Summary.StartedAt).Milliseconds()
		l.Summary.State = "completed"
		masks := map[string]string{}
		for k, v := range secrets {
			masks[k] = v
		}
		if source != nil && source.Source != nil {
			s := *source.Source
			value, extractErr := extractHeaderValue(result.Body, s.JSONPointer)
			if extractErr == nil {
				masks["trace-scalar"] = value
				masks["trace-formatted"] = s.Prefix + value + s.Suffix
				addTraceSecrets(ctx, map[string]string{l.Summary.ID + "-scalar": value, l.Summary.ID + "-formatted": s.Prefix + value + s.Suffix})
				if len(value) > 64<<10 || len(s.Prefix+value+s.Suffix) > 64<<10 {
					extractErr = sourceError("Значение источника превышает 64 KiB")
				}
			}
			if result.Truncated || extractErr != nil {
				result.Body = ""
				l.BodyOmittedReason = "Тело источника пропущено: невозможно безопасно обработать выбранное JSON-поле"
			} else {
				result.Body, _ = maskSourceBody(result.Body, s.JSONPointer)
				result.Body = maskCredentialJSON(result.Body)
			}
			if err == nil {
				_, _, err = sourceValue(result, nil)
				if err == nil {
					err = extractErr
				}
			}
		}
		if err != nil {
			l.Summary.State = "error"
			result.Error = err.Error()
			if result.ErrorCode == "" {
				result.ErrorCode = "request_failed"
			}
		}
		if ctx.Err() == context.DeadlineExceeded {
			l.Summary.State = "timed_out"
			result.ErrorCode = "timed_out"
			result.Error = "Превышено время выполнения; внешний эффект неизвестен"
		} else if ctx.Err() == context.Canceled {
			l.Summary.State = "cancelled"
			result.ErrorCode = "cancelled"
			result.Error = "Вызов отменён; внешний эффект неизвестен"
		}
		result.Comparison = comparison.Result{Status: "not_compared", Reason: "Журнал фактического вызова", Differences: []comparison.Difference{}}
		l.Response = &result
		l = safeLog(l, masks)
		return recorder.EndTrace(ctx, l)
	}
	return func() { sent = true }, finish, nil
}

func maskSourceBody(body, pointer string) (string, error) {
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	var root any
	if err := d.Decode(&root); err != nil {
		return "", err
	}
	if pointer == "" {
		return `"[скрыто]"`, nil
	}
	tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	value := root
	for i, token := range tokens {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		last := i == len(tokens)-1
		switch node := value.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return "", sourceError("Поле отсутствует")
			}
			if last {
				node[token] = "[скрыто]"
			} else {
				value = next
			}
		case []any:
			n, err := strconv.Atoi(token)
			if err != nil || n < 0 || n >= len(node) {
				return "", sourceError("Неверный индекс")
			}
			if last {
				node[n] = "[скрыто]"
			} else {
				value = node[n]
			}
		default:
			return "", sourceError("Неверный JSON Pointer")
		}
	}
	b, err := json.Marshal(root)
	return string(b), err
}

func sourceValue(actual Result, err error) (string, *int, error) {
	// Extraction is performed by the caller with its own JSON Pointer.
	if err != nil {
		return "", actual.StatusCode, err
	}
	if actual.Truncated {
		return "", actual.StatusCode, sourceError("Ответ источника превышает 5 MiB")
	}
	if actual.StatusCode == nil || *actual.StatusCode < 200 || *actual.StatusCode >= 300 {
		return "", actual.StatusCode, sourceError("Источник вернул HTTP-статус вне 2xx")
	}
	return "", actual.StatusCode, nil
}

type sourceError string

func (e sourceError) Error() string { return string(e) }

func maskCredentialJSON(body string) string {
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	var root any
	if d.Decode(&root) != nil {
		return ""
	}
	var visit func(any)
	visit = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for key, child := range node {
				normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
				switch normalized {
				case "token", "access_token", "refresh_token", "id_token", "client_secret", "password", "secret", "private_key", "authorization", "api_key", "apikey":
					node[key] = "[скрыто]"
				default:
					visit(child)
				}
			}
		case []any:
			for _, child := range node {
				visit(child)
			}
		}
	}
	visit(root)
	b, _ := json.Marshal(root)
	return string(b)
}
