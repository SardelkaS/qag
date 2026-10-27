package runs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"qag/internal/catalog"
	"qag/internal/comparison"
	"qag/internal/dbscope"
	"qag/internal/problem"
)

func reportRequest(b catalog.Block) ReportRequest {
	// Copy before replacing header source configuration by a public description.
	raw, _ := json.Marshal(b)
	var copy catalog.Block
	_ = json.Unmarshal(raw, &copy)
	publicHeaders := func(hs []catalog.RequestHeader) []catalog.RequestHeader {
		for i := range hs {
			h := &hs[i]
			if h.Source != nil {
				h.Value = fmt.Sprintf("[источник: %s %s; JSON %s; %s]", h.Source.Method, h.Source.URL, h.Source.JSONPointer, h.Source.FetchMode)
				h.Source = nil
				h.ID = ""
			}
		}
		return hs
	}
	if copy.HTTP != nil {
		copy.HTTP.Headers = publicHeaders(copy.HTTP.Headers)
	}
	if copy.GRPC != nil {
		copy.GRPC.Metadata = publicHeaders(copy.GRPC.Metadata)
	}
	return ReportRequest{TimeoutSeconds: copy.TimeoutSeconds, HTTP: copy.HTTP, GRPC: copy.GRPC, SQL: copy.SQL}
}
func (r *Repository) ReportSnapshot(ctx context.Context, id string, includeExpected bool) (Report, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, problem.Database(err)
	}
	defer rollback(tx)
	run, _, err := getRun(ctx, tx, id, false)
	if err != nil {
		return Report{}, err
	}
	report := Report{SnapshotID: catalog.NewID(), RunID: run.ID, Title: run.Snapshot.Name, EnvironmentName: run.EnvironmentName, Status: run.Status, Partial: run.Status == "running", IncludeExpected: includeExpected, CreatedAt: time.Now().UTC(), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Steps: []ReportStep{}, Batches: run.Batches, Images: []ReportImage{}}
	refs := map[string]bool{}
	addRefs := func(images []catalog.ImageRef) {
		for _, image := range images {
			refs[image.ImageID] = true
		}
	}
	for _, step := range run.Snapshot.Steps {
		s := ReportStep{ID: step.ID, Title: step.Title, Description: step.Description, Calls: []ReportCall{}}
		for _, state := range run.StepStates {
			if state.StepID == step.ID {
				s.Status = state.Status
				s.LegacyStatus = state.LegacyStatus
				s.Comment = state.Comment
			}
		}
		if includeExpected {
			s.Expected = step.Expected
			if s.Expected != nil {
				addRefs(s.Expected.Images)
			}
		}
		for _, block := range step.Blocks {
			c := ReportCall{ID: block.ID, Kind: block.Kind, Resource: block.Resource, Request: reportRequest(block), Attempts: []ReportAttempt{}, Variants: []ReportVariant{}}
			if includeExpected {
				expected := block.Expected
				c.Expected = &expected
				addRefs(expected.Images)
			}
			for _, v := range run.Variants {
				if v.BlockID != block.ID {
					continue
				}
				variant := ReportVariant{ID: v.ID, Name: v.Name, Position: v.Position, Revision: v.Revision, Versions: []ReportVariantVersion{}, Attempts: []ReportAttempt{}}
				for _, version := range v.Versions {
					variant.Versions = append(variant.Versions, ReportVariantVersion{ID: version.ID, Revision: version.Revision, Request: reportRequest(version.Request), CreatedAt: version.CreatedAt})
				}
				c.Variants = append(c.Variants, variant)
			}
			for _, a := range run.Attempts {
				if a.BlockID != block.ID {
					continue
				}
				attempt := ReportAttempt{ID: a.ID, Number: a.Number, VariantVersionID: a.VariantVersionID, State: a.State, StartedAt: a.StartedAt, FinishedAt: a.FinishedAt, DurationMS: a.DurationMS, Request: reportRequest(a.Request)}
				if includeExpected {
					expected := a.Request.Expected
					attempt.Expected = &expected
					addRefs(expected.Images)
				}
				if a.Result != nil && a.FinishedAt != nil {
					raw, _ := json.Marshal(a.Result)
					_ = json.Unmarshal(raw, &attempt.Result)
					if !includeExpected {
						attempt.Result.Comparison.Differences = []comparison.Difference{}
						attempt.Result.Comparison.Reason = ""
						attempt.Result.Comparison.DifferencesTruncated = false
					}
				}
				if a.VariantID == "" {
					c.Attempts = append(c.Attempts, attempt)
				} else {
					for i := range c.Variants {
						if c.Variants[i].ID == a.VariantID {
							c.Variants[i].Attempts = append(c.Variants[i].Attempts, attempt)
						}
					}
				}
			}
			s.Calls = append(s.Calls, c)
		}
		report.Steps = append(report.Steps, s)
	}
	total := 0
	// Stable order comes from the stored image ID, independent of map iteration.
	rows, err := tx.Query(ctx, dbscope.Render(tx, `SELECT id::text,content_type,sha256,width,height,data FROM qag.expected_images WHERE test_case_id=$1 AND id=ANY($2::uuid[]) ORDER BY id`), run.TestCaseID, func() []string {
		ids := []string{}
		for id := range refs {
			ids = append(ids, id)
		}
		return ids
	}())
	if err != nil {
		return Report{}, problem.Database(err)
	}
	for rows.Next() {
		var image ReportImage
		var data []byte
		if err = rows.Scan(&image.ID, &image.MIME, &image.SHA256, &image.Width, &image.Height, &data); err != nil {
			rows.Close()
			return Report{}, problem.Database(err)
		}
		total += len(data)
		if total > 48<<20 {
			rows.Close()
			return Report{}, problem.Invalid("report", "Эталоны отчёта превышают 48 MiB")
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != image.SHA256 {
			rows.Close()
			return Report{}, problem.Invalid("report", "Эталон повреждён")
		}
		image.Data = base64.StdEncoding.EncodeToString(data)
		report.Images = append(report.Images, image)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Report{}, problem.Database(err)
	}
	if len(report.Images) != len(refs) {
		return Report{}, problem.Invalid("report", "Не удалось прочитать все эталоны отчёта")
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, problem.Database(err)
	}
	return report, nil
}
