package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"qag/internal/catalog"
	"qag/internal/execution"
	"qag/internal/problem"
	"qag/internal/runs"
)

func (e *Engine) fence(runID string) *sync.Mutex {
	v, _ := e.fences.LoadOrStore(runID, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (e *Engine) StartBatch(ctx context.Context, runID, key string) (runs.Batch, error) {
	if err := commandKey(key); err != nil {
		return runs.Batch{}, err
	}
	batch, err := e.Runs.StartBatch(ctx, runID, key, requestHash("batch", runID))
	if err == nil {
		if run, readErr := e.Runs.Get(ctx, runID); readErr == nil {
			e.ensureBatches(run)
		}
	}
	return batch, err
}
func (e *Engine) ensureBatches(run runs.Run) {
	for _, batch := range run.Batches {
		if batch.State != "running" {
			continue
		}
		e.mu.Lock()
		if e.batches[batch.ID] {
			e.mu.Unlock()
			continue
		}
		e.batches[batch.ID] = true
		e.wg.Add(1)
		e.mu.Unlock()
		go e.batchWorker(run.ID, batch.ID)
	}
}
func itemKey(batch string, position int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("qag-batch:%s:%d", batch, position)))
	sum[6] = (sum[6] & 15) | 0x40
	sum[8] = (sum[8] & 63) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
func (e *Engine) batchWorker(runID, batchID string) {
	defer e.wg.Done()
	defer func() { e.mu.Lock(); delete(e.batches, batchID); e.mu.Unlock() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		err := e.Runs.AdvanceBatch(ctx, runID, batchID)
		if err == nil {
			run, readErr := e.Runs.Get(ctx, runID)
			if readErr == nil {
				if run.Status != "running" {
					cancel()
					return
				}
				var batch *runs.Batch
				for i := range run.Batches {
					if run.Batches[i].ID == batchID {
						batch = &run.Batches[i]
						break
					}
				}
				if batch == nil || batch.State != "running" {
					cancel()
					return
				}
				for _, item := range batch.Items {
					if item.State == "completed" {
						continue
					}
					if item.State == "pending" {
						key := itemKey(batchID, item.Position)
						hash := requestHash("batch-item", runID, batchID, item.Position)
						if _, found, lookupErr := e.Runs.LookupAttempt(ctx, key, hash); lookupErr == nil && !found {
							block, findErr := findBlock(run.Snapshot, item.BlockID)
							if findErr == nil {
								snapshot, snapshotErr := e.Runs.Context(ctx, runID)
								if snapshotErr == nil {
									_, _ = e.begin(ctx, run.TestCaseID, runID, run.Snapshot.Revision, block, snapshot, key, hash, runs.AttemptOptions{BatchID: batchID, BatchPosition: item.Position})
								}
							}
						}
					}
					break
				}
			}
		}
		cancel()
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (e *Engine) VariantAttempt(ctx context.Context, runID, variantID string, revision int64, key string) (execution.Attempt, error) {
	if err := commandKey(key); err != nil {
		return execution.Attempt{}, err
	}
	hash := requestHash("variant", runID, variantID, revision)
	if previous, found, err := e.Runs.LookupAttempt(ctx, key, hash); err != nil || found {
		return previous, err
	}
	run, err := e.GetRun(ctx, runID)
	if err != nil {
		return execution.Attempt{}, err
	}
	if run.Status != "running" {
		return execution.Attempt{}, problem.Conflict("Прогон завершён")
	}
	for _, variant := range run.Variants {
		if variant.ID != variantID {
			continue
		}
		if variant.Revision != revision {
			return execution.Attempt{}, problem.Conflict("Сохраните актуальную версию варианта")
		}
		for _, version := range variant.Versions {
			if version.ID != variant.CurrentVersionID {
				continue
			}
			block := version.Request
			if err = catalog.ValidateBlock(&block, "request"); err != nil {
				return execution.Attempt{}, err
			}
			snapshot, err := e.Runs.Context(ctx, runID)
			if err != nil {
				return execution.Attempt{}, err
			}
			return e.begin(ctx, run.TestCaseID, run.ID, run.Snapshot.Revision, block, snapshot, key, hash, runs.AttemptOptions{VariantID: variant.ID, VariantVersionID: version.ID})
		}
	}
	return execution.Attempt{}, problem.NotFound()
}
