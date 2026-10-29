package runs

import (
	"context"
	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/dbscope"
	"qag/internal/problem"
)

type StepRevision struct {
	StepID   string `json:"stepId"`
	Revision int64  `json:"revision"`
}

// PassAll validates the whole snapshot before writing and returns revisions
// while the run is still locked. Comments and execution results stay intact.
func (r *Repository) PassAll(ctx context.Context, id string, steps []StepRevision) (Run, error) {
	var result Run
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if run.Status != "running" {
			return problem.Conflict("Завершённый прогон доступен только для просмотра")
		}
		if len(steps) != len(run.StepStates) {
			return problem.Invalid("steps", "Нужны версии всех шагов прогона")
		}
		versions := make(map[string]int64, len(steps))
		for _, s := range steps {
			if !catalog.ValidID(s.StepID) || s.Revision < 1 {
				return problem.Invalid("steps", "Некорректная версия шага")
			}
			if _, ok := versions[s.StepID]; ok {
				return problem.Invalid("steps", "Шаги не должны повторяться")
			}
			versions[s.StepID] = s.Revision
		}
		for _, s := range run.StepStates {
			version, ok := versions[s.StepID]
			if !ok {
				return problem.Invalid("steps", "Шаг не относится к этому снимку")
			}
			if version != s.Revision {
				return problem.Conflict("Шаг изменён в другой вкладке; обновите прогон")
			}
		}
		if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_steps SET status='passed',revision=revision+1 WHERE run_id=$1 AND status<>'passed'`), id); err != nil {
			return problem.Database(err)
		}
		result, _, err = getRun(ctx, tx, id, false)
		return err
	})
	return result, err
}
