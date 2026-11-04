package runs

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"errors"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/comparison"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/jackc/pgx/v5"
)

type attemptEnvelope struct {
	Block           catalog.CallRequest `json:"block"`
	Resource        resources.Resource  `json:"resource"`
	EnvironmentName string              `json:"environmentName"`
}

const attemptSelect = `SELECT id::text,run_id::text,test_case_id::text,block_id::text,case_revision,attempt_number,state,safe_request,result,started_at,finished_at,duration_ms,coalesce(variant_id::text,''),coalesce(variant_version_id::text,''),coalesce(batch_id::text,''),coalesce(batch_position,0) FROM qag.call_attempts`

type scanner interface{ Scan(...any) error }

func scanAttempt(row scanner) (execution.Attempt, error) {
	var attempt execution.Attempt
	var runID *string
	var envelope attemptEnvelope
	err := row.Scan(&attempt.ID, &runID, &attempt.TestCaseID, &attempt.BlockID, &attempt.Revision, &attempt.Number, &attempt.State, &envelope, &attempt.Result, &attempt.StartedAt, &attempt.FinishedAt, &attempt.DurationMS, &attempt.VariantID, &attempt.VariantVersionID, &attempt.BatchID, &attempt.BatchPosition)
	if runID != nil {
		attempt.RunID = *runID
	}
	attempt.Request = envelope.Block
	if attempt.VariantID != "" {
		attempt.Request.Expected = nil
		if attempt.Result != nil {
			attempt.Result.Comparison = execution.ResearchComparison()
		}
	}
	attempt.Resource = envelope.Resource
	attempt.EnvironmentName = envelope.EnvironmentName
	attempt.EnvironmentID = envelope.Resource.EnvironmentID
	return attempt, problem.Database(err)
}
func getAttempt(ctx context.Context, d db, id string) (execution.Attempt, error) {
	if err := validID(id); err != nil {
		return execution.Attempt{}, err
	}
	return scanAttempt(d.QueryRow(ctx, dbscope.Render(d, attemptSelect+` WHERE id=$1`), id))
}
func (r *Repository) GetAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	return getAttempt(ctx, r.pool, id)
}
func loadAttempts(ctx context.Context, d db, id string) ([]execution.Attempt, error) {
	rows, err := d.Query(ctx, dbscope.Render(d, attemptSelect+` WHERE run_id=$1 ORDER BY started_at,attempt_number,id`), id)
	if err != nil {
		return nil, problem.Database(err)
	}
	defer rows.Close()
	result := []execution.Attempt{}
	for rows.Next() {
		item, err := scanAttempt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, problem.Database(rows.Err())
}
func (r *Repository) LookupAttempt(ctx context.Context, key, hash string) (execution.Attempt, bool, error) {
	var id, stored string
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT id::text,request_hash FROM qag.call_attempts WHERE idempotency_key=$1`), key).Scan(&id, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return execution.Attempt{}, false, nil
	}
	if err != nil {
		return execution.Attempt{}, false, problem.Database(err)
	}
	if hash != stored {
		return execution.Attempt{}, true, problem.Conflict("Ключ команды уже использован с другими параметрами")
	}
	attempt, err := r.GetAttempt(ctx, id)
	return attempt, true, err
}
func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type AttemptOptions struct {
	VariantID, VariantVersionID, BatchID string
	BatchPosition                        int
}

func (r *Repository) BeginAttempt(ctx context.Context, candidateID, caseID, runID string, revision int64, block catalog.Block, snapshot resources.Snapshot, safeBlock catalog.Block, key, hash string, options ...AttemptOptions) (execution.Attempt, bool, error) {
	opt := AttemptOptions{}
	if len(options) > 0 {
		opt = options[0]
	}
	var attempt execution.Attempt
	created := false
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, dbscope.Render(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`), key); err != nil {
			return problem.Database(err)
		}
		var id, stored string
		err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text,request_hash FROM qag.call_attempts WHERE idempotency_key=$1`), key).Scan(&id, &stored)
		if err == nil {
			if stored != hash {
				return problem.Conflict("Ключ команды использован иначе")
			}
			attempt, err = getAttempt(ctx, tx, id)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return problem.Database(err)
		}
		if runID != "" {
			// Inserting an attempt takes a testcase FK key-share lock. Acquire it
			// before the run lock, matching finish/cancel's retention lock order.
			// Otherwise immediate cancel can deadlock against that implicit FK.
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `SELECT id FROM qag.test_cases WHERE id=$1 FOR KEY SHARE`), caseID); err != nil {
				return problem.Database(err)
			}
			var status string
			if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT status FROM qag.runs WHERE id=$1 AND test_case_id=$2 FOR UPDATE`), runID, caseID).Scan(&status); err != nil {
				return problem.Database(err)
			}
			if status != "running" {
				return problem.Conflict("Завершённый прогон доступен только для просмотра")
			}
			var activeBatch string
			err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text FROM qag.run_batches WHERE run_id=$1 AND state='running' FOR UPDATE`), runID).Scan(&activeBatch)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return problem.Database(err)
			}
			if opt.BatchID == "" && activeBatch != "" {
				return problem.Conflict("Дождитесь завершения очереди")
			}
			if opt.BatchID != "" {
				if activeBatch != opt.BatchID {
					return problem.Conflict("Очередь уже остановлена")
				}
				var state string
				if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT state FROM qag.run_batch_items WHERE batch_id=$1 AND position=$2 AND run_id=$3 AND block_id=$4 FOR UPDATE`), opt.BatchID, opt.BatchPosition, runID, block.ID).Scan(&state); err != nil {
					return problem.Database(err)
				}
				if state != "pending" {
					return problem.Conflict("Элемент очереди уже запущен")
				}
			}
			if opt.VariantID != "" {
				var current string
				if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT current_version_id::text FROM qag.run_call_variants WHERE id=$1 AND run_id=$2 AND block_id=$3 FOR UPDATE`), opt.VariantID, runID, block.ID).Scan(&current); err != nil {
					return problem.Database(err)
				}
				if current != opt.VariantVersionID {
					return problem.Conflict("Сохранённая версия варианта изменилась")
				}
			}
			var busy bool
			if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT EXISTS(SELECT 1 FROM qag.call_attempts WHERE run_id=$1 AND state IN ('queued','running'))`), runID).Scan(&busy); err != nil {
				return problem.Database(err)
			}
			if busy {
				return problem.Conflict("В прогоне уже выполняется блок; дождитесь ответа или отмените его")
			}
		} else {
			var current int64
			var archived, parent bool
			if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT c.revision,c.archived_at IS NOT NULL,f.archived_at IS NOT NULL FROM qag.test_cases c JOIN qag.folders f ON f.id=c.folder_id WHERE c.id=$1 FOR UPDATE OF f,c`), caseID).Scan(&current, &archived, &parent); err != nil {
				return problem.Database(err)
			}
			if archived || parent {
				return problem.Archived()
			}
			if current != revision {
				return problem.Conflict("Сценарий изменился перед запуском")
			}
		}
		var number int
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT coalesce(max(attempt_number),0)+1 FROM qag.call_attempts WHERE test_case_id=$1 AND block_id=$2 AND run_id IS NOT DISTINCT FROM $3::uuid AND variant_id IS NOT DISTINCT FROM $4::uuid`), caseID, block.ID, nullableID(runID), nullableID(opt.VariantID)).Scan(&number); err != nil {
			return problem.Database(err)
		}
		resource := resources.Resource{}
		for _, item := range snapshot.Resources {
			if item.Name == block.Resource {
				resource = item
				break
			}
		}
		if resource.ID == "" {
			return problem.Invalid("resource", "Ресурс не найден в снимке")
		}
		envelope := attemptEnvelope{Block: catalog.RequestFromBlock(safeBlock, opt.VariantID == ""), Resource: resource, EnvironmentName: snapshot.EnvironmentName}
		id = candidateID
		_, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.call_attempts(id,run_id,test_case_id,block_id,case_revision,environment_version_id,resource_version_id,attempt_number,idempotency_key,request_hash,safe_request,variant_id,variant_version_id,batch_id,batch_position) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`), id, nullableID(runID), caseID, block.ID, revision, snapshot.EnvironmentVersionID, snapshot.ResourceVersions[block.Resource], number, key, hash, envelope, nullableID(opt.VariantID), nullableID(opt.VariantVersionID), nullableID(opt.BatchID), func() any {
			if opt.BatchID == "" {
				return nil
			}
			return opt.BatchPosition
		}())
		if err != nil {
			return problem.Database(err)
		}
		if opt.BatchID != "" {
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_batch_items SET state='running',attempt_id=$3 WHERE batch_id=$1 AND position=$2`), opt.BatchID, opt.BatchPosition, id); err != nil {
				return problem.Database(err)
			}
		}
		attempt, err = getAttempt(ctx, tx, id)
		created = err == nil
		return err
	})
	return attempt, created, err
}

func (r *Repository) ClaimAttempt(ctx context.Context, id string) (bool, error) {
	claimed := false
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		var runID *string
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT run_id::text FROM qag.call_attempts WHERE id=$1`), id).Scan(&runID); err != nil {
			return problem.Database(err)
		}
		if runID != nil {
			var state string
			if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT status FROM qag.runs WHERE id=$1 FOR UPDATE`), *runID).Scan(&state); err != nil {
				return problem.Database(err)
			}
			if state != "running" {
				return nil
			}
		}
		tag, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.call_attempts SET state='running' WHERE id=$1 AND state='queued'`), id)
		claimed = tag.RowsAffected() == 1
		return problem.Database(err)
	})
	return claimed, err
}
func (r *Repository) CheckAttemptRunnable(ctx context.Context, id string) error {
	var allowed bool
	if err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT a.state='running' AND (a.run_id IS NULL OR r.status='running') FROM qag.call_attempts a LEFT JOIN qag.runs r ON r.id=a.run_id WHERE a.id=$1`), id).Scan(&allowed); err != nil {
		return problem.Database(err)
	}
	if !allowed {
		return context.Canceled
	}
	return nil
}
func (r *Repository) CompleteAttempt(ctx context.Context, id, state string, result execution.Result, duration time.Duration) error {
	if state != "completed" && state != "error" && state != "timed_out" && state != "cancelled" && state != "interrupted" {
		return problem.Invalid("state", "Некорректный итог попытки")
	}
	_, err := r.pool.Exec(ctx, dbscope.Render(r.pool, `UPDATE qag.call_attempts SET state=$2,result=$3,duration_ms=$4,finished_at=clock_timestamp() WHERE id=$1 AND state IN ('queued','running')`), id, state, result, max(duration.Milliseconds(), 0))
	return problem.Database(err)
}
func (r *Repository) CancelAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	if err := validID(id); err != nil {
		return execution.Attempt{}, err
	}
	_, err := r.pool.Exec(ctx, dbscope.Render(r.pool, `UPDATE qag.call_attempts SET state='cancelled',result=$2,finished_at=clock_timestamp() WHERE id=$1 AND state IN ('queued','running')`), id, interruptedResult("cancelled"))
	if err != nil {
		return execution.Attempt{}, problem.Database(err)
	}
	return r.GetAttempt(ctx, id)
}
func interruptedResult(state string) execution.Result { return InterruptedResult(state) }

// InterruptedResult is shared by every durable backend's recovery path.
func InterruptedResult(state string) execution.Result {
	message := "Процесс остановлен; результат внешнего действия неизвестен. Автоматического повтора не было"
	if state == "cancelled" {
		message = "Попытка отменена; внешнее действие могло уже выполниться"
	}
	return execution.Result{Headers: []catalog.KeyValue{}, Error: message, Comparison: comparison.Result{Status: "not_compared", Reason: message, Differences: []comparison.Difference{}}}
}
func cancelAttempts(ctx context.Context, tx pgx.Tx, runID, state string) error {
	_, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.call_attempts SET state=$2,result=$3,finished_at=clock_timestamp() WHERE run_id=$1 AND state IN ('queued','running')`), runID, state, interruptedResult(state))
	return problem.Database(err)
}
