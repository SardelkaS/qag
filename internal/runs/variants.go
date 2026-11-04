package runs

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/dbscope"
	"github.com/SardelkaS/qag/internal/problem"
)

func loadVariants(ctx context.Context, d db, runID string) ([]Variant, error) {
	rows, err := d.Query(ctx, dbscope.Render(d, `SELECT id::text,run_id::text,block_id::text,name,position,revision,current_version_id::text FROM qag.run_call_variants WHERE run_id=$1 ORDER BY block_id,position`), runID)
	if err != nil {
		return nil, problem.Database(err)
	}
	result := []Variant{}
	for rows.Next() {
		var v Variant
		if err = rows.Scan(&v.ID, &v.RunID, &v.BlockID, &v.Name, &v.Position, &v.Revision, &v.CurrentVersionID); err != nil {
			rows.Close()
			return nil, problem.Database(err)
		}
		v.Versions = []VariantVersion{}
		result = append(result, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, problem.Database(err)
	}
	for i := range result {
		versions, e := d.Query(ctx, dbscope.Render(d, `SELECT id::text,revision,request,created_at FROM qag.run_call_variant_versions WHERE variant_id=$1 ORDER BY revision`), result[i].ID)
		if e != nil {
			return nil, problem.Database(e)
		}
		for versions.Next() {
			var v VariantVersion
			if e = versions.Scan(&v.ID, &v.Revision, &v.Request, &v.CreatedAt); e != nil {
				versions.Close()
				return nil, problem.Database(e)
			}
			v.Request.Expected = nil
			result[i].Versions = append(result[i].Versions, v)
		}
		e = versions.Err()
		versions.Close()
		if e != nil {
			return nil, problem.Database(e)
		}
	}
	return result, nil
}
func ParentBlock(run Run, id string) (catalog.Block, error) {
	for _, s := range run.Snapshot.Steps {
		for _, b := range s.Blocks {
			if b.ID == id {
				return b, nil
			}
		}
	}
	return catalog.Block{}, problem.NotFound()
}
func (r *Repository) SaveVariant(ctx context.Context, runID, blockID, id, name string, revision int64, request *catalog.CallRequest) (Variant, error) {
	if err := validID(runID); err != nil {
		return Variant{}, err
	}
	if id != "" {
		if err := validID(id); err != nil {
			return Variant{}, err
		}
	}
	err := r.transaction(ctx, func(tx pgx.Tx) error {
		run, _, err := getRun(ctx, tx, runID, true)
		if err != nil {
			return err
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
			if a.State == "queued" || a.State == "running" {
				return problem.Conflict("Дождитесь выполняющегося вызова")
			}
		}
		position := 1
		if id != "" {
			found := false
			for _, v := range run.Variants {
				if v.ID == id {
					found = true
					blockID = v.BlockID
					position = v.Position
					if v.Revision != revision {
						return problem.Conflict("Вариант изменён в другой вкладке; введённый текст сохранён в форме")
					}
				}
			}
			if !found {
				return problem.NotFound()
			}
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
		parent, err := ParentBlock(run, blockID)
		if err != nil {
			return err
		}
		payload := parent
		payload.Expected = catalog.Expectation{}
		if request != nil {
			payload.HTTP = request.HTTP
			payload.GRPC = request.GRPC
			payload.SQL = request.SQL
			payload.TimeoutSeconds = request.TimeoutSeconds
		}
		if err = catalog.ValidateDraft([]catalog.Step{{ID: catalog.NewID(), Blocks: []catalog.Block{payload}}}); err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = fmt.Sprintf("Вариант %d", position)
		}
		if len([]rune(name)) > 200 {
			return problem.Invalid("name", "Не больше 200 символов")
		}
		versionID := catalog.NewID()
		if revision == 0 {
			_, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_call_variants(id,run_id,block_id,name,position,revision,current_version_id) VALUES($1,$2,$3,$4,$5,1,$6)`), id, runID, blockID, name, position, versionID)
		} else {
			_, err = tx.Exec(ctx, dbscope.Render(tx, `UPDATE qag.run_call_variants SET name=$2,revision=revision+1,current_version_id=$3 WHERE id=$1`), id, name, versionID)
		}
		if err != nil {
			return problem.Database(err)
		}
		_, err = tx.Exec(ctx, dbscope.Render(tx, `INSERT INTO qag.run_call_variant_versions(id,variant_id,run_id,block_id,revision,request) VALUES($1,$2,$3,$4,$5,$6)`), versionID, id, runID, blockID, revision+1, catalog.RequestFromBlock(payload, false))
		return problem.Database(err)
	})
	if err != nil {
		return Variant{}, err
	}
	variants, err := loadVariants(ctx, r.pool, runID)
	if err != nil {
		return Variant{}, err
	}
	for _, v := range variants {
		if v.ID == id {
			return v, nil
		}
	}
	return Variant{}, problem.NotFound()
}
