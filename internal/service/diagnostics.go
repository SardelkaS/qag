package service

import (
	"context"
	"sync"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
)

type journal struct {
	engine   *Engine
	meta     execution.LogSummary
	mu       sync.Mutex
	position int
}

func (e *Engine) attemptJournal(a execution.Attempt, res resources.Resolved) *journal {
	origin := "editor"
	if a.RunID != "" {
		origin = "run"
	}
	if a.VariantID != "" {
		origin = "variant"
	}
	return &journal{engine: e, meta: execution.LogSummary{AttemptID: a.ID, RunID: a.RunID, TestCaseID: a.TestCaseID, BlockID: a.BlockID, VariantID: a.VariantID, EnvironmentID: a.EnvironmentID, EnvironmentName: a.EnvironmentName, EnvironmentVersionID: res.EnvironmentVersionID, ResourceID: a.Resource.ID, ResourceName: a.Resource.Name, ResourceVersionID: a.Resource.VersionID, Origin: origin}}
}
func (j *journal) BeginTrace(ctx context.Context, l *execution.CallLog) error {
	j.mu.Lock()
	j.position++
	position := j.position
	j.mu.Unlock()
	s := l.Summary
	l.Summary = j.meta
	l.Summary.ID = s.ID
	l.Summary.Protocol = s.Protocol
	l.Summary.Stage = s.Stage
	l.Summary.SourceName = s.SourceName
	l.Summary.State = s.State
	l.Summary.StartedAt = s.StartedAt
	l.Summary.Position = position
	l.Summary.Redacted = s.Redacted
	return j.engine.Runs.BeginLog(ctx, *l)
}
func (j *journal) EndTrace(ctx context.Context, l execution.CallLog) error {
	e := j.engine
	for {
		persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := e.Runs.CompleteLog(persist, l)
		cancel()
		e.mu.Lock()
		if err == nil {
			delete(e.pendingLogs, l.Summary.ID)
			if id := j.meta.AttemptID; id != "" {
				if job, ok := e.jobs[id]; ok {
					job.persistencePending = false
					e.jobs[id] = job
				}
			}
		} else {
			e.pendingLogs[l.Summary.ID] = l
			if id := j.meta.AttemptID; id != "" {
				job, ok := e.jobs[id]
				if ok {
					job.persistencePending = true
					e.jobs[id] = job
				}
			}
		}
		e.mu.Unlock()
		if err == nil {
			return nil
		}
		select {
		case <-e.ctx.Done():
			return &problem.Error{Code: "storage_unavailable", Message: "Результат журнала не подтверждён; внешний запрос не повторялся"}
		case <-time.After(200 * time.Millisecond):
		}
	}
}
func (e *Engine) GetLog(ctx context.Context, id string) (execution.CallLog, error) {
	e.mu.Lock()
	pending, ok := e.pendingLogs[id]
	e.mu.Unlock()
	if ok {
		pending.Summary.PersistencePending = true
		return pending, nil
	}
	return e.Runs.GetLog(ctx, id)
}
func (e *Engine) ListLogs(ctx context.Context, f execution.LogFilter) (catalog.Page[execution.LogSummary], error) {
	page, err := e.Runs.ListLogs(ctx, f)
	if err != nil {
		return page, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range page.Items {
		_, page.Items[i].PersistencePending = e.pendingLogs[page.Items[i].ID]
	}
	return page, nil
}
