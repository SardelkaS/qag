package dataset

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
)

func strictDiagnostic[T any](raw JSON) (T, error) {
	var v T
	d := json.NewDecoder(bytes.NewBufferString(string(raw)))
	d.DisallowUnknownFields()
	err := d.Decode(&v)
	return v, err
}

func validateDiagnostics(doc Document) error {
	d := doc.Data
	if doc.Version < 4 && (len(d.CallLogs) > 0 || len(d.HTTPVariableProbes) > 0) {
		return invalid("формат", "", "журнал требует v4")
	}
	envs := map[string]bool{}
	rs := map[string]Resource{}
	attempts := map[string]CallAttempt{}
	probes := map[string]HTTPVariableProbe{}
	keys := map[string]bool{}
	for _, v := range d.Environments {
		envs[v.ID] = true
	}
	for _, v := range d.Resources {
		rs[v.ID] = v
	}
	for _, v := range d.CallAttempts {
		attempts[v.ID] = v
	}
	validState := func(s string) bool {
		return oneOf(s, "queued", "running", "completed", "error", "timed_out", "cancelled", "interrupted")
	}
	for _, p := range d.HTTPVariableProbes {
		v, err := strictDiagnostic[execution.HTTPVariableProbe](p.Payload)
		if err != nil || v.ID != p.ID || v.EnvironmentID != p.EnvironmentID || v.ResourceID != p.ResourceID || v.State != p.State || !v.StartedAt.Equal(p.StartedAt) || !validState(p.State) || !envs[p.EnvironmentID] || !catalog.ValidID(p.IdempotencyKey) || keys[p.IdempotencyKey] || len(p.RequestHash) != 64 || !oneOf(v.Scope, "environment", "resource") {
			return invalid("httpVariableProbes", p.ID, "состояние, содержимое или связь")
		}
		if p.ResourceID != "" && rs[p.ResourceID].EnvironmentID != p.EnvironmentID {
			return invalid("httpVariableProbes", p.ID, "ресурс")
		}
		keys[p.IdempotencyKey] = true
		probes[p.ID] = p
	}
	for _, l := range d.CallLogs {
		v, err := strictDiagnostic[execution.CallLog](l.Payload)
		s, se := strictDiagnostic[execution.LogSummary](l.Summary)
		if err != nil || se != nil || !reflect.DeepEqual(v.Summary, s) || s.ID != l.ID || s.AttemptID != l.AttemptID || s.ProbeID != l.ProbeID || s.RunID != l.RunID || s.TestCaseID != l.TestCaseID || s.EnvironmentID != l.EnvironmentID || s.ResourceID != l.ResourceID || s.Protocol != l.Protocol || s.Origin != l.Origin || s.Stage != l.Stage || s.State != l.State || !s.StartedAt.Equal(l.StartedAt) || s.Legacy || s.PersistencePending || !validState(l.State) || !envs[l.EnvironmentID] || !oneOf(l.Protocol, "http", "grpc", "postgresql") || !oneOf(l.Origin, "editor", "run", "variant", "variable_probe") || !oneOf(l.Stage, "main", "header_source", "environment_variable_source", "resource_variable_source", "variable_probe") || (l.AttemptID == "") == (l.ProbeID == "") {
			return invalid("callLogs", l.ID, "состояние, содержимое или связь")
		}
		if l.AttemptID != "" {
			a, ok := attempts[l.AttemptID]
			if !ok || a.RunID != l.RunID || a.TestCaseID != l.TestCaseID {
				return invalid("callLogs", l.ID, "попытка")
			}
		}
		if l.ProbeID != "" {
			p, ok := probes[l.ProbeID]
			if !ok || p.EnvironmentID != l.EnvironmentID || l.RunID != "" || l.TestCaseID != "" {
				return invalid("callLogs", l.ID, "проверка")
			}
		}
		if l.ResourceID != "" && rs[l.ResourceID].EnvironmentID != l.EnvironmentID {
			return invalid("callLogs", l.ID, "ресурс")
		}
	}
	return nil
}
