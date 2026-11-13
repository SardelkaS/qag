package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/jackc/pgx/v5"
)

func diagnosticID(id string) any {
	if id == "" {
		return nil
	}
	return id
}
func (r *Repository) BeginLog(ctx context.Context, l execution.CallLog) error {
	s := l.Summary
	_, err := r.pool.Exec(ctx, dbscope.Render(r.pool, `INSERT INTO qag.call_logs(id,attempt_id,probe_id,run_id,test_case_id,environment_id,resource_id,protocol,origin,stage,state,started_at,summary,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`), s.ID, diagnosticID(s.AttemptID), diagnosticID(s.ProbeID), diagnosticID(s.RunID), diagnosticID(s.TestCaseID), s.EnvironmentID, diagnosticID(s.ResourceID), s.Protocol, s.Origin, s.Stage, s.State, s.StartedAt, s, l)
	return problem.Database(err)
}
func (r *Repository) CompleteLog(ctx context.Context, l execution.CallLog) error {
	tag, err := r.pool.Exec(ctx, dbscope.Render(r.pool, `UPDATE qag.call_logs SET state=$2,summary=$3,payload=$4 WHERE id=$1 AND state IN ('queued','running')`), l.Summary.ID, l.Summary.State, l.Summary, l)
	if err != nil {
		return problem.Database(err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	old, err := r.GetLog(ctx, l.Summary.ID)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(old, l) {
		return nil
	}
	return problem.Conflict("Вызов уже завершён")
}
func (r *Repository) GetLog(ctx context.Context, id string) (execution.CallLog, error) {
	if strings.HasPrefix(id, "legacy:") {
		a, err := r.GetAttempt(ctx, strings.TrimPrefix(id, "legacy:"))
		if err != nil {
			return execution.CallLog{}, err
		}
		return execution.LegacyLog(a), nil
	}
	if !catalog.ValidID(id) {
		return execution.CallLog{}, problem.Invalid("id", "Неверный ID")
	}
	var l execution.CallLog
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT payload FROM qag.call_logs WHERE id=$1`), id).Scan(&l)
	return l, problem.Database(err)
}

// This legacy projection reads only summary fields from old envelopes, never
// their response bodies. New summaries are stored separately from payloads.
const diagnosticListSQL = `WITH logs AS (
 SELECT id::text,started_at,summary,protocol,origin,stage,state,environment_id::text,coalesce(resource_id::text,'') resource_id,coalesce(test_case_id::text,'') test_case_id,coalesce(run_id::text,'') run_id,coalesce(attempt_id::text,'') attempt_id,coalesce(probe_id::text,'') probe_id FROM qag.call_logs
 UNION ALL
 SELECT 'legacy:'||a.id::text,a.started_at,
 jsonb_build_object('id','legacy:'||a.id::text,'attemptId',a.id::text,'runId',coalesce(a.run_id::text,''),'testCaseId',a.test_case_id::text,'blockId',a.block_id::text,'variantId',coalesce(a.variant_id::text,''),'environmentId',a.safe_request->'resource'->>'environmentId','environmentName',a.safe_request->>'environmentName','resourceId',a.safe_request->'resource'->>'id','resourceName',a.safe_request->'resource'->>'name','resourceVersionId',a.safe_request->'resource'->>'versionId','protocol',a.safe_request->'block'->>'kind','origin',CASE WHEN a.variant_id IS NOT NULL THEN 'variant' WHEN a.run_id IS NOT NULL THEN 'run' ELSE 'editor' END,'stage','main','position',1,'state',a.state,'startedAt',a.started_at,'finishedAt',a.finished_at,'durationMs',a.duration_ms,'legacy',true,'requestSent',false,'redacted',false,'persistencePending',false),
 a.safe_request->'block'->>'kind',CASE WHEN a.variant_id IS NOT NULL THEN 'variant' WHEN a.run_id IS NOT NULL THEN 'run' ELSE 'editor' END,'main',a.state,a.safe_request->'resource'->>'environmentId',coalesce(a.safe_request->'resource'->>'id',''),a.test_case_id::text,coalesce(a.run_id::text,''),a.id::text,''
 FROM qag.call_attempts a WHERE NOT EXISTS(SELECT 1 FROM qag.call_logs l WHERE l.attempt_id=a.id)
) SELECT summary FROM logs WHERE true`

func (r *Repository) ListLogs(ctx context.Context, f execution.LogFilter) (catalog.Page[execution.LogSummary], error) {
	page := catalog.Page[execution.LogSummary]{Items: []execution.LogSummary{}}
	cursor, err := f.Validate()
	if err != nil {
		return page, err
	}
	query := diagnosticListSQL
	args := []any{}
	where := func(expr string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(" AND "+expr, len(args))
	}
	for _, pair := range [][2]string{{"protocol", f.Protocol}, {"origin", f.Origin}, {"stage", f.Stage}, {"state", f.State}, {"environment_id", f.EnvironmentID}, {"resource_id", f.ResourceID}, {"test_case_id", f.TestCaseID}, {"run_id", f.RunID}, {"attempt_id", f.AttemptID}, {"probe_id", f.ProbeID}} {
		if pair[1] != "" {
			where(pair[0]+"=$%d", pair[1])
		}
	}
	if f.From != "" {
		where("started_at >= $%d::timestamptz", f.From)
	}
	if f.To != "" {
		where("started_at <= $%d::timestamptz", f.To)
	}
	if f.Cursor != "" {
		args = append(args, cursor.StartedAt, cursor.ID)
		query += fmt.Sprintf(" AND (started_at,id)<($%d::timestamptz,$%d::text)", len(args)-1, len(args))
	}
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	args = append(args, limit+1)
	query += fmt.Sprintf(" ORDER BY started_at DESC,id DESC LIMIT $%d", len(args))
	rows, err := r.pool.Query(ctx, dbscope.Render(r.pool, query), args...)
	if err != nil {
		return page, problem.Database(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s execution.LogSummary
		if err = rows.Scan(&s); err != nil {
			return page, problem.Database(err)
		}
		if len(page.Items) == limit {
			page.NextCursor = execution.EncodeLogCursor(page.Items[limit-1])
			break
		}
		page.Items = append(page.Items, s)
	}
	return page, problem.Database(rows.Err())
}
func (r *Repository) BeginProbe(ctx context.Context, p execution.HTTPVariableProbe, key, hash string) (execution.HTTPVariableProbe, bool, error) {
	out := p
	created := false
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		var data json.RawMessage
		err := tx.QueryRow(ctx, dbscope.Render(tx, `INSERT INTO qag.http_variable_probes(id,environment_id,resource_id,idempotency_key,request_hash,state,started_at,finished_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(idempotency_key) DO NOTHING RETURNING payload`), p.ID, p.EnvironmentID, diagnosticID(p.ResourceID), key, hash, p.State, p.StartedAt, p.FinishedAt, p).Scan(&data)
		if err == nil {
			created = true
			return nil
		}
		if err != pgx.ErrNoRows {
			return problem.Database(err)
		}
		var oldHash string
		if err = tx.QueryRow(ctx, dbscope.Render(tx, `SELECT request_hash,payload FROM qag.http_variable_probes WHERE idempotency_key=$1`), key).Scan(&oldHash, &out); err != nil {
			return problem.Database(err)
		}
		if oldHash != hash {
			return problem.Conflict("Ключ проверки использован с другими параметрами")
		}
		return nil
	})
	return out, created, err
}
func (r *Repository) GetProbe(ctx context.Context, id string) (execution.HTTPVariableProbe, error) {
	if !catalog.ValidID(id) {
		return execution.HTTPVariableProbe{}, problem.Invalid("id", "Неверный ID")
	}
	var p execution.HTTPVariableProbe
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT payload FROM qag.http_variable_probes WHERE id=$1`), id).Scan(&p)
	return p, problem.Database(err)
}

func (r *Repository) LookupProbe(ctx context.Context, key, hash string) (execution.HTTPVariableProbe, bool, error) {
	var p execution.HTTPVariableProbe
	var oldHash string
	err := r.pool.QueryRow(ctx, dbscope.Render(r.pool, `SELECT request_hash,payload FROM qag.http_variable_probes WHERE idempotency_key=$1`), key).Scan(&oldHash, &p)
	if err == pgx.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, problem.Database(err)
	}
	if oldHash != hash {
		return p, true, problem.Conflict("Ключ проверки использован с другими параметрами")
	}
	return p, true, nil
}
func (r *Repository) UpdateProbe(ctx context.Context, p execution.HTTPVariableProbe) (execution.HTTPVariableProbe, error) {
	out := p
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		var old execution.HTTPVariableProbe
		if err := tx.QueryRow(ctx, dbscope.Render(tx, `SELECT payload FROM qag.http_variable_probes WHERE id=$1 FOR UPDATE`), p.ID).Scan(&old); err != nil {
			return problem.Database(err)
		}
		if !execution.ActiveState(old.State) {
			out = old
			return nil
		}
		_, err := tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.http_variable_probes SET state=$2,finished_at=$3,payload=$4 WHERE id=$1`), p.ID, p.State, p.FinishedAt, p)
		return problem.Database(err)
	})
	return out, err
}

func (r *Repository) recoverDiagnostics(ctx context.Context) error {
	return r.transaction(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, dbscope.Render(tx, `SELECT payload FROM qag.http_variable_probes WHERE state IN ('queued','running') FOR UPDATE`))
		if err != nil {
			return problem.Database(err)
		}
		probes := []execution.HTTPVariableProbe{}
		for rows.Next() {
			var p execution.HTTPVariableProbe
			if err = rows.Scan(&p); err != nil {
				rows.Close()
				return problem.Database(err)
			}
			probes = append(probes, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return problem.Database(err)
		}
		for _, p := range probes {
			t := time.Now().UTC()
			p.State = "interrupted"
			p.FinishedAt = &t
			p.ErrorCode = "interrupted"
			p.Error = "Проверка прервана; запрос мог повлиять на внешний сервис"
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.http_variable_probes SET state=$2,finished_at=$3,payload=$4 WHERE id=$1`), p.ID, p.State, p.FinishedAt, p); err != nil {
				return problem.Database(err)
			}
		}
		rows, err = tx.Query(ctx, dbscope.Render(tx, `SELECT payload FROM qag.call_logs WHERE state IN ('queued','running') FOR UPDATE`))
		if err != nil {
			return problem.Database(err)
		}
		logs := []execution.CallLog{}
		for rows.Next() {
			var l execution.CallLog
			if err = rows.Scan(&l); err != nil {
				rows.Close()
				return problem.Database(err)
			}
			logs = append(logs, l)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return problem.Database(err)
		}
		for _, l := range logs {
			t := time.Now().UTC()
			l.Summary.State = "interrupted"
			l.Summary.FinishedAt = &t
			l.IncompleteReason = "Выполнение прервано; внешний эффект неизвестен"
			if _, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.call_logs SET state=$2,summary=$3,payload=$4 WHERE id=$1`), l.Summary.ID, l.Summary.State, l.Summary, l); err != nil {
				return problem.Database(err)
			}
		}
		return nil
	})
}
