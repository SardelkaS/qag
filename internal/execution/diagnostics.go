package execution

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
)

// LogRequest is an application-level request, captured after substitution.
// It deliberately has no transport credentials or TLS private key fields.
type LogRequest struct {
	Protocol   string               `json:"protocol"`
	Address    string               `json:"address,omitempty"`
	Method     string               `json:"method,omitempty"`
	URL        string               `json:"url,omitempty"`
	Headers    []catalog.KeyValue   `json:"headers"`
	Body       string               `json:"body"`
	SQL        string               `json:"sql,omitempty"`
	Parameters []catalog.TypedValue `json:"parameters,omitempty"`
	Mode       string               `json:"mode,omitempty"`
	Truncated  bool                 `json:"truncated"`
}

type LogSummary struct {
	ID                   string     `json:"id"`
	AttemptID            string     `json:"attemptId,omitempty"`
	ProbeID              string     `json:"probeId,omitempty"`
	RunID                string     `json:"runId,omitempty"`
	TestCaseID           string     `json:"testCaseId,omitempty"`
	BlockID              string     `json:"blockId,omitempty"`
	VariantID            string     `json:"variantId,omitempty"`
	EnvironmentID        string     `json:"environmentId"`
	EnvironmentName      string     `json:"environmentName"`
	EnvironmentVersionID string     `json:"environmentVersionId,omitempty"`
	ResourceID           string     `json:"resourceId,omitempty"`
	ResourceName         string     `json:"resourceName,omitempty"`
	ResourceVersionID    string     `json:"resourceVersionId,omitempty"`
	Protocol             string     `json:"protocol"`
	Origin               string     `json:"origin"`
	Stage                string     `json:"stage"`
	SourceName           string     `json:"sourceName,omitempty"`
	Position             int        `json:"position"`
	State                string     `json:"state"`
	StartedAt            time.Time  `json:"startedAt"`
	FinishedAt           *time.Time `json:"finishedAt,omitempty"`
	DurationMS           int64      `json:"durationMs"`
	RequestSent          bool       `json:"requestSent"`
	Legacy               bool       `json:"legacy"`
	Redacted             bool       `json:"redacted"`
	PersistencePending   bool       `json:"persistencePending"`
}

type CallLog struct {
	Summary           LogSummary `json:"summary"`
	Request           LogRequest `json:"request"`
	Response          *Result    `json:"response,omitempty"`
	BodyOmittedReason string     `json:"bodyOmittedReason,omitempty"`
	IncompleteReason  string     `json:"incompleteReason,omitempty"`
}

type LogFilter struct {
	From          string `json:"from,omitempty"`
	To            string `json:"to,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Origin        string `json:"origin,omitempty"`
	Stage         string `json:"stage,omitempty"`
	State         string `json:"state,omitempty"`
	EnvironmentID string `json:"environmentId,omitempty"`
	ResourceID    string `json:"resourceId,omitempty"`
	TestCaseID    string `json:"testCaseId,omitempty"`
	RunID         string `json:"runId,omitempty"`
	AttemptID     string `json:"attemptId,omitempty"`
	ProbeID       string `json:"probeId,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

type LogCursor struct {
	StartedAt time.Time `json:"at"`
	ID        string    `json:"id"`
}

func (f LogFilter) Validate() (LogCursor, error) {
	var cursor LogCursor
	for _, rule := range []struct {
		field, value string
		allowed      []string
	}{
		{"protocol", f.Protocol, []string{"http", "grpc", "postgresql"}},
		{"origin", f.Origin, []string{"editor", "run", "variant", "variable_probe"}},
		{"stage", f.Stage, []string{"main", "header_source", "environment_variable_source", "resource_variable_source", "variable_probe"}},
		{"state", f.State, []string{"queued", "running", "completed", "error", "timed_out", "cancelled", "interrupted"}},
	} {
		if rule.value != "" {
			found := false
			for _, v := range rule.allowed {
				found = found || rule.value == v
			}
			if !found {
				return cursor, problem.Invalid(rule.field, "Недопустимое значение фильтра")
			}
		}
	}
	if f.Limit < 0 || f.Limit > 100 {
		return cursor, problem.Invalid("limit", "Допустимо от 1 до 100")
	}
	for field, value := range map[string]string{"from": f.From, "to": f.To} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return cursor, problem.Invalid(field, "Нужна дата RFC3339")
			}
		}
	}
	if f.From != "" && f.To != "" {
		from, _ := time.Parse(time.RFC3339Nano, f.From)
		to, _ := time.Parse(time.RFC3339Nano, f.To)
		if from.After(to) {
			return cursor, problem.Invalid("from", "Начало позже окончания")
		}
	}
	for field, value := range map[string]string{"environmentId": f.EnvironmentID, "resourceId": f.ResourceID, "testCaseId": f.TestCaseID, "runId": f.RunID, "attemptId": f.AttemptID, "probeId": f.ProbeID} {
		if value != "" && !catalog.ValidID(value) {
			return cursor, problem.Invalid(field, "Неверный ID")
		}
	}
	if f.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.StartedAt.IsZero() || !ValidLogID(cursor.ID) {
			return cursor, problem.Invalid("cursor", "Неверный курсор")
		}
	}
	return cursor, nil
}
func ValidLogID(id string) bool { return catalog.ValidID(strings.TrimPrefix(id, "legacy:")) }
func EncodeLogCursor(s LogSummary) string {
	b, _ := json.Marshal(LogCursor{s.StartedAt, s.ID})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (f LogFilter) Matches(s LogSummary) bool {
	for _, pair := range [][2]string{{f.Protocol, s.Protocol}, {f.Origin, s.Origin}, {f.Stage, s.Stage}, {f.State, s.State}, {f.EnvironmentID, s.EnvironmentID}, {f.ResourceID, s.ResourceID}, {f.TestCaseID, s.TestCaseID}, {f.RunID, s.RunID}, {f.AttemptID, s.AttemptID}, {f.ProbeID, s.ProbeID}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return false
		}
	}
	if f.From != "" {
		t, _ := time.Parse(time.RFC3339Nano, f.From)
		if s.StartedAt.Before(t) {
			return false
		}
	}
	if f.To != "" {
		t, _ := time.Parse(time.RFC3339Nano, f.To)
		if s.StartedAt.After(t) {
			return false
		}
	}
	return true
}

// ProbeInput is never persisted: its draft may contain write-only secret changes.
type ProbeInput struct {
	Scope               string                                  `json:"scope"`
	EnvironmentID       string                                  `json:"environmentId"`
	EnvironmentRevision int64                                   `json:"environmentRevision"`
	ResourceID          string                                  `json:"resourceId,omitempty"`
	ResourceRevision    int64                                   `json:"resourceRevision,omitempty"`
	VariableName        string                                  `json:"variableName,omitempty"`
	Source              *catalog.HTTPHeaderSource               `json:"source,omitempty"`
	Variables           map[string]string                       `json:"variables,omitempty"`
	ResourceVariables   map[string]resources.VariableDefinition `json:"resourceVariables,omitempty"`
	Secrets             *resources.SecretChanges                `json:"secrets,omitempty"`
	TimeoutSeconds      int                                     `json:"timeoutSeconds,omitempty"`
	IdempotencyKey      string                                  `json:"idempotencyKey"`
}

// HTTPVariableProbe contains only safe, durable diagnostics, never a fetched value.
type HTTPVariableProbe struct {
	ID                 string                   `json:"id"`
	Scope              string                   `json:"scope"`
	EnvironmentID      string                   `json:"environmentId"`
	ResourceID         string                   `json:"resourceId,omitempty"`
	VariableName       string                   `json:"variableName,omitempty"`
	Source             catalog.HTTPHeaderSource `json:"source"`
	LogID              string                   `json:"logId,omitempty"`
	State              string                   `json:"state"`
	StartedAt          time.Time                `json:"startedAt"`
	FinishedAt         *time.Time               `json:"finishedAt,omitempty"`
	DurationMS         int64                    `json:"durationMs"`
	StatusCode         *int                     `json:"statusCode,omitempty"`
	ErrorCode          string                   `json:"errorCode,omitempty"`
	Error              string                   `json:"error,omitempty"`
	PersistencePending bool                     `json:"persistencePending"`
}

// ProbeView is the sole live value projection. Repositories only accept Probe.
type ProbeView struct {
	Probe            HTTPVariableProbe `json:"probe"`
	ValueAvailable   bool              `json:"valueAvailable"`
	ValueUnavailable string            `json:"valueUnavailable,omitempty"`
	ExtractedValue   *string           `json:"extractedValue,omitempty"`
	FormattedValue   *string           `json:"formattedValue,omitempty"`
	ValueType        string            `json:"valueType,omitempty"`
}

func ActiveState(state string) bool { return state == "queued" || state == "running" }

func LegacySummary(a Attempt) LogSummary {
	origin := "editor"
	if a.RunID != "" {
		origin = "run"
	}
	if a.VariantID != "" {
		origin = "variant"
	}
	return LogSummary{ID: "legacy:" + a.ID, AttemptID: a.ID, RunID: a.RunID, TestCaseID: a.TestCaseID, BlockID: a.BlockID, VariantID: a.VariantID, EnvironmentID: a.EnvironmentID, EnvironmentName: a.EnvironmentName, ResourceID: a.Resource.ID, ResourceName: a.Resource.Name, ResourceVersionID: a.Resource.VersionID, Protocol: a.Request.Kind, Origin: origin, Stage: "main", Position: 1, State: a.State, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, DurationMS: a.DurationMS, Legacy: true}
}

func LegacyLog(a Attempt) CallLog {
	l := CallLog{Summary: LegacySummary(a), Response: a.Result, IncompleteReason: "Сохранённое определение попытки: фактические параметры и тела HTTP-источников недоступны"}
	l.Request.Protocol = a.Request.Kind
	l.Request.Headers = []catalog.KeyValue{}
	headers := func(values []catalog.RequestHeader) []catalog.KeyValue {
		out := []catalog.KeyValue{}
		for _, h := range values {
			value := h.Value
			if h.Source != nil {
				value = "[HTTP-источник]"
			}
			out = append(out, catalog.KeyValue{Name: h.Name, Value: value})
		}
		return out
	}
	if h := a.Request.HTTP; h != nil {
		l.Request.Method = h.Method
		l.Request.URL = h.Path
		l.Request.Body = h.Body
		l.Request.Headers = headers(h.Headers)
	}
	if g := a.Request.GRPC; g != nil {
		l.Request.Address = a.Resource.Config.Address
		l.Request.Method = g.Service + "/" + g.Method
		l.Request.Body = g.Body
		l.Request.Headers = headers(g.Metadata)
	}
	if s := a.Request.SQL; s != nil {
		l.Request.Address = a.Resource.Config.Address
		l.Request.SQL = s.Query
		l.Request.Parameters = s.Parameters
		l.Request.Mode = s.Mode
	}
	return l
}

func PageLogs(items []LogSummary, f LogFilter) (catalog.Page[LogSummary], error) {
	page := catalog.Page[LogSummary]{Items: []LogSummary{}}
	cursor, err := f.Validate()
	if err != nil {
		return page, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].StartedAt.Equal(items[j].StartedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	for _, s := range items {
		if !f.Matches(s) {
			continue
		}
		if f.Cursor != "" && (s.StartedAt.After(cursor.StartedAt) || s.StartedAt.Equal(cursor.StartedAt) && s.ID >= cursor.ID) {
			continue
		}
		if len(page.Items) == limit {
			page.NextCursor = EncodeLogCursor(page.Items[len(page.Items)-1])
			break
		}
		page.Items = append(page.Items, s)
	}
	return page, nil
}
