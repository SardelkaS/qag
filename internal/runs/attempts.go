package runs

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/comparison"
	"qag/internal/execution"
	"qag/internal/problem"
	"qag/internal/resources"
)

type attemptEnvelope struct {
	Block           catalog.Block      `json:"block"`
	Resource        resources.Resource `json:"resource"`
	EnvironmentName string             `json:"environmentName"`
}

const attemptSelect = `SELECT id::text,run_id::text,test_case_id::text,block_id::text,case_revision,attempt_number,state,safe_request,result,started_at,finished_at,duration_ms FROM qag.call_attempts`

type scanner interface{ Scan(...any) error }

func scanAttempt(row scanner) (execution.Attempt, error) {
	var attempt execution.Attempt
	var runID *string
	var envelope attemptEnvelope
	err := row.Scan(&attempt.ID, &runID, &attempt.TestCaseID, &attempt.BlockID, &attempt.Revision, &attempt.Number, &attempt.State, &envelope, &attempt.Result, &attempt.StartedAt, &attempt.FinishedAt, &attempt.DurationMS)
	if runID != nil {
		attempt.RunID = *runID
	}
	attempt.Request = envelope.Block
	attempt.Resource = envelope.Resource
	attempt.EnvironmentName = envelope.EnvironmentName
	attempt.EnvironmentID = envelope.Resource.EnvironmentID
	return attempt, problem.Database(err)
}
func getAttempt(ctx context.Context, d db, id string) (execution.Attempt, error) {
	if err := validID(id); err != nil {
		return execution.Attempt{}, err
	}
	return scanAttempt(d.QueryRow(ctx, attemptSelect+` WHERE id=$1`, id))
}
func (r *Repository) GetAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	return getAttempt(ctx, r.pool, id)
}
func loadAttempts(ctx context.Context, d db, id string) ([]execution.Attempt, error) {
	rows, err := d.Query(ctx, attemptSelect+` WHERE run_id=$1 ORDER BY started_at,attempt_number,id`, id)
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
	err := r.pool.QueryRow(ctx, `SELECT id::text,request_hash FROM qag.call_attempts WHERE idempotency_key=$1`, key).Scan(&id, &stored)
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

func (r *Repository) BeginAttempt(ctx context.Context, candidateID, caseID, runID string, revision int64, block catalog.Block, snapshot resources.Snapshot, safeBlock catalog.Block, key, hash string) (execution.Attempt, bool, error) {
	var attempt execution.Attempt
	created := false
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return problem.Database(err)
		}
		var id, stored string
		err := tx.QueryRow(ctx, `SELECT id::text,request_hash FROM qag.call_attempts WHERE idempotency_key=$1`, key).Scan(&id, &stored)
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
			var status string
			if err = tx.QueryRow(ctx, `SELECT status FROM qag.runs WHERE id=$1 AND test_case_id=$2 FOR UPDATE`, runID, caseID).Scan(&status); err != nil {
				return problem.Database(err)
			}
			if status != "running" {
				return problem.Conflict("Завершённый прогон доступен только для просмотра")
			}
			var busy bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM qag.call_attempts WHERE run_id=$1 AND state IN ('queued','running'))`, runID).Scan(&busy); err != nil {
				return problem.Database(err)
			}
			if busy {
				return problem.Conflict("В прогоне уже выполняется блок; дождитесь ответа или отмените его")
			}
		} else {
			var current int64
			var archived, parent bool
			if err = tx.QueryRow(ctx, `SELECT c.revision,c.archived_at IS NOT NULL,f.archived_at IS NOT NULL FROM qag.test_cases c JOIN qag.folders f ON f.id=c.folder_id WHERE c.id=$1 FOR UPDATE OF f,c`, caseID).Scan(&current, &archived, &parent); err != nil {
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
		if err = tx.QueryRow(ctx, `SELECT coalesce(max(attempt_number),0)+1 FROM qag.call_attempts WHERE test_case_id=$1 AND block_id=$2 AND run_id IS NOT DISTINCT FROM $3::uuid`, caseID, block.ID, nullableID(runID)).Scan(&number); err != nil {
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
		envelope := attemptEnvelope{Block: safeBlock, Resource: resource, EnvironmentName: snapshot.EnvironmentName}
		id = candidateID
		_, err = tx.Exec(ctx, `INSERT INTO qag.call_attempts(id,run_id,test_case_id,block_id,case_revision,environment_version_id,resource_version_id,attempt_number,idempotency_key,request_hash,safe_request) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, nullableID(runID), caseID, block.ID, revision, snapshot.EnvironmentVersionID, snapshot.ResourceVersions[block.Resource], number, key, hash, envelope)
		if err != nil {
			return problem.Database(err)
		}
		attempt, err = getAttempt(ctx, tx, id)
		created = err == nil
		return err
	})
	return attempt, created, err
}

func (r *Repository) ClaimAttempt(ctx context.Context, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE qag.call_attempts SET state='running' WHERE id=$1 AND state='queued'`, id)
	return tag.RowsAffected() == 1, problem.Database(err)
}
func (r *Repository) CompleteAttempt(ctx context.Context, id, state string, result execution.Result, duration time.Duration) error {
	if state != "completed" && state != "error" && state != "timed_out" && state != "cancelled" && state != "interrupted" {
		return problem.Invalid("state", "Некорректный итог попытки")
	}
	_, err := r.pool.Exec(ctx, `UPDATE qag.call_attempts SET state=$2,result=$3,duration_ms=$4,finished_at=clock_timestamp() WHERE id=$1 AND state IN ('queued','running')`, id, state, result, max(duration.Milliseconds(), 0))
	return problem.Database(err)
}
func (r *Repository) CancelAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	if err := validID(id); err != nil {
		return execution.Attempt{}, err
	}
	_, err := r.pool.Exec(ctx, `UPDATE qag.call_attempts SET state='cancelled',result=$2,finished_at=clock_timestamp() WHERE id=$1 AND state IN ('queued','running')`, id, interruptedResult("cancelled"))
	if err != nil {
		return execution.Attempt{}, problem.Database(err)
	}
	return r.GetAttempt(ctx, id)
}
func interruptedResult(state string) execution.Result {
	message := "Процесс остановлен; результат внешнего действия неизвестен. Автоматического повтора не было"
	if state == "cancelled" {
		message = "Попытка отменена; внешнее действие могло уже выполниться"
	}
	return execution.Result{Headers: []catalog.KeyValue{}, Error: message, Comparison: comparison.Result{Status: "not_compared", Reason: message, Differences: []comparison.Difference{}}}
}
func cancelAttempts(ctx context.Context, tx pgx.Tx, runID, state string) error {
	_, err := tx.Exec(ctx, `UPDATE qag.call_attempts SET state=$2,result=$3,finished_at=clock_timestamp() WHERE run_id=$1 AND state IN ('queued','running')`, runID, state, interruptedResult(state))
	return problem.Database(err)
}
