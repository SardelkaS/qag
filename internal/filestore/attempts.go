package filestore

import (
	"context"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

func activeAttempt(s string) bool { return s == "queued" || s == "running" }
func (r *Runs) GetAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	if e := valid(id); e != nil {
		return execution.Attempt{}, e
	}
	d, e := r.Store.data(ctx)
	if e != nil {
		return execution.Attempt{}, e
	}
	i := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == id })
	if i < 0 {
		return execution.Attempt{}, problem.NotFound()
	}
	return attemptValue(d.CallAttempts[i])
}
func (r *Runs) LookupAttempt(ctx context.Context, key, hash string) (execution.Attempt, bool, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return execution.Attempt{}, false, e
	}
	for _, v := range d.CallAttempts {
		if v.IdempotencyKey == key {
			if v.RequestHash != hash {
				return execution.Attempt{}, true, problem.Conflict("Этот ключ команды уже использован с другими параметрами")
			}
			a, e := attemptValue(v)
			return a, true, e
		}
	}
	return execution.Attempt{}, false, nil
}
func (r *Runs) BeginAttempt(ctx context.Context, candidateID, caseID, runID string, revision int64, block catalog.Block, snapshot resources.Snapshot, safeBlock catalog.Block, key, hash string, options ...runs.AttemptOptions) (execution.Attempt, bool, error) {
	opt := runs.AttemptOptions{}
	if len(options) > 0 {
		opt = options[0]
	}
	id := candidateID
	created := false
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		for _, v := range d.CallAttempts {
			if v.IdempotencyKey == key {
				if v.RequestHash != hash {
					return problem.Conflict("Ключ команды использован иначе")
				}
				id = v.ID
				return nil
			}
		}
		if runID != "" {
			run, e := getRun(d, runID)
			if e != nil {
				return e
			}
			if run.TestCaseID != caseID {
				return problem.NotFound()
			}
			if run.Status != "running" {
				return problem.Conflict("Завершённый прогон доступен только для просмотра")
			}
			batch := ""
			for _, b := range run.Batches {
				if b.State == "running" {
					batch = b.ID
				}
			}
			if opt.BatchID == "" && batch != "" {
				return problem.Conflict("Дождитесь завершения очереди")
			}
			if opt.BatchID != "" {
				if batch != opt.BatchID {
					return problem.Conflict("Очередь уже остановлена")
				}
				i := index(d.BatchItems, func(v dataset.BatchItem) bool {
					return v.BatchID == opt.BatchID && v.Position == opt.BatchPosition && v.RunID == runID && v.BlockID == block.ID
				})
				if i < 0 {
					return problem.NotFound()
				}
				if d.BatchItems[i].State != "pending" {
					return problem.Conflict("Элемент очереди уже запущен")
				}
			}
			if opt.VariantID != "" {
				i := index(d.Variants, func(v dataset.Variant) bool {
					return v.ID == opt.VariantID && v.RunID == runID && v.BlockID == block.ID
				})
				if i < 0 {
					return problem.NotFound()
				}
				if d.Variants[i].CurrentVersionID != opt.VariantVersionID {
					return problem.Conflict("Сохранённая версия варианта изменилась")
				}
			}
			for _, a := range run.Attempts {
				if activeAttempt(a.State) {
					return problem.Conflict("В прогоне уже выполняется блок; дождитесь ответа или отмените его")
				}
			}
		} else {
			c, e := caseValue(d, caseID)
			if e != nil {
				return e
			}
			if c.Archived || c.ParentArchived {
				return problem.Archived()
			}
			if c.Revision != revision {
				return problem.Conflict("Сценарий изменился перед запуском")
			}
		}
		number := 1
		for _, a := range d.CallAttempts {
			if a.TestCaseID == caseID && a.BlockID == block.ID && a.RunID == runID && a.VariantID == opt.VariantID && a.AttemptNumber >= number {
				number = a.AttemptNumber + 1
			}
		}
		var res resources.Resource
		for _, v := range snapshot.Resources {
			if v.Name == block.Resource {
				res = v
				break
			}
		}
		if res.ID == "" {
			return problem.Invalid("resource", "Ресурс не найден в снимке")
		}
		envelope := attemptEnvelope{Block: catalog.RequestFromBlock(safeBlock, opt.VariantID == ""), Resource: res, EnvironmentName: snapshot.EnvironmentName}
		d.CallAttempts = append(d.CallAttempts, dataset.CallAttempt{ID: id, RunID: runID, TestCaseID: caseID, BlockID: block.ID, CaseRevision: revision, EnvironmentVersionID: snapshot.EnvironmentVersionID, ResourceVersionID: snapshot.ResourceVersions[block.Resource], AttemptNumber: number, IdempotencyKey: key, RequestHash: hash, State: "queued", SafeRequest: dataset.Raw(envelope), StartedAt: now(), VariantID: opt.VariantID, VariantVersionID: opt.VariantVersionID, BatchID: opt.BatchID, BatchPosition: opt.BatchPosition})
		if opt.BatchID != "" {
			i := index(d.BatchItems, func(v dataset.BatchItem) bool { return v.BatchID == opt.BatchID && v.Position == opt.BatchPosition })
			d.BatchItems[i].State = "running"
			d.BatchItems[i].AttemptID = id
		}
		created = true
		return nil
	})
	if e != nil {
		return execution.Attempt{}, false, e
	}
	a, e := r.GetAttempt(ctx, id)
	return a, created, e
}
func (r *Runs) ClaimAttempt(ctx context.Context, id string) (bool, error) {
	claimed := false
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		i := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == id })
		if i < 0 {
			return problem.NotFound()
		}
		v := &d.CallAttempts[i]
		if v.RunID != "" {
			j := index(d.Runs, func(r dataset.Run) bool { return r.ID == v.RunID })
			if j < 0 {
				return problem.NotFound()
			}
			if d.Runs[j].Status != "running" {
				return nil
			}
		}
		if v.State == "queued" {
			v.State = "running"
			claimed = true
		}
		return nil
	})
	return claimed, e
}
func (r *Runs) CheckAttemptRunnable(ctx context.Context, id string) error {
	d, e := r.Store.data(ctx)
	if e != nil {
		return e
	}
	i := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == id })
	if i < 0 {
		return problem.NotFound()
	}
	a := d.CallAttempts[i]
	if a.State != "running" {
		return context.Canceled
	}
	if a.RunID != "" {
		j := index(d.Runs, func(v dataset.Run) bool { return v.ID == a.RunID })
		if j < 0 || d.Runs[j].Status != "running" {
			return context.Canceled
		}
	}
	return nil
}
func (r *Runs) CompleteAttempt(ctx context.Context, id, state string, result execution.Result, duration time.Duration) error {
	if state != "completed" && state != "error" && state != "timed_out" && state != "cancelled" && state != "interrupted" {
		return problem.Invalid("state", "Некорректный итог попытки")
	}
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		i := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == id })
		if i < 0 {
			return nil
		}
		a := &d.CallAttempts[i]
		if activeAttempt(a.State) {
			a.State = state
			a.Result = dataset.Raw(result)
			a.DurationMS = max(duration.Milliseconds(), 0)
			t := now()
			a.FinishedAt = &t
		}
		return nil
	})
}
func (r *Runs) CancelAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		i := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == id })
		if i < 0 {
			return problem.NotFound()
		}
		a := &d.CallAttempts[i]
		if activeAttempt(a.State) {
			a.State = "cancelled"
			t := now()
			a.FinishedAt = &t
			a.Result = dataset.Raw(runs.InterruptedResult("cancelled"))
		}
		return nil
	})
	if e != nil {
		return execution.Attempt{}, e
	}
	return r.GetAttempt(ctx, id)
}
