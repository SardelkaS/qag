package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"qag/internal/catalog"
	"qag/internal/comparison"
	"qag/internal/execution"
	"qag/internal/problem"
	"qag/internal/resources"
	"qag/internal/runs"
	"sync"
	"time"
)

type job struct {
	cancel             context.CancelFunc
	runID              string
	persistencePending bool
}
type Engine struct {
	Catalog   *catalog.Repository
	Resources *resources.Repository
	Runs      *runs.Repository
	Runner    *execution.Runner
	ctx       context.Context
	stop      context.CancelFunc
	mu        sync.Mutex
	jobs      map[string]job
	batches   map[string]bool
	fences    sync.Map
	slots     chan struct{}
	wg        sync.WaitGroup
}

func New(ctx context.Context, c *catalog.Repository, res *resources.Repository, r *runs.Repository) *Engine {
	root, stop := context.WithCancel(ctx)
	return &Engine{Catalog: c, Resources: res, Runs: r, Runner: execution.NewRunner(), ctx: root, stop: stop, jobs: map[string]job{}, batches: map[string]bool{}, slots: make(chan struct{}, 4)}
}
func (e *Engine) Close() { e.stop(); e.wg.Wait(); e.Runner.ClearHeaderScopes() }
func requestHash(values ...any) string {
	data, _ := json.Marshal(values)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func commandKey(key string) error {
	if !catalog.ValidID(key) {
		return problem.Invalid("Idempotency-Key", "Нужен уникальный UUID команды")
	}
	return nil
}
func findBlock(c catalog.TestCase, id string) (catalog.Block, error) {
	for _, step := range c.Steps {
		for _, block := range step.Blocks {
			if block.ID == id {
				return block, nil
			}
		}
	}
	return catalog.Block{}, problem.NotFound()
}
func needs(c catalog.TestCase) (map[string]string, error) {
	result := map[string]string{}
	for _, step := range c.Steps {
		for _, b := range step.Blocks {
			if previous, ok := result[b.Resource]; ok && previous != b.Kind {
				return nil, problem.Invalid("resource", "Создайте разные ресурсы для разных протоколов")
			}
			result[b.Resource] = b.Kind
		}
	}
	return result, nil
}

func (e *Engine) StartRun(ctx context.Context, caseID, environmentID string, revision int64, key string, executeAll ...bool) (runs.Run, error) {
	if err := commandKey(key); err != nil {
		return runs.Run{}, err
	}
	hash := requestHash("run", caseID, environmentID, revision)
	all := len(executeAll) > 0 && executeAll[0]
	if all {
		hash = requestHash("run", caseID, environmentID, revision, true)
	}
	if previous, found, err := e.Runs.LookupRun(ctx, key, hash); err != nil || found {
		if err == nil {
			e.ensureBatches(previous)
		}
		return previous, err
	}
	if err := e.Catalog.CheckEnvironment(ctx, caseID, environmentID); err != nil {
		return runs.Run{}, err
	}
	c, err := e.Catalog.GetCase(ctx, caseID)
	if err != nil {
		return runs.Run{}, err
	}
	if c.Archived || c.ParentArchived {
		return runs.Run{}, problem.Archived()
	}
	if c.Revision != revision {
		return runs.Run{}, problem.Conflict("Сохраните и обновите актуальную версию сценария")
	}
	if len(c.Steps) == 0 {
		return runs.Run{}, problem.Invalid("steps", "Добавьте хотя бы один шаг")
	}
	if err = catalog.ValidateExecutable(c.Steps); err != nil {
		return runs.Run{}, err
	}
	if err = e.Catalog.CheckImages(ctx, c.ID, c.Steps); err != nil {
		return runs.Run{}, err
	}
	required, err := needs(c)
	if err != nil {
		return runs.Run{}, err
	}
	snapshot, err := e.Resources.Freeze(ctx, environmentID, required)
	if err != nil {
		return runs.Run{}, err
	}
	for _, step := range c.Steps {
		for _, block := range step.Blocks {
			res, err := e.Resources.Resolve(ctx, snapshot, block.Resource)
			if err != nil {
				return runs.Run{}, err
			}
			if _, _, err = res.Prepare(block); err != nil {
				return runs.Run{}, err
			}
		}
	}
	run, err := e.Runs.Start(ctx, c, snapshot, key, hash, all)
	if err == nil {
		e.ensureBatches(run)
	}
	return run, err
}
func (e *Engine) EditorAttempt(ctx context.Context, caseID, blockID, environmentID string, revision int64, key string) (execution.Attempt, error) {
	if err := commandKey(key); err != nil {
		return execution.Attempt{}, err
	}
	hash := requestHash("editor", caseID, blockID, environmentID, revision)
	if previous, found, err := e.Runs.LookupAttempt(ctx, key, hash); err != nil || found {
		return previous, err
	}
	if err := e.Catalog.CheckEnvironment(ctx, caseID, environmentID); err != nil {
		return execution.Attempt{}, err
	}
	c, err := e.Catalog.GetCase(ctx, caseID)
	if err != nil {
		return execution.Attempt{}, err
	}
	if c.Archived || c.ParentArchived {
		return execution.Attempt{}, problem.Archived()
	}
	if c.Revision != revision {
		return execution.Attempt{}, problem.Conflict("Сначала сохраните актуальную версию сценария")
	}
	b, err := findBlock(c, blockID)
	if err != nil {
		return execution.Attempt{}, err
	}
	if err = catalog.ValidateBlock(&b, "block"); err != nil {
		return execution.Attempt{}, err
	}
	if err = e.Catalog.CheckImages(ctx, c.ID, []catalog.Step{{Blocks: []catalog.Block{b}}}); err != nil {
		return execution.Attempt{}, err
	}
	snapshot, err := e.Resources.Freeze(ctx, environmentID, map[string]string{b.Resource: b.Kind})
	if err != nil {
		return execution.Attempt{}, err
	}
	return e.begin(ctx, c.ID, "", c.Revision, b, snapshot, key, hash)
}
func (e *Engine) RunAttempt(ctx context.Context, runID, blockID, key string) (execution.Attempt, error) {
	if err := commandKey(key); err != nil {
		return execution.Attempt{}, err
	}
	hash := requestHash("run-attempt", runID, blockID)
	if previous, found, err := e.Runs.LookupAttempt(ctx, key, hash); err != nil || found {
		return previous, err
	}
	run, err := e.GetRun(ctx, runID)
	if err != nil {
		return execution.Attempt{}, err
	}
	if run.Status != "running" {
		return execution.Attempt{}, problem.Conflict("История доступна только для просмотра")
	}
	b, err := findBlock(run.Snapshot, blockID)
	if err != nil {
		return execution.Attempt{}, err
	}
	snapshot, err := e.Runs.Context(ctx, runID)
	if err != nil {
		return execution.Attempt{}, err
	}
	return e.begin(ctx, run.TestCaseID, run.ID, run.Snapshot.Revision, b, snapshot, key, hash)
}
func (e *Engine) begin(ctx context.Context, caseID, runID string, revision int64, block catalog.Block, snapshot resources.Snapshot, key, hash string, options ...runs.AttemptOptions) (execution.Attempt, error) {
	res, err := e.Resources.Resolve(ctx, snapshot, block.Resource)
	if err != nil {
		return execution.Attempt{}, err
	}
	prepared, res, err := res.Prepare(block)
	if err != nil {
		return execution.Attempt{}, err
	}
	select {
	case e.slots <- struct{}{}:
	default:
		return execution.Attempt{}, problem.Conflict("Уже выполняются четыре вызова; дождитесь результата")
	}
	safe := execution.MaskBlock(prepared, resources.NewRedactor(res.Secrets))
	candidateID := catalog.NewID()
	jobCtx, cancel := context.WithCancel(e.ctx)
	e.mu.Lock()
	e.jobs[candidateID] = job{cancel: cancel, runID: runID}
	e.mu.Unlock()
	attempt, created, err := e.Runs.BeginAttempt(ctx, candidateID, caseID, runID, revision, block, snapshot, safe, key, hash, options...)
	if err != nil || !created {
		cancel()
		e.mu.Lock()
		delete(e.jobs, candidateID)
		e.mu.Unlock()
		<-e.slots
		return attempt, err
	}
	e.wg.Add(1)
	go e.execute(jobCtx, cancel, attempt, prepared, res)
	return attempt, nil
}
func (e *Engine) execute(ctx context.Context, cancel context.CancelFunc, attempt execution.Attempt, block catalog.Block, res resources.Resolved) {
	defer e.wg.Done()
	defer cancel()
	defer func() { e.mu.Lock(); delete(e.jobs, attempt.ID); e.mu.Unlock(); <-e.slots }()
	var err error
	for {
		claimCtx, claimCancel := context.WithTimeout(ctx, 10*time.Second)
		var claimed bool
		claimed, err = e.Runs.ClaimAttempt(claimCtx, attempt.ID)
		claimCancel()
		if err == nil {
			if !claimed {
				return
			}
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	scope := attempt.RunID
	if scope == "" {
		scope = attempt.ID
		defer e.Runner.ForgetScope(scope)
	}
	started := time.Now()
	ctx = execution.WithRequestGate(ctx, func(ctx context.Context) error {
		fence := e.fence(attempt.RunID)
		fence.Lock()
		defer fence.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		return e.Runs.CheckAttemptRunnable(ctx, attempt.ID)
	})
	state, result := e.Runner.Execute(ctx, scope, block, res)
	for retry := 0; ; retry++ {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = e.Runs.CompleteAttempt(persistCtx, attempt.ID, state, result, time.Since(started))
		persistCancel()
		if err == nil {
			return
		}
		e.mu.Lock()
		pending := e.jobs[attempt.ID]
		pending.persistencePending = true
		e.jobs[attempt.ID] = pending
		e.mu.Unlock()
		if retry == 0 {
			fmt.Fprintln(os.Stderr, "qag: результат попытки не подтверждён PostgreSQL:", attempt.ID)
		}
		select {
		case <-e.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
func (e *Engine) CancelAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	attempt, err := e.Runs.GetAttempt(ctx, id)
	if err != nil {
		return execution.Attempt{}, err
	}
	fence := e.fence(attempt.RunID)
	fence.Lock()
	defer fence.Unlock()
	result, err := e.Runs.CancelAttempt(ctx, id)
	if err != nil {
		return result, err
	}
	e.mu.Lock()
	if j, ok := e.jobs[id]; ok {
		j.cancel()
	}
	e.mu.Unlock()
	return result, nil
}
func (e *Engine) CancelRun(ctx context.Context, id string) (runs.Run, error) {
	fence := e.fence(id)
	fence.Lock()
	run, err := e.Runs.Cancel(ctx, id)
	if err != nil {
		fence.Unlock()
		return run, err
	}
	e.mu.Lock()
	for _, j := range e.jobs {
		if j.runID == id {
			j.cancel()
		}
	}
	e.mu.Unlock()
	fence.Unlock()
	if err == nil {
		e.Runner.ForgetScope(id)
	}
	return run, err
}

// GetAttempt reports an unconfirmed persistence result without changing data.
// The worker retries only the metadata write, never the external request.
func (e *Engine) GetAttempt(ctx context.Context, id string) (execution.Attempt, error) {
	attempt, err := e.Runs.GetAttempt(ctx, id)
	if err != nil {
		return attempt, err
	}
	if attempt.State != "queued" && attempt.State != "running" {
		return attempt, nil
	}
	e.mu.Lock()
	pending := e.jobs[id].persistencePending
	e.mu.Unlock()
	if !pending {
		return attempt, nil
	}
	message := "Результат внешнего действия не подтверждён; автоматического повтора не было"
	result := execution.Result{Headers: []catalog.KeyValue{}, Error: message, Comparison: comparison.Result{Status: "not_compared", Reason: message, Differences: []comparison.Difference{}}}
	attempt.PersistencePending = true
	attempt.Result = &result
	return attempt, nil
}
func (e *Engine) GetRun(ctx context.Context, id string) (runs.Run, error) {
	run, err := e.Runs.Get(ctx, id)
	if err != nil {
		return run, err
	}
	e.ensureBatches(run)
	for i, attempt := range run.Attempts {
		if attempt.State == "queued" || attempt.State == "running" {
			current, err := e.GetAttempt(ctx, attempt.ID)
			if err != nil {
				return run, err
			}
			run.Attempts[i] = current
		}
	}
	return run, nil
}
func (e *Engine) FinishRun(ctx context.Context, id string) (runs.Run, error) {
	if _, err := e.GetRun(ctx, id); err != nil {
		return runs.Run{}, err
	}
	run, err := e.Runs.Finish(ctx, id)
	if err == nil {
		e.Runner.ForgetScope(id)
	}
	return run, err
}
