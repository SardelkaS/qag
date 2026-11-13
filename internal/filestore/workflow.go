package filestore

import (
	"context"
	"fmt"
	"strings"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dataset"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/runs"
)

func insertBatch(d *dataset.Data, runID, key, hash string, doc catalog.TestCase, selected ...string) string {
	step := ""
	if len(selected) > 0 {
		step = selected[0]
	}
	id := catalog.NewID()
	b := dataset.Batch{StepID: step, ID: id, RunID: runID, IdempotencyKey: key, RequestHash: hash, State: "running", StartedAt: now()}
	position := 0
	for _, s := range doc.Steps {
		if step != "" && s.ID != step {
			continue
		}
		for _, block := range s.Blocks {
			position++
			d.BatchItems = append(d.BatchItems, dataset.BatchItem{BatchID: id, RunID: runID, Position: position, BlockID: block.ID, State: "pending"})
		}
	}
	if position == 0 {
		b.State = "completed"
		t := now()
		b.FinishedAt = &t
	}
	d.Batches = append(d.Batches, b)
	return id
}
func (r *Runs) StartBatch(ctx context.Context, runID, key, hash string, stepID ...string) (runs.Batch, error) {
	id := ""
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, runID)
		if e != nil {
			return e
		}
		for _, b := range d.Batches {
			if b.IdempotencyKey == key {
				if b.RequestHash != hash {
					return problem.Conflict("Ключ команды использован иначе")
				}
				id = b.ID
				return nil
			}
		}
		if run.Status != "running" {
			return problem.Conflict("Прогон завершён")
		}
		for _, b := range run.Batches {
			if b.State == "running" {
				return problem.Conflict("Очередь уже выполняется")
			}
		}
		for _, a := range run.Attempts {
			if activeAttempt(a.State) {
				return problem.Conflict("Дождитесь текущего вызова")
			}
		}
		step := ""
		if len(stepID) > 0 {
			step = stepID[0]
		}
		if _, e = runs.SelectStep(run.Snapshot, step); e != nil {
			return e
		}
		id = insertBatch(d, runID, key, hash, run.Snapshot, step)
		return nil
	})
	if e != nil {
		return runs.Batch{}, e
	}
	run, e := r.Get(ctx, runID)
	for _, b := range run.Batches {
		if b.ID == id {
			return b, e
		}
	}
	return runs.Batch{}, e
}
func stopBatches(d *dataset.Data, runID, state string) {
	for i := range d.Batches {
		b := &d.Batches[i]
		if b.RunID == runID && b.State == "running" {
			b.State = state
			t := now()
			b.FinishedAt = &t
		}
	}
	for i := range d.BatchItems {
		v := &d.BatchItems[i]
		if v.RunID == runID && (v.State == "pending" || v.State == "running") {
			v.State = state
		}
	}
}
func (r *Runs) AdvanceBatch(ctx context.Context, runID, batchID string) error {
	return r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, runID)
		if e != nil {
			return e
		}
		if run.Status != "running" {
			return nil
		}
		for i := range d.BatchItems {
			v := &d.BatchItems[i]
			if v.BatchID == batchID && v.State == "running" {
				a := index(d.CallAttempts, func(a dataset.CallAttempt) bool { return a.ID == v.AttemptID })
				if a >= 0 && !activeAttempt(d.CallAttempts[a].State) {
					v.State = "completed"
				}
			}
		}
		pending := false
		for _, v := range d.BatchItems {
			if v.BatchID == batchID && (v.State == "pending" || v.State == "running") {
				pending = true
			}
		}
		if !pending {
			i := index(d.Batches, func(v dataset.Batch) bool { return v.ID == batchID })
			if i >= 0 && d.Batches[i].State == "running" {
				d.Batches[i].State = "completed"
				t := now()
				d.Batches[i].FinishedAt = &t
			}
		}
		return nil
	})
}
func (r *Runs) SaveVariant(ctx context.Context, runID, blockID, id, name string, revision int64, request *catalog.CallRequest) (runs.Variant, error) {
	e := r.Store.Update(ctx, func(d *dataset.Data) error {
		run, e := getRun(d, runID)
		if e != nil {
			return e
		}
		if run.Status != "running" {
			return problem.Conflict("История доступна только для просмотра")
		}
		for _, b := range run.Batches {
			if b.State == "running" {
				return problem.Conflict("Дождитесь завершения очереди")
			}
		}
		for _, a := range run.Attempts {
			if activeAttempt(a.State) {
				return problem.Conflict("Дождитесь выполняющегося вызова")
			}
		}
		position := 1
		i := -1
		if id != "" {
			i = index(d.Variants, func(v dataset.Variant) bool { return v.ID == id && v.RunID == runID })
			if i < 0 {
				return problem.NotFound()
			}
			old := d.Variants[i]
			if old.Revision != revision {
				return problem.Conflict("Вариант изменён в другой вкладке; введённый текст сохранён в форме")
			}
			blockID = old.BlockID
			position = old.Position
		} else {
			for _, v := range run.Variants {
				if v.BlockID == blockID && v.Position >= position {
					position = v.Position + 1
				}
			}
			if position > 20 {
				return problem.Invalid("variants", "У вызова может быть не больше 20 вариантов")
			}
			id = catalog.NewID()
			revision = 0
		}
		parent, e := runs.ParentBlock(run, blockID)
		if e != nil {
			return e
		}
		payload := parent
		payload.Expected = catalog.Expectation{}
		if request != nil {
			if request.Expected != nil {
				return problem.Invalid("request.expected", "У исследовательского вызова нет ожидаемого результата")
			}
			payload.HTTP = request.HTTP
			payload.GRPC = request.GRPC
			payload.SQL = request.SQL
			payload.TimeoutSeconds = request.TimeoutSeconds
		}
		if e = catalog.ValidateDraft([]catalog.Step{{ID: catalog.NewID(), Blocks: []catalog.Block{payload}}}); e != nil {
			return e
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("Вариант %d", position)
		}
		if len([]rune(name)) > 200 {
			return problem.Invalid("name", "Не больше 200 символов")
		}
		version := catalog.NewID()
		if i < 0 {
			d.Variants = append(d.Variants, dataset.Variant{ID: id, RunID: runID, BlockID: blockID, Name: name, Position: position, Revision: 1, CurrentVersionID: version})
		} else {
			v := &d.Variants[i]
			v.Name = name
			v.Revision++
			v.CurrentVersionID = version
		}
		d.VariantVersions = append(d.VariantVersions, dataset.VariantVersion{ID: version, VariantID: id, RunID: runID, BlockID: blockID, Revision: revision + 1, Request: dataset.Raw(catalog.RequestFromBlock(payload, false)), CreatedAt: now()})
		return nil
	})
	if e != nil {
		return runs.Variant{}, e
	}
	run, e := r.Get(ctx, runID)
	if e != nil {
		return runs.Variant{}, e
	}
	for _, v := range run.Variants {
		if v.ID == id {
			return v, nil
		}
	}
	return runs.Variant{}, problem.NotFound()
}
