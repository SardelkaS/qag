package runs

import (
	"context"
	"errors"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/jackc/pgx/v5"
)

func loadBatches(ctx context.Context, d db, runID string) ([]Batch, error) {
	rows, err := d.Query(ctx, dbscope.Render(d, `SELECT id::text,run_id::text,state,started_at,finished_at,coalesce(step_id::text,'') FROM qag.run_batches WHERE run_id=$1 ORDER BY started_at,id`), runID)
	if err != nil {
		return nil, problem.Database(err)
	}
	result := []Batch{}
	for rows.Next() {
		var b Batch
		if err = rows.Scan(&b.ID, &b.RunID, &b.State, &b.StartedAt, &b.FinishedAt, &b.StepID); err != nil {
			rows.Close()
			return nil, problem.Database(err)
		}
		b.Items = []BatchItem{}
		result = append(result, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, problem.Database(err)
	}
	for i := range result {
		items, e := d.Query(ctx, dbscope.Render(d, `SELECT i.position,i.block_id::text,i.state,coalesce(i.attempt_id::text,''),coalesce(a.state='completed' AND a.result->'comparison'->>'status'='match',false) FROM qag.run_batch_items i LEFT JOIN qag.call_attempts a ON a.id=i.attempt_id WHERE i.batch_id=$1 ORDER BY i.position`), result[i].ID)
		if e != nil {
			return nil, problem.Database(e)
		}
		for items.Next() {
			var item BatchItem
			var match bool
			if e = items.Scan(&item.Position, &item.BlockID, &item.State, &item.AttemptID, &match); e != nil {
				items.Close()
				return nil, problem.Database(e)
			}
			if item.State == "completed" {
				result[i].Completed++
				if !match {
					result[i].Problems++
				}
			}
			result[i].Items = append(result[i].Items, item)
		}
		e = items.Err()
		items.Close()
		if e != nil {
			return nil, problem.Database(e)
		}
	}
	return result, nil
}
func insertBatch(ctx context.Context, tx pgx.Tx, runID, key, hash string, doc catalog.TestCase, selected ...string) (string, error) {
	step := ""
	if len(selected) > 0 {
		step = selected[0]
	}
	id := catalog.NewID()
	if _, err := tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_batches(id,run_id,idempotency_key,request_hash,state,step_id) VALUES($1,$2,$3,$4,'running',nullif($5,'')::uuid)`), id, runID, key, hash, step); err != nil {
		return "", problem.Database(err)
	}
	position := 0
	for _, s := range doc.Steps {
		if step != "" && s.ID != step {
			continue
		}
		for _, b := range s.Blocks {
			position++
			if _, err := tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_batch_items(batch_id,run_id,position,block_id) VALUES($1,$2,$3,$4)`), id, runID, position, b.ID); err != nil {
				return "", problem.Database(err)
			}
		}
	}
	if position == 0 {
		_, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batches SET state='completed',finished_at=clock_timestamp() WHERE id=$1`), id)
		return id, problem.Database(err)
	}
	return id, nil
}
func (r *Repository) StartBatch(ctx context.Context, runID, key, hash string, stepID ...string) (Batch, error) {
	var id string
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, runID, true)
		if err != nil {
			return err
		}
		var stored string
		err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text,request_hash FROM qag.run_batches WHERE idempotency_key=$1`), key).Scan(&id, &stored)
		if err == nil {
			if stored != hash {
				return problem.Conflict("Ключ команды использован иначе")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return problem.Database(err)
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
			if a.State == "queued" || a.State == "running" {
				return problem.Conflict("Дождитесь текущего вызова")
			}
		}
		step := ""
		if len(stepID) > 0 {
			step = stepID[0]
		}
		if _, err = SelectStep(run.Snapshot, step); err != nil {
			return err
		}
		id, err = insertBatch(ctx, tx, runID, key, hash, run.Snapshot, step)
		return err
	})
	if err != nil {
		return Batch{}, err
	}
	items, err := loadBatches(ctx, r.pool, runID)
	for _, b := range items {
		if b.ID == id {
			return b, err
		}
	}
	return Batch{}, problem.Database(err)
}
func cancelBatches(ctx context.Context, tx pgx.Tx, runID, state string) error {
	if _, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batches SET state=$2,finished_at=clock_timestamp() WHERE run_id=$1 AND state='running'`), runID, state); err != nil {
		return problem.Database(err)
	}
	_, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batch_items SET state=$2 WHERE run_id=$1 AND state IN ('pending','running')`), runID, state)
	return problem.Database(err)
}
func (r *Repository) AdvanceBatch(ctx context.Context, runID, batchID string) error {
	return r.transaction(ctx, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT status FROM qag.runs WHERE id=$1 FOR UPDATE`), runID).Scan(&status); err != nil {
			return problem.Database(err)
		}
		if status != "running" {
			return nil
		}
		if _, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batch_items i SET state='completed' FROM qag.call_attempts a WHERE i.batch_id=$1 AND i.attempt_id=a.id AND i.state='running' AND a.state NOT IN ('queued','running')`), batchID); err != nil {
			return problem.Database(err)
		}
		_, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batches b SET state='completed',finished_at=clock_timestamp() WHERE b.id=$1 AND b.state='running' AND NOT EXISTS(SELECT 1 FROM qag.run_batch_items WHERE batch_id=b.id AND state IN ('pending','running'))`), batchID)
		return problem.Database(err)
	})
}
