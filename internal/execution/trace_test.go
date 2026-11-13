package execution

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

type memoryTrace struct {
	logs      []CallLog
	failBegin bool
}

func (r *memoryTrace) BeginTrace(_ context.Context, l *CallLog) error {
	if r.failBegin {
		return errors.New("registration failed")
	}
	r.logs = append(r.logs, *l)
	return nil
}
func (r *memoryTrace) EndTrace(_ context.Context, l CallLog) error {
	r.logs[len(r.logs)-1] = l
	return nil
}
func TestSourceTraceMasksFormAndExtractedField(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.ParseForm() != nil || r.Form.Get("secret") != "a+b& c" {
			t.Error("wrong form")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "cookie-secret")
		w.Write([]byte(`{"token":"live-token","expires":9007199254740993123.125}`))
	}))
	defer srv.Close()
	source := catalog.HTTPHeaderSource{Method: "POST", URL: srv.URL, BodyMode: "form", Form: []catalog.KeyValue{{Name: "secret", Value: "a+b& c"}}, TLS: catalog.TLSConfig{Mode: "plaintext"}, JSONPointer: "/token", Prefix: "Bearer ", FetchMode: "per_attempt"}
	recorder := &memoryTrace{}
	entry := &SourceTrace{Stage: "variable_probe", Source: &source}
	ctx := WithSourceTrace(WithTraceRecorder(context.Background(), recorder), entry)
	value, _, err := NewRunner().FetchVariableSource(ctx, source, resources.Resolved{Secrets: map[string]string{"secret": "a+b& c"}})
	if err != nil || value != "live-token" || entry.ValueType != "string" {
		t.Fatal("live value", value, err)
	}
	if len(recorder.logs) != 1 || !recorder.logs[0].Summary.RequestSent {
		t.Fatal("missing sent trace")
	}
	l := recorder.logs[0]
	if strings.Contains(l.Request.Body, "a%2Bb%26+c") || strings.Contains(l.Response.Body, "live-token") || !strings.Contains(l.Response.Body, "9007199254740993123.125") {
		t.Fatal("mask or precision", l)
	}
	for _, h := range l.Response.Headers {
		if h.Value == "cookie-secret" {
			t.Fatal("cookie exposed")
		}
	}
	recorder.failBegin = true
	if _, _, err = NewRunner().FetchVariableSource(ctx, source, resources.Resolved{}); err == nil || calls.Load() != 1 {
		t.Fatal("network before registration", calls.Load(), err)
	}
}
func TestSourceMaskPointerEmptyAndEscaped(t *testing.T) {
	masked, err := maskSourceBody(`{"a/b":[{"~token":""}],"n":9007199254740993123.125}`, "/a~1b/0/~0token")
	if err != nil || !strings.Contains(masked, `"~token":"[скрыто]"`) || !strings.Contains(masked, "9007199254740993123.125") {
		t.Fatal(masked, err)
	}
}
