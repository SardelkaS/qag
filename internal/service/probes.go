package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
)

type probeValue struct {
	raw, formatted, kind string
	expires              time.Time
}

func (e *Engine) prepareProbe(ctx context.Context, in execution.ProbeInput) (resources.Resolved, catalog.HTTPHeaderSource, catalog.HTTPHeaderSource, error) {
	var source catalog.HTTPHeaderSource
	if in.Scope != "environment" && in.Scope != "resource" {
		return resources.Resolved{}, source, source, problem.Invalid("scope", "Выберите environment или resource")
	}
	if in.Scope == "environment" && in.ResourceID != "" {
		return resources.Resolved{}, source, source, problem.Invalid("resourceId", "Источник окружения не использует ресурс")
	}
	res, err := e.Resources.ResolveProbe(ctx, in.EnvironmentID, in.ResourceID)
	if err != nil {
		return res, source, source, err
	}
	if in.EnvironmentRevision != res.EnvironmentRevision || in.ResourceID != "" && in.ResourceRevision != res.Resource.Revision {
		return res, source, source, problem.Conflict("Настройки изменились; обновите контекст проверки")
	}
	if in.Source != nil {
		source = *in.Source
	} else if in.Scope == "environment" {
		var exists bool
		source, exists = res.EnvironmentHTTPVariables[in.VariableName]
		if !exists {
			return res, source, source, problem.NotFound()
		}
	} else {
		def, exists := res.Resource.Config.Variables[in.VariableName]
		if !exists || def.Mode != "http" || def.Source == nil {
			return res, source, source, problem.NotFound()
		}
		source = *def.Source
	}
	if in.Variables != nil {
		res.EnvironmentVariables = resources.NormalizeNewVariables(in.Variables)
		res.Variables = res.EnvironmentVariables
	}
	localSecrets := map[string]string{}
	changes := resources.SecretChanges{}
	if in.Secrets != nil {
		changes = *in.Secrets
	}
	if in.Scope == "environment" {
		for k, v := range res.EnvironmentSecrets {
			localSecrets[k] = v
		}
	} else {
		for k, v := range res.Secrets {
			if _, env := res.EnvironmentSecrets[k]; !env {
				localSecrets[k] = v
			}
		}
	}
	for _, name := range changes.Clear {
		if strings.TrimSpace(name) != "" {
			delete(localSecrets, name)
		}
	}
	for name, value := range changes.Replace {
		if strings.TrimSpace(name) != "" {
			localSecrets[name] = value
		}
	}
	if in.Scope == "environment" {
		res.EnvironmentSecrets = localSecrets
		res.Secrets = localSecrets
		res = res.EnvironmentSourceContext()
	} else {
		res.Secrets = map[string]string{}
		for k, v := range res.EnvironmentSecrets {
			res.Secrets[k] = v
		}
		for k, v := range localSecrets {
			if _, exists := res.Secrets[k]; exists {
				return res, source, source, problem.Invalid("secrets", "Имя секрета повторяется в окружении и ресурсе")
			}
			res.Secrets[k] = v
		}
		if in.ResourceVariables != nil {
			res.Resource.Config.Variables = resources.NormalizeResourceVariables(in.ResourceVariables)
		}
	}
	prepared, err := resources.ProbeSource(res, source)
	if err != nil {
		return res, source, prepared, err
	}
	if _, err = resources.HeaderTLS(prepared, res.Secrets); err != nil {
		return res, source, prepared, err
	}
	if len(prepared.Prefix)+len(prepared.Suffix) > 64<<10 {
		return res, source, prepared, problem.Invalid("source", "Текст вокруг значения превышает 64 KiB")
	}
	return res, source, prepared, nil
}

func (e *Engine) StartProbe(ctx context.Context, in execution.ProbeInput) (execution.HTTPVariableProbe, error) {
	if err := commandKey(in.IdempotencyKey); err != nil {
		return execution.HTTPVariableProbe{}, err
	}
	if in.TimeoutSeconds < 0 || in.TimeoutSeconds > 300 {
		return execution.HTTPVariableProbe{}, problem.Invalid("timeoutSeconds", "Допустимо от 1 до 300 секунд")
	}
	hash := requestHash(in)
	if p, found, err := e.Runs.LookupProbe(ctx, in.IdempotencyKey, hash); err != nil || found {
		return p, err
	}
	res, definition, source, err := e.prepareProbe(ctx, in)
	if err != nil {
		return execution.HTTPVariableProbe{}, err
	}
	// Public definition retains placeholders, while draft secret replacements
	// and expanded credentials remain confined to this execution context.
	b, _ := json.Marshal(definition)
	safe := resources.NewRedactor(res.Secrets).Mask(string(b))
	if json.Unmarshal([]byte(safe), &definition) != nil {
		return execution.HTTPVariableProbe{}, problem.Invalid("source", "Не удалось подготовить безопасное определение")
	}
	if definition.Query == nil {
		definition.Query = []catalog.KeyValue{}
	}
	if definition.Headers == nil {
		definition.Headers = []catalog.KeyValue{}
	}
	p := execution.HTTPVariableProbe{ID: catalog.NewID(), Scope: in.Scope, EnvironmentID: in.EnvironmentID, ResourceID: in.ResourceID, VariableName: in.VariableName, Source: definition, State: "queued", StartedAt: time.Now().UTC().Truncate(time.Microsecond)}
	p, created, err := e.Runs.BeginProbe(ctx, p, in.IdempotencyKey, hash)
	if err != nil || !created {
		return p, err
	}
	jobCtx, cancel := context.WithCancel(e.ctx)
	e.mu.Lock()
	e.jobs[p.ID] = job{cancel: cancel}
	e.mu.Unlock()
	e.wg.Add(1)
	go e.executeProbe(jobCtx, cancel, p, in.TimeoutSeconds, source, res)
	return p, nil
}

func (e *Engine) executeProbe(ctx context.Context, cancel context.CancelFunc, p execution.HTTPVariableProbe, seconds int, source catalog.HTTPHeaderSource, res resources.Resolved) {
	defer e.wg.Done()
	defer cancel()
	defer func() { e.mu.Lock(); delete(e.jobs, p.ID); e.mu.Unlock() }()
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return
	}
	p.State = "running"
	current, err := e.persistProbe(p)
	if err != nil || !execution.ActiveState(current.State) {
		return
	}
	p = current
	if seconds == 0 {
		seconds = 30
	}
	ctx, deadline := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer deadline()
	ctx = execution.WithRequestGate(ctx, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		state, err := e.Runs.GetProbe(ctx, p.ID)
		if err != nil {
			return err
		}
		if !execution.ActiveState(state.State) {
			return context.Canceled
		}
		return nil
	})
	j := &journal{engine: e, meta: execution.LogSummary{ProbeID: p.ID, EnvironmentID: p.EnvironmentID, EnvironmentName: res.EnvironmentName, EnvironmentVersionID: res.EnvironmentVersionID, ResourceID: p.ResourceID, ResourceName: res.Resource.Name, ResourceVersionID: res.Resource.VersionID, Origin: "variable_probe"}}
	entry := &execution.SourceTrace{Stage: "variable_probe", Name: p.VariableName, Source: &source}
	ctx = execution.WithSourceTrace(execution.WithTraceRecorder(ctx, j), entry)
	value, status, err := e.Runner.FetchVariableSource(ctx, source, res)
	p.LogID = entry.LogID
	p.StatusCode = status
	p.State = "completed"
	formatted := source.Prefix + value + source.Suffix
	if err == nil && len(formatted) > 64<<10 {
		err = problem.Invalid("source", "Значение источника превышает 64 KiB")
	}
	if err != nil {
		p.State = "error"
		p.ErrorCode = "source_failed"
		p.Error = resources.NewRedactor(res.Secrets).Mask(err.Error())
	}
	if ctx.Err() == context.DeadlineExceeded {
		p.State = "timed_out"
		p.ErrorCode = "timed_out"
		p.Error = "Превышено время получения значения"
	} else if ctx.Err() == context.Canceled {
		p.State = "cancelled"
		p.ErrorCode = "cancelled"
		p.Error = "Проверка отменена; внешний эффект неизвестен"
	}
	t := time.Now().UTC().Truncate(time.Microsecond)
	p.FinishedAt = &t
	p.DurationMS = t.Sub(p.StartedAt).Milliseconds()
	// Distinguish the source JSON scalar type without converting numeric tokens.
	kind := "string"
	// FetchVariableSource records the precise type in its per-request context.
	if entry.ValueType != "" {
		kind = entry.ValueType
	}
	if p.State == "completed" {
		e.putProbeValue(p.ID, probeValue{raw: value, formatted: formatted, kind: kind})
	}
	current, err = e.persistProbe(p)
	if err != nil || current.State != "completed" {
		e.mu.Lock()
		delete(e.probeValues, p.ID)
		e.mu.Unlock()
	}
}

func (e *Engine) persistProbe(p execution.HTTPVariableProbe) (execution.HTTPVariableProbe, error) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := e.Runs.UpdateProbe(ctx, p)
		cancel()
		e.mu.Lock()
		if err == nil {
			delete(e.probePending, p.ID)
		} else {
			e.probePending[p.ID] = p
		}
		e.mu.Unlock()
		if err == nil {
			return out, nil
		}
		select {
		case <-e.ctx.Done():
			return out, err
		case <-time.After(200 * time.Millisecond):
		}
	}
}
func (e *Engine) putProbeValue(id string, v probeValue) {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.probeNow()
	for id, p := range e.probeValues {
		if !now.Before(p.expires) {
			delete(e.probeValues, id)
		}
	}
	if len(e.probeValues) >= 128 {
		oldID := ""
		var oldest time.Time
		for id, p := range e.probeValues {
			if oldID == "" || p.expires.Before(oldest) {
				oldID = id
				oldest = p.expires
			}
		}
		delete(e.probeValues, oldID)
	}
	v.expires = now.Add(10 * time.Minute)
	e.probeValues[id] = v
}
func (e *Engine) GetProbe(ctx context.Context, id string) (execution.ProbeView, error) {
	p, err := e.Runs.GetProbe(ctx, id)
	if err != nil {
		return execution.ProbeView{}, err
	}
	if p.LogID == "" && !execution.ActiveState(p.State) {
		if page, err := e.Runs.ListLogs(ctx, execution.LogFilter{ProbeID: p.ID, Limit: 1}); err == nil && len(page.Items) > 0 {
			p.LogID = page.Items[0].ID
		}
	}
	view := execution.ProbeView{Probe: p}
	e.mu.Lock()
	defer e.mu.Unlock()
	if pending, ok := e.probePending[id]; ok {
		view.Probe = pending
		view.Probe.PersistencePending = true
	}
	if v, ok := e.probeValues[id]; ok && e.probeNow().Before(v.expires) {
		if p.State == "completed" {
			view.ValueAvailable = true
			view.ExtractedValue = &v.raw
			view.FormattedValue = &v.formatted
			view.ValueType = v.kind
		}
	} else {
		delete(e.probeValues, id)
		if p.State == "completed" {
			view.ValueUnavailable = "Значение истекло, вытеснено либо недоступно после перезапуска"
		}
	}
	return view, nil
}
func (e *Engine) CancelProbe(ctx context.Context, id string) (execution.HTTPVariableProbe, error) {
	p, err := e.Runs.GetProbe(ctx, id)
	if err != nil {
		return p, err
	}
	if !execution.ActiveState(p.State) {
		return p, nil
	}
	p.State = "cancelled"
	p.ErrorCode = "cancelled"
	p.Error = "Проверка отменена; внешний эффект неизвестен"
	t := time.Now().UTC().Truncate(time.Microsecond)
	p.FinishedAt = &t
	p.DurationMS = t.Sub(p.StartedAt).Milliseconds()
	p, err = e.Runs.UpdateProbe(ctx, p)
	if err != nil {
		return p, err
	}
	e.mu.Lock()
	job := e.jobs[id]
	delete(e.probeValues, id)
	e.mu.Unlock()
	if job.cancel != nil {
		job.cancel()
	}
	return p, nil
}
