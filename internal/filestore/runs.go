package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sort"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/ports"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

type Runs struct{ Store *Store }

var _ ports.Runs = (*Runs)(nil)

type attemptEnvelope struct {
	Block           catalog.CallRequest `json:"block"`
	Resource        resources.Resource  `json:"resource"`
	EnvironmentName string              `json:"environmentName"`
}

func attemptValue(v dataset.CallAttempt) (execution.Attempt, error) {
	env, e := dataset.Value[attemptEnvelope](v.SafeRequest)
	if e != nil {
		return execution.Attempt{}, storageError()
	}
	var result *execution.Result
	if v.Result != "" {
		result, e = dataset.Value[*execution.Result](v.Result)
		if e != nil {
			return execution.Attempt{}, storageError()
		}
	}
	if v.VariantID != "" {
		env.Block.Expected = nil
		if result != nil {
			result.Comparison = execution.ResearchComparison()
		}
	}
	return execution.Attempt{ID: v.ID, RunID: v.RunID, TestCaseID: v.TestCaseID, BlockID: v.BlockID, Revision: v.CaseRevision, EnvironmentID: env.Resource.EnvironmentID, EnvironmentName: env.EnvironmentName, Resource: env.Resource, Request: env.Block, Number: v.AttemptNumber, State: v.State, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, DurationMS: v.DurationMS, VariantID: v.VariantID, VariantVersionID: v.VariantVersionID, BatchID: v.BatchID, BatchPosition: v.BatchPosition, Result: result}, nil
}
func getRun(d *dataset.Data, id string) (runs.Run, error) {
	if e := valid(id); e != nil {
		return runs.Run{}, e
	}
	i := index(d.Runs, func(v dataset.Run) bool { return v.ID == id })
	if i < 0 {
		return runs.Run{}, problem.NotFound()
	}
	v := d.Runs[i]
	c, e := dataset.Value[catalog.TestCase](v.Snapshot)
	if e != nil {
		return runs.Run{}, storageError()
	}
	env, e := dataset.Value[resources.Snapshot](v.Context)
	if e != nil {
		return runs.Run{}, storageError()
	}
	rs := env.Resources
	if rs == nil {
		rs = []resources.Resource{}
	}
	reasons := v.RetentionReasons
	if reasons == nil {
		reasons = []string{}
	}
	r := runs.Run{ID: v.ID, TestCaseID: v.TestCaseID, Status: v.Status, Snapshot: c, EnvironmentID: env.EnvironmentID, EnvironmentName: env.EnvironmentName, Resources: rs, RetentionReasons: reasons, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, StepStates: []runs.StepState{}, Attempts: []execution.Attempt{}, Variants: []runs.Variant{}, Batches: []runs.Batch{}}
	for _, s := range c.Steps {
		for _, state := range d.RunSteps {
			if state.RunID == id && state.StepID == s.ID {
				ss := runs.StepState{StepID: s.ID, Status: state.Status, Comment: state.Comment, Revision: state.Revision}
				if ss.Status == "blocked" || ss.Status == "skipped" {
					ss.LegacyStatus = ss.Status
					ss.Status = "not_run"
				}
				r.StepStates = append(r.StepStates, ss)
			}
		}
	}
	for _, a := range d.CallAttempts {
		if a.RunID == id {
			v, e := attemptValue(a)
			if e != nil {
				return r, e
			}
			r.Attempts = append(r.Attempts, v)
		}
	}
	sort.Slice(r.Attempts, func(i, j int) bool {
		a, b := r.Attempts[i], r.Attempts[j]
		if !a.StartedAt.Equal(b.StartedAt) {
			return a.StartedAt.Before(b.StartedAt)
		}
		if a.Number != b.Number {
			return a.Number < b.Number
		}
		return a.ID < b.ID
	})
	for _, v := range d.Variants {
		if v.RunID == id {
			cv := runs.Variant{ID: v.ID, RunID: id, BlockID: v.BlockID, Name: v.Name, Position: v.Position, Revision: v.Revision, CurrentVersionID: v.CurrentVersionID, Versions: []runs.VariantVersion{}}
			for _, ver := range d.VariantVersions {
				if ver.VariantID == v.ID {
					req, e := dataset.Value[catalog.CallRequest](ver.Request)
					if e != nil {
						return r, storageError()
					}
					req.Expected = nil
					cv.Versions = append(cv.Versions, runs.VariantVersion{ID: ver.ID, Revision: ver.Revision, Request: req, CreatedAt: ver.CreatedAt})
				}
			}
			sort.Slice(cv.Versions, func(i, j int) bool { return cv.Versions[i].Revision < cv.Versions[j].Revision })
			r.Variants = append(r.Variants, cv)
		}
	}
	sort.Slice(r.Variants, func(i, j int) bool {
		if r.Variants[i].BlockID != r.Variants[j].BlockID {
			return r.Variants[i].BlockID < r.Variants[j].BlockID
		}
		return r.Variants[i].Position < r.Variants[j].Position
	})
	for _, b := range d.Batches {
		if b.RunID == id {
			cb := runs.Batch{ID: b.ID, RunID: id, State: b.State, StartedAt: b.StartedAt, FinishedAt: b.FinishedAt, Items: []runs.BatchItem{}}
			for _, item := range d.BatchItems {
				if item.BatchID == b.ID {
					cb.Items = append(cb.Items, runs.BatchItem{Position: item.Position, BlockID: item.BlockID, State: item.State, AttemptID: item.AttemptID})
					if item.State == "completed" {
						cb.Completed++
						a := index(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.ID == item.AttemptID })
						match := false
						if a >= 0 {
							v, e := attemptValue(d.CallAttempts[a])
							match = e == nil && v.State == "completed" && v.Result != nil && v.Result.Comparison.Status == "match"
						}
						if !match {
							cb.Problems++
						}
					}
				}
			}
			sort.Slice(cb.Items, func(i, j int) bool { return cb.Items[i].Position < cb.Items[j].Position })
			r.Batches = append(r.Batches, cb)
		}
	}
	sort.Slice(r.Batches, func(i, j int) bool {
		if !r.Batches[i].StartedAt.Equal(r.Batches[j].StartedAt) {
			return r.Batches[i].StartedAt.Before(r.Batches[j].StartedAt)
		}
		return r.Batches[i].ID < r.Batches[j].ID
	})
	return r, nil
}
func (r *Runs) Get(ctx context.Context, id string) (runs.Run, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return runs.Run{}, e
	}
	return getRun(&d, id)
}
func (r *Runs) Context(ctx context.Context, id string) (resources.Snapshot, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return resources.Snapshot{}, e
	}
	i := index(d.Runs, func(v dataset.Run) bool { return v.ID == id })
	if i < 0 {
		return resources.Snapshot{}, problem.NotFound()
	}
	return dataset.Value[resources.Snapshot](d.Runs[i].Context)
}
func (r *Runs) LookupRun(ctx context.Context, key, hash string) (runs.Run, bool, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return runs.Run{}, false, e
	}
	for _, v := range d.Runs {
		if v.IdempotencyKey == key {
			if v.RequestHash != hash {
				return runs.Run{}, true, problem.Conflict("Этот ключ команды уже использован с другими параметрами")
			}
			run, e := getRun(&d, v.ID)
			return run, true, e
		}
	}
	return runs.Run{}, false, nil
}
func (r *Runs) Start(ctx context.Context, snapshot catalog.TestCase, env resources.Snapshot, key, hash string, executeAll ...bool) (runs.Run, error) {
	id := ""
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		for _, v := range d.Runs {
			if v.IdempotencyKey == key {
				if v.RequestHash != hash {
					return problem.Conflict("Ключ команды использован иначе")
				}
				id = v.ID
				return nil
			}
		}
		c, e := caseValue(d, snapshot.ID)
		if e != nil {
			return e
		}
		if c.Archived || c.ParentArchived {
			return problem.Archived()
		}
		if c.Revision != snapshot.Revision {
			return problem.Conflict("Сценарий изменился перед началом прогона")
		}
		for _, v := range d.Runs {
			if v.TestCaseID == snapshot.ID && v.Status == "running" {
				id = v.ID
				return nil
			}
		}
		id = catalog.NewID()
		d.Runs = append(d.Runs, dataset.Run{ID: id, TestCaseID: snapshot.ID, EnvironmentVersionID: env.EnvironmentVersionID, Snapshot: dataset.Raw(snapshot), Context: dataset.Raw(env), Status: "running", RetentionReasons: []string{}, IdempotencyKey: key, RequestHash: hash, StartedAt: now()})
		for _, s := range snapshot.Steps {
			d.RunSteps = append(d.RunSteps, dataset.RunStep{RunID: id, StepID: s.ID, Status: "not_run", Revision: 1})
		}
		for name, v := range env.ResourceVersions {
			d.RunResourceVersions = append(d.RunResourceVersions, dataset.RunResourceVersion{RunID: id, ResourceName: name, ResourceVersionID: v})
		}
		if len(executeAll) > 0 && executeAll[0] {
			insertBatch(d, id, key, hash, snapshot)
		}
		return nil
	})
	if e != nil {
		return runs.Run{}, e
	}
	return r.Get(ctx, id)
}
func (r *Runs) List(ctx context.Context, caseID string, retained *bool, limit int, cursor string, active ...bool) (catalog.Page[runs.Run], error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return catalog.Page[runs.Run]{}, e
	}
	if _, e = caseValue(&d, caseID); e != nil {
		return catalog.Page[runs.Run]{}, e
	}
	running := len(active) > 0 && active[0]
	items := []runs.Run{}
	for _, v := range d.Runs {
		if v.TestCaseID == caseID && (v.Status == "running") == running && (retained == nil || (len(v.RetentionReasons) > 0) == *retained) {
			cv, e := getRun(&d, v.ID)
			if e != nil {
				return catalog.Page[runs.Run]{}, e
			}
			items = append(items, cv)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i].StartedAt, items[j].StartedAt
		if items[i].FinishedAt != nil {
			a = *items[i].FinishedAt
		}
		if items[j].FinishedAt != nil {
			b = *items[j].FinishedAt
		}
		if !a.Equal(b) {
			return a.After(b)
		}
		return items[i].ID > items[j].ID
	})
	return catalog.Paginate(items, limit, cursor, func(v runs.Run) string { return v.ID })
}
func (r *Runs) UpdateStep(ctx context.Context, id, stepID, status, comment string, revision int64) (runs.Run, error) {
	if status != "not_run" && status != "passed" && status != "failed" {
		return runs.Run{}, problem.Invalid("status", "Выберите ручной статус шага")
	}
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, id)
		if e != nil {
			return e
		}
		if run.Status != "running" {
			return problem.Conflict("Завершённый прогон доступен только для просмотра")
		}
		i := index(d.RunSteps, func(v dataset.RunStep) bool { return v.RunID == id && v.StepID == stepID })
		if i < 0 {
			return problem.NotFound()
		}
		v := &d.RunSteps[i]
		if v.Revision != revision {
			return problem.Conflict("Шаг изменён в другой вкладке; обновите прогон")
		}
		v.Status = status
		v.Comment = comment
		v.Revision++
		return nil
	})
	if e != nil {
		return runs.Run{}, e
	}
	return r.Get(ctx, id)
}
func (r *Runs) PassAll(ctx context.Context, id string, steps []runs.StepRevision) (runs.Run, error) {
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, id)
		if e != nil {
			return e
		}
		if run.Status != "running" {
			return problem.Conflict("Завершённый прогон доступен только для просмотра")
		}
		if len(steps) != len(run.StepStates) {
			return problem.Invalid("steps", "Нужны версии всех шагов прогона")
		}
		revs := map[string]int64{}
		for _, v := range steps {
			if !catalog.ValidID(v.StepID) || v.Revision < 1 {
				return problem.Invalid("steps", "Некорректная версия шага")
			}
			if _, ok := revs[v.StepID]; ok {
				return problem.Invalid("steps", "Шаги не должны повторяться")
			}
			revs[v.StepID] = v.Revision
		}
		for _, v := range run.StepStates {
			rev, ok := revs[v.StepID]
			if !ok {
				return problem.Invalid("steps", "Шаг не относится к этому снимку")
			}
			if rev != v.Revision {
				return problem.Conflict("Шаг изменён в другой вкладке; обновите прогон")
			}
		}
		for i := range d.RunSteps {
			v := &d.RunSteps[i]
			if v.RunID == id && v.Status != "passed" {
				v.Status = "passed"
				v.Revision++
			}
		}
		return nil
	})
	if e != nil {
		return runs.Run{}, e
	}
	return r.Get(ctx, id)
}
func finish(d *dataset.Data, id, forced string) error {
	run, e := getRun(d, id)
	if e != nil {
		return e
	}
	if run.Status != "running" {
		return nil
	}
	if forced == "" {
		for _, b := range run.Batches {
			if b.State == "running" {
				return problem.Conflict("Дождитесь очереди или отмените прогон")
			}
		}
		for _, a := range run.Attempts {
			if activeAttempt(a.State) {
				return problem.Conflict("Дождитесь вызова или отмените прогон")
			}
		}
	} else {
		stopBatches(d, id, forced)
		for i := range d.CallAttempts {
			a := &d.CallAttempts[i]
			if a.RunID == id && activeAttempt(a.State) {
				a.State = forced
				t := now()
				a.FinishedAt = &t
				a.Result = dataset.Raw(runs.InterruptedResult(forced))
			}
		}
	}
	status := forced
	if status == "" {
		status = runs.Evaluate(run)
	}
	first, success := false, false
	for _, v := range d.Runs {
		if v.TestCaseID == run.TestCaseID {
			for _, reason := range v.RetentionReasons {
				first = first || reason == "first_run"
				success = success || reason == "first_success"
			}
		}
	}
	i := index(d.Runs, func(v dataset.Run) bool { return v.ID == id })
	v := &d.Runs[i]
	v.Status = status
	t := now()
	v.FinishedAt = &t
	v.RetentionReasons = runs.RetentionReasons(status, first, success)
	return nil
}
func (r *Runs) Finish(ctx context.Context, id string) (runs.Run, error) { return r.finish(ctx, id, "") }
func (r *Runs) Cancel(ctx context.Context, id string) (runs.Run, error) {
	return r.finish(ctx, id, "cancelled")
}
func (r *Runs) finish(ctx context.Context, id, forced string) (runs.Run, error) {
	e := r.Store.Update(ctx, func(d *dataset.Data) error { return finish(d, id, forced) })
	if e != nil {
		return runs.Run{}, e
	}
	return r.Get(ctx, id)
}
func (r *Runs) Save(ctx context.Context, id string) (runs.Run, error) {
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, id)
		if e != nil {
			return e
		}
		if run.Status == "running" {
			return problem.Conflict("Сначала завершите прогон")
		}
		i := index(d.Runs, func(v dataset.Run) bool { return v.ID == id })
		for _, reason := range d.Runs[i].RetentionReasons {
			if reason == "manual" {
				return nil
			}
		}
		d.Runs[i].RetentionReasons = append(d.Runs[i].RetentionReasons, "manual")
		return nil
	})
	if e != nil {
		return runs.Run{}, e
	}
	return r.Get(ctx, id)
}
func (r *Runs) Delete(ctx context.Context, id string) error {
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, id)
		if e != nil {
			return e
		}
		if run.Status == "running" || len(run.RetentionReasons) > 0 {
			return problem.Conflict("Сохранённый или текущий прогон удалять нельзя")
		}
		d.Runs = filter(d.Runs, func(v dataset.Run) bool { return v.ID != id })
		d.RunSteps = filter(d.RunSteps, func(v dataset.RunStep) bool { return v.RunID != id })
		d.RunResourceVersions = filter(d.RunResourceVersions, func(v dataset.RunResourceVersion) bool { return v.RunID != id })
		d.CallAttempts = filter(d.CallAttempts, func(v dataset.CallAttempt) bool { return v.RunID != id })
		d.Variants = filter(d.Variants, func(v dataset.Variant) bool { return v.RunID != id })
		d.VariantVersions = filter(d.VariantVersions, func(v dataset.VariantVersion) bool { return v.RunID != id })
		d.Batches = filter(d.Batches, func(v dataset.Batch) bool { return v.RunID != id })
		d.BatchItems = filter(d.BatchItems, func(v dataset.BatchItem) bool { return v.RunID != id })
		return nil
	})
}
func filter[T any](items []T, keep func(T) bool) []T {
	out := []T{}
	for _, v := range items {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}
func (r *Runs) Recover(ctx context.Context) error {
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		ids := []string{}
		for _, v := range d.Runs {
			if v.Status == "running" {
				ids = append(ids, v.ID)
			}
		}
		sort.Slice(ids, func(i, j int) bool {
			a := d.Runs[index(d.Runs, func(v dataset.Run) bool { return v.ID == ids[i] })]
			b := d.Runs[index(d.Runs, func(v dataset.Run) bool { return v.ID == ids[j] })]
			if !a.StartedAt.Equal(b.StartedAt) {
				return a.StartedAt.Before(b.StartedAt)
			}
			return a.ID < b.ID
		})
		for _, id := range ids {
			if e := finish(d, id, "interrupted"); e != nil {
				return e
			}
		}
		for i := range d.CallAttempts {
			a := &d.CallAttempts[i]
			if a.RunID == "" && activeAttempt(a.State) {
				a.State = "interrupted"
				t := now()
				a.FinishedAt = &t
				a.Result = dataset.Raw(runs.InterruptedResult("interrupted"))
			}
		}
		return nil
	})
}
func (r *Runs) ReportSnapshot(ctx context.Context, id string, expected bool) (runs.Report, error) {
	d, e := r.Store.data(ctx)
	if e != nil {
		return runs.Report{}, e
	}
	run, e := getRun(&d, id)
	if e != nil {
		return runs.Report{}, e
	}
	report, ids := runs.ProjectReport(run, expected)
	total := 0
	for _, id := range ids {
		i := index(d.ExpectedImages, func(v dataset.Image) bool { return v.ID == id && v.TestCaseID == run.TestCaseID })
		if i < 0 {
			return report, problem.Invalid("report", "Не удалось прочитать все эталоны отчёта")
		}
		v := d.ExpectedImages[i]
		total += len(v.Data)
		if total > 48<<20 {
			return report, problem.Invalid("report", "Эталоны отчёта превышают 48 MiB")
		}
		hash := sha256.Sum256(v.Data)
		if hex.EncodeToString(hash[:]) != v.SHA256 {
			return report, problem.Invalid("report", "Эталон повреждён")
		}
		report.Images = append(report.Images, runs.ReportImage{ID: v.ID, MIME: v.ContentType, SHA256: v.SHA256, Width: v.Width, Height: v.Height, Data: base64.StdEncoding.EncodeToString(v.Data)})
	}
	return report, nil
}
