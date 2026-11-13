package filestore

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
)

func logRow(l execution.CallLog) dataset.CallLog {
	s := l.Summary
	return dataset.CallLog{ID: s.ID, AttemptID: s.AttemptID, ProbeID: s.ProbeID, RunID: s.RunID, TestCaseID: s.TestCaseID, EnvironmentID: s.EnvironmentID, ResourceID: s.ResourceID, Protocol: s.Protocol, Origin: s.Origin, Stage: s.Stage, State: s.State, StartedAt: s.StartedAt, Summary: dataset.Raw(s), Payload: dataset.Raw(l)}
}
func (r *Runs) BeginLog(ctx context.Context, l execution.CallLog) error {
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		for _, v := range d.CallLogs {
			if v.ID == l.Summary.ID {
				return problem.Conflict("Запись журнала уже существует")
			}
		}
		d.CallLogs = append(d.CallLogs, logRow(l))
		return nil
	})
}
func (r *Runs) CompleteLog(ctx context.Context, l execution.CallLog) error {
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		i := index(d.CallLogs, func(v dataset.CallLog) bool { return v.ID == l.Summary.ID })
		if i < 0 {
			return problem.NotFound()
		}
		if !execution.ActiveState(d.CallLogs[i].State) {
			if d.CallLogs[i].Payload == dataset.Raw(l) {
				return nil
			}
			return problem.Conflict("Вызов уже завершён")
		}
		d.CallLogs[i] = logRow(l)
		return nil
	})
}
func (r *Runs) GetLog(ctx context.Context, id string) (execution.CallLog, error) {
	if strings.HasPrefix(id, "legacy:") {
		a, e := r.GetAttempt(ctx, strings.TrimPrefix(id, "legacy:"))
		if e != nil {
			return execution.CallLog{}, e
		}
		return execution.LegacyLog(a), nil
	}
	if !catalog.ValidID(id) {
		return execution.CallLog{}, problem.Invalid("id", "Неверный ID")
	}
	r.Store.mu.RLock()
	defer r.Store.mu.RUnlock()
	if e := ctx.Err(); e != nil {
		return execution.CallLog{}, e
	}
	if e := r.Store.check(); e != nil {
		return execution.CallLog{}, e
	}
	for _, v := range r.Store.doc.Data.CallLogs {
		if v.ID == id {
			return dataset.Value[execution.CallLog](v.Payload)
		}
	}
	return execution.CallLog{}, problem.NotFound()
}
func (r *Runs) ListLogs(ctx context.Context, f execution.LogFilter) (catalog.Page[execution.LogSummary], error) {
	if _, e := f.Validate(); e != nil {
		return catalog.Page[execution.LogSummary]{}, e
	}
	r.Store.mu.RLock()
	defer r.Store.mu.RUnlock()
	if e := ctx.Err(); e != nil {
		return catalog.Page[execution.LogSummary]{}, e
	}
	if e := r.Store.check(); e != nil {
		return catalog.Page[execution.LogSummary]{}, e
	}
	d := &r.Store.doc.Data
	items := []execution.LogSummary{}
	known := map[string]bool{}
	for _, v := range d.CallLogs {
		s, e := dataset.Value[execution.LogSummary](v.Summary)
		if e != nil {
			return catalog.Page[execution.LogSummary]{}, storageError()
		}
		items = append(items, s)
		known[v.AttemptID] = true
	}
	for _, v := range d.CallAttempts {
		if known[v.ID] {
			continue
		}
		var envelope struct {
			Block struct {
				Kind string `json:"kind"`
			} `json:"block"`
			Resource struct {
				ID            string `json:"id"`
				EnvironmentID string `json:"environmentId"`
				Name          string `json:"name"`
				VersionID     string `json:"versionId"`
			} `json:"resource"`
			EnvironmentName string `json:"environmentName"`
		}
		if json.Unmarshal([]byte(v.SafeRequest), &envelope) != nil {
			return catalog.Page[execution.LogSummary]{}, storageError()
		}
		a := execution.Attempt{ID: v.ID, RunID: v.RunID, TestCaseID: v.TestCaseID, BlockID: v.BlockID, VariantID: v.VariantID, EnvironmentID: envelope.Resource.EnvironmentID, EnvironmentName: envelope.EnvironmentName, State: v.State, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, DurationMS: v.DurationMS, Request: catalog.CallRequest{Kind: envelope.Block.Kind}}
		a.Resource.ID = envelope.Resource.ID
		a.Resource.Name = envelope.Resource.Name
		a.Resource.VersionID = envelope.Resource.VersionID
		items = append(items, execution.LegacySummary(a))
	}
	return execution.PageLogs(items, f)
}
func (r *Runs) BeginProbe(ctx context.Context, p execution.HTTPVariableProbe, key, hash string) (execution.HTTPVariableProbe, bool, error) {
	out := p
	created := false
	err := r.Store.Update(ctx, func(d *dataset.Data) error {
		for _, v := range d.HTTPVariableProbes {
			if v.IdempotencyKey == key {
				if v.RequestHash != hash {
					return problem.Conflict("Ключ проверки использован с другими параметрами")
				}
				var e error
				out, e = dataset.Value[execution.HTTPVariableProbe](v.Payload)
				return e
			}
		}
		d.HTTPVariableProbes = append(d.HTTPVariableProbes, dataset.HTTPVariableProbe{ID: p.ID, EnvironmentID: p.EnvironmentID, ResourceID: p.ResourceID, IdempotencyKey: key, RequestHash: hash, State: p.State, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt, Payload: dataset.Raw(p)})
		created = true
		return nil
	})
	return out, created, err
}
func (r *Runs) GetProbe(ctx context.Context, id string) (execution.HTTPVariableProbe, error) {
	if !catalog.ValidID(id) {
		return execution.HTTPVariableProbe{}, problem.Invalid("id", "Неверный ID")
	}
	r.Store.mu.RLock()
	defer r.Store.mu.RUnlock()
	if e := ctx.Err(); e != nil {
		return execution.HTTPVariableProbe{}, e
	}
	if e := r.Store.check(); e != nil {
		return execution.HTTPVariableProbe{}, e
	}
	for _, v := range r.Store.doc.Data.HTTPVariableProbes {
		if v.ID == id {
			return dataset.Value[execution.HTTPVariableProbe](v.Payload)
		}
	}
	return execution.HTTPVariableProbe{}, problem.NotFound()
}

func (r *Runs) LookupProbe(ctx context.Context, key, hash string) (execution.HTTPVariableProbe, bool, error) {
	r.Store.mu.RLock()
	defer r.Store.mu.RUnlock()
	if e := ctx.Err(); e != nil {
		return execution.HTTPVariableProbe{}, false, e
	}
	if e := r.Store.check(); e != nil {
		return execution.HTTPVariableProbe{}, false, e
	}
	for _, v := range r.Store.doc.Data.HTTPVariableProbes {
		if v.IdempotencyKey == key {
			if v.RequestHash != hash {
				return execution.HTTPVariableProbe{}, true, problem.Conflict("Ключ проверки использован с другими параметрами")
			}
			p, e := dataset.Value[execution.HTTPVariableProbe](v.Payload)
			return p, true, e
		}
	}
	return execution.HTTPVariableProbe{}, false, nil
}
func (r *Runs) UpdateProbe(ctx context.Context, p execution.HTTPVariableProbe) (execution.HTTPVariableProbe, error) {
	out := p
	err := r.Store.Update(ctx, func(d *dataset.Data) error {
		i := index(d.HTTPVariableProbes, func(v dataset.HTTPVariableProbe) bool { return v.ID == p.ID })
		if i < 0 {
			return problem.NotFound()
		}
		v := &d.HTTPVariableProbes[i]
		if !execution.ActiveState(v.State) {
			var e error
			out, e = dataset.Value[execution.HTTPVariableProbe](v.Payload)
			return e
		}
		v.State = p.State
		v.FinishedAt = p.FinishedAt
		v.Payload = dataset.Raw(p)
		return nil
	})
	return out, err
}
