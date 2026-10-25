package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"qag/internal/catalog"
	"qag/internal/resources"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HeaderSourceResult struct {
	HeaderID   string `json:"headerId"`
	HeaderName string `json:"headerName"`
	FetchMode  string `json:"fetchMode"`
	State      string `json:"state"`
	StatusCode *int   `json:"statusCode,omitempty"`
	DurationMS int64  `json:"durationMs"`
	ErrorCode  string `json:"errorCode,omitempty"`
}
type headerScope struct {
	mu     sync.Mutex
	cache  map[string]string
	values map[string]bool
}

func (r *Runner) scopeHeaders(id string) *headerScope {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.headers == nil {
		r.headers = map[string]*headerScope{}
	}
	state := r.headers[id]
	if state == nil {
		state = &headerScope{cache: map[string]string{}, values: map[string]bool{}}
		r.headers[id] = state
	}
	return state
}
func (r *Runner) ForgetScope(id string) { r.mu.Lock(); defer r.mu.Unlock(); delete(r.headers, id) }
func (r *Runner) ClearHeaderScopes() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = map[string]*headerScope{}
}

func (r *Runner) resolveHeaders(ctx context.Context, scope string, block catalog.Block, res resources.Resolved) (catalog.Block, resources.Resolved, []HeaderSourceResult, error) {
	state := r.scopeHeaders(scope)
	state.mu.Lock()
	defer state.mu.Unlock()
	secrets := map[string]string{}
	for k, v := range res.Secrets {
		secrets[k] = v
	}
	res.Secrets = secrets
	var effective []catalog.RequestHeader
	if block.HTTP != nil {
		copy := *block.HTTP
		block.HTTP = &copy
		effective = catalog.MergeRequestHeaders(res.Resource.Config.Headers, copy.Headers)
	}
	if block.GRPC != nil {
		copy := *block.GRPC
		block.GRPC = &copy
		effective = catalog.MergeRequestHeaders(res.Resource.Config.Headers, copy.Metadata)
	}
	traces := []HeaderSourceResult{}
	// The engine already performed pure preparation before BeginAttempt. Keep
	// direct runner callers safe too, and never send a source with invalid config.
	for _, h := range effective {
		if h.Source != nil {
			if err := catalog.ValidateHeaderSource(*h.Source); err != nil {
				return block, res, traces, fmt.Errorf("Источник заголовка: некорректная настройка")
			}
			if _, err := resources.HeaderTLS(*h.Source, res.Secrets); err != nil {
				return block, res, traces, err
			}
		}
	}
	var failure error
	for i := range effective {
		h := &effective[i]
		if h.Source == nil {
			continue
		}
		s := *h.Source
		trace := HeaderSourceResult{HeaderID: h.ID, HeaderName: h.Name, FetchMode: s.FetchMode, State: "fetched"}
		raw, _ := json.Marshal(s)
		digest := sha256.Sum256(raw)
		key := res.Resource.VersionID + ":" + h.ID + ":" + hex.EncodeToString(digest[:])
		value, found := state.cache[key]
		if s.FetchMode == "per_run" && found {
			trace.State = "reused"
		} else {
			started := time.Now()
			var status *int
			value, status, failure = r.fetchHeader(ctx, s, res)
			trace.StatusCode = status
			trace.DurationMS = time.Since(started).Milliseconds()
		}
		if failure != nil {
			trace.State = "error"
			trace.ErrorCode = "header_source_failed"
			traces = append(traces, trace)
			break
		}
		full := s.Prefix + value
		state.values[value] = true
		state.values[full] = true
		invalid := false
		for _, ch := range full {
			if ch < 32 || ch == 127 || (block.Kind == "grpc" && ch > 126) {
				invalid = true
			}
		}
		if invalid {
			trace.State = "error"
			trace.ErrorCode = "invalid_header_value"
			traces = append(traces, trace)
			failure = fmt.Errorf("Источник заголовка вернул недопустимое значение")
			break
		}
		if s.FetchMode == "per_run" {
			state.cache[key] = value
		}
		h.Value = full
		h.Source = nil
		traces = append(traces, trace)
	}
	for value := range state.values {
		res.Secrets["qag-header-"+strconv.Itoa(len(res.Secrets))] = value
	}
	res.Resource.Config.Headers = []catalog.RequestHeader{}
	if block.HTTP != nil {
		block.HTTP.Headers = effective
	}
	if block.GRPC != nil {
		block.GRPC.Metadata = effective
	}
	return block, res, traces, failure
}
func (r *Runner) fetchHeader(ctx context.Context, s catalog.HTTPHeaderSource, res resources.Resolved) (string, *int, error) {
	u, err := url.Parse(s.URL)
	if err != nil {
		return "", nil, fmt.Errorf("Источник: некорректный URL")
	}
	cfg := resources.Config{BaseURL: u.Scheme + "://" + u.Host, TLS: s.TLS, Headers: []catalog.RequestHeader{}}
	keys := map[string]string{}
	if s.TLS.Mode == "mtls" {
		keys["PRIVATE_KEY"] = res.Secrets[s.PrivateKeySecret]
	}
	hs := []catalog.RequestHeader{}
	for _, h := range s.Headers {
		hs = append(hs, catalog.RequestHeader{Name: h.Name, Value: h.Value})
	}
	b := catalog.Block{Kind: "http", HTTP: &catalog.HTTPRequest{Method: s.Method, Path: u.RequestURI(), Query: s.Query, Headers: hs, BodyMode: s.BodyMode, Body: s.Body}}
	actual, err := r.http(ctx, b, resources.Resolved{Resource: resources.Resource{Config: cfg}, Secrets: keys})
	if err != nil {
		return "", actual.StatusCode, fmt.Errorf("Не удалось выполнить запрос источника заголовка")
	}
	if actual.Truncated {
		return "", actual.StatusCode, fmt.Errorf("Ответ источника превышает 5 MiB")
	}
	if actual.StatusCode == nil || *actual.StatusCode < 200 || *actual.StatusCode >= 300 {
		return "", actual.StatusCode, fmt.Errorf("Источник заголовка вернул HTTP-статус вне 2xx")
	}
	value, err := extractHeaderValue(actual.Body, s.JSONPointer)
	return value, actual.StatusCode, err
}
func extractHeaderValue(body, pointer string) (string, error) {
	fail := func() (string, error) {
		return "", fmt.Errorf("Не удалось извлечь скалярное поле JSON источника заголовка")
	}
	d := json.NewDecoder(strings.NewReader(body))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return fail()
	}
	if d.Decode(new(any)) != io.EOF {
		return fail()
	}
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return fail()
		}
		for _, token := range strings.Split(pointer[1:], "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch v := value.(type) {
			case map[string]any:
				var ok bool
				value, ok = v[token]
				if !ok {
					return fail()
				}
			case []any:
				if len(token) > 1 && token[0] == '0' {
					return fail()
				}
				n, err := strconv.Atoi(token)
				if err != nil || n < 0 || n >= len(v) || strconv.Itoa(n) != token {
					return fail()
				}
				value = v[n]
			default:
				return fail()
			}
		}
	}
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return string(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return fail()
	}
}
