package runs

import "github.com/SardelkaS/qag/internal/dbscope"

import (
	"context"
	"errors"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool} }

type db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func (r *Repository) transaction(ctx context.Context, apply func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return problem.Database(err)
	}
	defer rollback(tx)
	if err = apply(tx); err != nil {
		return err
	}
	return problem.Database(tx.Commit(ctx))
}
func validID(id string) error {
	if !catalog.ValidID(id) {
		return problem.Invalid("id", "Некорректный идентификатор")
	}
	return nil
}

func (r *Repository) LookupRun(ctx context.Context, key, hash string) (Run, bool, error) {
	var id, stored string
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT id::text,request_hash FROM qag.runs WHERE idempotency_key=$1`), key).Scan(&id, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, problem.Database(err)
	}
	if hash != stored {
		return Run{}, true, problem.Conflict("Этот ключ команды уже использован с другими параметрами")
	}
	run, err := r.Get(ctx, id)
	return run, true, err
}

func (r *Repository) Start(ctx context.Context, snapshot catalog.TestCase, environment resources.Snapshot, key, hash string, executeAll ...bool) (Run, error) {
	return r.StartWithOptions(ctx, snapshot, environment, key, hash, StartOptions{ExecuteAll: len(executeAll) > 0 && executeAll[0]})
}
func (r *Repository) StartWithOptions(ctx context.Context, snapshot catalog.TestCase, environment resources.Snapshot, key, hash string, options StartOptions) (Run, error) {
	if !ValidHistoryPolicy(options.HistoryPolicy) {
		return Run{}, problem.Invalid("historyPolicy", "Выберите auto, retain или unretained")
	}
	options.HistoryPolicy = NormalizeHistoryPolicy(options.HistoryPolicy)
	var id string
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, dbscope.Render(tx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`), key); err != nil {
			return problem.Database(err)
		}
		var stored string
		err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text,request_hash FROM qag.runs WHERE idempotency_key=$1`), key).Scan(&id, &stored)
		if err == nil {
			if stored != hash {
				return problem.Conflict("Ключ команды использован иначе")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return problem.Database(err)
		}
		var revision int64
		var archived, parentArchived bool
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT c.revision,c.archived_at IS NOT NULL,f.archived_at IS NOT NULL FROM qag.test_cases c JOIN qag.folders f ON f.id=c.folder_id WHERE c.id=$1 FOR UPDATE OF f,c`), snapshot.ID).Scan(&revision, &archived, &parentArchived); err != nil {
			return problem.Database(err)
		}
		if archived || parentArchived {
			return problem.Archived()
		}
		if revision != snapshot.Revision {
			return problem.Conflict("Сценарий изменился перед началом прогона")
		}
		err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT id::text FROM qag.runs WHERE test_case_id=$1 AND status='running'`), snapshot.ID).Scan(&id)
		if err == nil {
			existing, _, e := getRun(ctx, tx, id, false)
			if e != nil {
				return e
			}
			if existing.HistoryPolicy != options.HistoryPolicy {
				return problem.Conflict("Активный прогон использует другую политику закрепления")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return problem.Database(err)
		}
		id = catalog.NewID()
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.runs(id,test_case_id,environment_version_id,snapshot,context,idempotency_key,request_hash,history_policy) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`), id, snapshot.ID, environment.EnvironmentVersionID, snapshot, environment, key, hash, options.HistoryPolicy); err != nil {
			return problem.Database(err)
		}
		for _, step := range snapshot.Steps {
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_steps(run_id,step_id) VALUES($1,$2)`), id, step.ID); err != nil {
				return problem.Database(err)
			}
		}
		for name, version := range environment.ResourceVersions {
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_resource_versions(run_id,resource_name,resource_version_id) VALUES($1,$2,$3)`), id, name, version); err != nil {
				return problem.Database(err)
			}
		}
		if options.ExecuteAll {
			_, err = insertBatch(ctx, tx, id, key, hash, snapshot)
			return err
		}
		return nil
	})
	if err != nil {
		return Run{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) Get(ctx context.Context, id string) (Run, error) {
	run, _, err := getRun(ctx, r.pool, id, false)
	return run, err
}
func (r *Repository) Context(ctx context.Context, id string) (resources.Snapshot, error) {
	if err := validID(id); err != nil {
		return resources.Snapshot{}, err
	}
	var snapshot resources.Snapshot
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT context FROM qag.runs WHERE id=$1`), id).Scan(&snapshot)
	return snapshot, problem.Database(err)
}

func getRun(ctx context.Context, d db, id string, lock bool) (Run, resources.Snapshot, error) {
	var run Run
	var environment resources.Snapshot
	if err := validID(id); err != nil {
		return run, environment, err
	}
	sql := `SELECT id::text,test_case_id::text,status,snapshot,context,retention_reasons,started_at,finished_at,history_policy FROM qag.runs WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	err := d.QueryRow(ctx, dbscope.Render(d, sql), id).Scan(&run.ID, &run.TestCaseID, &run.Status, &run.Snapshot, &environment, &run.RetentionReasons, &run.StartedAt, &run.FinishedAt, &run.HistoryPolicy)
	if err != nil {
		return run, environment, problem.Database(err)
	}
	run.EnvironmentID = environment.EnvironmentID
	run.EnvironmentName = environment.EnvironmentName
	run.Resources = environment.Resources
	if run.Resources == nil {
		run.Resources = []resources.Resource{}
	}
	if run.RetentionReasons == nil {
		run.RetentionReasons = []string{}
	}
	rows, err := d.Query(ctx, dbscope.Render(d, `SELECT step_id::text,status,comment,revision FROM qag.run_steps WHERE run_id=$1`), id)
	if err != nil {
		return run, environment, problem.Database(err)
	}
	states := map[string]StepState{}
	for rows.Next() {
		var state StepState
		if err = rows.Scan(&state.StepID, &state.Status, &state.Comment, &state.Revision); err != nil {
			rows.Close()
			return run, environment, problem.Database(err)
		}
		states[state.StepID] = state
		if state.Status == "blocked" || state.Status == "skipped" {
			state.LegacyStatus = state.Status
			state.Status = "not_run"
			states[state.StepID] = state
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return run, environment, problem.Database(rows.Err())
	}
	run.StepStates = []StepState{}
	for _, step := range run.Snapshot.Steps {
		run.StepStates = append(run.StepStates, states[step.ID])
	}
	run.Attempts, err = loadAttempts(ctx, d, id)
	if err == nil {
		run.Variants, err = loadVariants(ctx, d, id)
	}
	if err == nil {
		run.Batches, err = loadBatches(ctx, d, id)
	}
	return run, environment, err
}

func (r *Repository) List(ctx context.Context, caseID string, retained *bool, limit int, cursor string, active ...bool) (catalog.Page[Run], error) {
	if err := validID(caseID); err != nil {
		return catalog.Page[Run]{}, err
	}
	var exists bool
	if err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT EXISTS(SELECT 1 FROM qag.test_cases WHERE id=$1)`), caseID).Scan(&exists); err != nil {
		return catalog.Page[Run]{}, problem.Database(err)
	}
	if !exists {
		return catalog.Page[Run]{}, problem.NotFound()
	}
	running := len(active) > 0 && active[0]
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, `SELECT id::text FROM qag.runs WHERE test_case_id=$1 AND ((status='running')=$3) AND ($2::boolean IS NULL OR (cardinality(retention_reasons)>0)=$2) ORDER BY coalesce(finished_at,started_at) DESC,id DESC`), caseID, retained, running)
	if err != nil {
		return catalog.Page[Run]{}, problem.Database(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return catalog.Page[Run]{}, problem.Database(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if rows.Err() != nil {
		return catalog.Page[Run]{}, problem.Database(rows.Err())
	}
	page, err := catalog.Paginate(ids, limit, cursor, func(id string) string { return id })
	if err != nil {
		return catalog.Page[Run]{}, err
	}
	result := catalog.Page[Run]{Items: []Run{}, NextCursor: page.NextCursor}
	for _, id := range page.Items {
		run, err := r.Get(ctx, id)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, run)
	}
	return result, nil
}

func (r *Repository) UpdateStep(ctx context.Context, id, stepID, status, comment string, revision int64) (Run, error) {
	allowed := map[string]bool{"not_run": true, "passed": true, "failed": true}
	if !allowed[status] {
		return Run{}, problem.Invalid("status", "Выберите ручной статус шага")
	}
	if err := validID(stepID); err != nil {
		return Run{}, err
	}
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return problem.Conflict("Завершённый прогон доступен только для просмотра")
		}
		var previous int64
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT revision FROM qag.run_steps WHERE run_id=$1 AND step_id=$2`), id, stepID).Scan(&previous); err != nil {
			return problem.Database(err)
		}
		if previous != revision {
			return problem.Conflict("Шаг изменён в другой вкладке; обновите прогон")
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_steps SET status=$3,comment=$4,revision=revision+1 WHERE run_id=$1 AND step_id=$2`), id, stepID, status, comment)
		return problem.Database(err)
	})
	if err != nil {
		return Run{}, err
	}
	return r.Get(ctx, id)
}

func (r *Repository) Finish(ctx context.Context, id string) (Run, error) {
	return r.finish(ctx, id, "")
}
func (r *Repository) Cancel(ctx context.Context, id string) (Run, error) {
	return r.finish(ctx, id, "cancelled")
}
func (r *Repository) finish(ctx context.Context, id, forced string) (Run, error) {
	if err := validID(id); err != nil {
		return Run{}, err
	}
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		var caseID string
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT test_case_id::text FROM qag.runs WHERE id=$1`), id).Scan(&caseID); err != nil {
			return problem.Database(err)
		}
		if _, err := tx.Exec(ctx, dbscope.Render(tx, `SELECT id FROM qag.test_cases WHERE id=$1 FOR UPDATE`), caseID); err != nil {
			return problem.Database(err)
		}
		run, _, err := getRun(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return nil
		}
		if forced != "" {
			if err = cancelBatches(ctx, tx, id, forced); err != nil {
				return err
			}
			if err = cancelAttempts(ctx, tx, id, forced); err != nil {
				return err
			}
		} else {
			for _, batch := range run.Batches {
				if batch.State == "running" {
					return problem.Conflict("Дождитесь очереди или отмените прогон")
				}
			}
			for _, attempt := range run.Attempts {
				if attempt.State == "running" || attempt.State == "queued" {
					return problem.Conflict("Дождитесь вызова или отмените прогон")
				}
			}
		}
		status := forced
		if status == "" {
			status = Evaluate(run)
		}
		var first, success bool
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT EXISTS(SELECT 1 FROM qag.runs WHERE test_case_id=$1 AND 'first_run'=ANY(retention_reasons)),EXISTS(SELECT 1 FROM qag.runs WHERE test_case_id=$1 AND 'first_success'=ANY(retention_reasons))`), caseID).Scan(&first, &success); err != nil {
			return problem.Database(err)
		}
		reasons := PolicyReasons(run.HistoryPolicy, status, first, success)
		_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.runs SET status=$2,retention_reasons=$3,finished_at=clock_timestamp() WHERE id=$1`), id, status, reasons)
		return problem.Database(err)
	})
	if err != nil {
		return Run{}, err
	}
	return r.Get(ctx, id)
}

func Evaluate(run Run) string {
	for _, batch := range run.Batches {
		if batch.State == "running" {
			return "blocked"
		}
	}
	for _, attempt := range run.Attempts {
		if attempt.State == "queued" || attempt.State == "running" {
			return "blocked"
		}
	}
	failed, blocked := false, false
	for _, step := range run.StepStates {
		if step.Status == "failed" {
			failed = true
		} else if step.Status != "passed" {
			blocked = true
		}
	}
	for _, step := range run.Snapshot.Steps {
		for _, block := range step.Blocks {
			var latest int
			state, comparison := "", ""
			for _, attempt := range run.Attempts {
				if attempt.VariantID == "" && attempt.BlockID == block.ID && attempt.Number >= latest {
					latest = attempt.Number
					state = attempt.State
					comparison = ""
					if attempt.Result != nil {
						comparison = attempt.Result.Comparison.Status
					}
				}
			}
			if comparison == "mismatch" {
				failed = true
			} else if state != "completed" || comparison != "match" {
				blocked = true
			}
		}
	}
	if failed {
		return "failed"
	}
	if blocked {
		return "blocked"
	}
	return "passed"
}

func (r *Repository) Save(ctx context.Context, id string) (Run, error) {
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if run.Status == "running" {
			return problem.Conflict("Сначала завершите прогон")
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.runs SET retention_reasons=CASE WHEN 'manual'=ANY(retention_reasons) THEN retention_reasons ELSE array_append(retention_reasons,'manual') END WHERE id=$1`), id)
		return problem.Database(err)
	})
	if err != nil {
		return Run{}, err
	}
	return r.Get(ctx, id)
}
func (r *Repository) Delete(ctx context.Context, id string) error {
	return r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if run.Status == "running" || len(run.RetentionReasons) > 0 {
			return problem.Conflict("Сохранённый или текущий прогон удалять нельзя")
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `DELETE FROM qag.runs WHERE id=$1`), id)
		return problem.Database(err)
	})
}

func (r *Repository) Recover(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, `SELECT id::text FROM qag.runs WHERE status='running' ORDER BY started_at,id`))
	if err != nil {
		return problem.Database(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return problem.Database(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if rows.Err() != nil {
		return problem.Database(rows.Err())
	}
	for _, id := range ids {
		if _, err = r.finish(ctx, id, "interrupted"); err != nil {
			return err
		}
	}
	_, err = r.pool.Exec(ctx, dbscope.Render(r.pool, `UPDATE qag.call_attempts SET state='interrupted',finished_at=clock_timestamp(),result=$1 WHERE run_id IS NULL AND state IN ('queued','running')`), interruptedResult("interrupted"))
	return problem.Database(err)
}

// RetentionReasons chooses automatic pins without changing existing history.
func RetentionReasons(status string, first, success bool) []string {
	reasons := []string{}
	if !first {
		reasons = append(reasons, "first_run")
	}
	if status == "passed" && !success {
		reasons = append(reasons, "first_success")
	}
	return reasons
}
