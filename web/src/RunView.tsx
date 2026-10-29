import { useState } from 'react'
import type { Run, StepState } from './api'
import {
  command,
  date,
  message,
  reasonLabel,
  request,
  statusLabel,
} from './api'
import { Notice } from './ui'
import { StepStatus } from './StepStatus'
import { RunCallTabs } from './RunCallTabs'
import { ExportDialog } from './ExportDialog'
import { ImageGallery } from './ImageGallery'
import { ReadOnlyNotice } from './ReadOnlyNotice'
import { useRunStickyTop } from './useRunStickyTop'

export function mergeRun(previous: Run | null, next: Run): Run {
  if (!previous || previous.id !== next.id) return next
  if (previous.status !== 'running' && next.status === 'running')
    return previous
  const steps = new Map(previous.stepStates.map((v) => [v.stepId, v]))
  const attempts = new Map(previous.attempts.map((v) => [v.id, v]))
  for (const v of next.attempts)
    if (!attempts.get(v.id)?.finishedAt || v.finishedAt) attempts.set(v.id, v)
  return {
    ...next,
    stepStates: next.stepStates.map((v) =>
      (steps.get(v.stepId)?.revision ?? 0) > v.revision
        ? steps.get(v.stepId)!
        : v,
    ),
    attempts: [...attempts.values()],
  }
}

export default function RunView({
  run,
  update,
  onRefresh,
  readOnly = false,
}: {
  run: Run
  update: (r: Run) => void
  onRefresh: () => void
  readOnly?: boolean
}) {
  const viewRef = useRunStickyTop(run.status === 'running' && !readOnly)
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [comments, setComments] = useState<
    Record<string, { value: string; revision: number; baseComment: string }>
  >({})
  const editable = run.status === 'running' && !readOnly
  const perform = async (action: () => Promise<Run>) => {
    setBusy(true)
    setError('')
    try {
      update(await action())
      onRefresh()
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  const updateStep = (state: StepState, status = state.status) =>
    perform(async () => {
      const draft = comments[state.stepId]
      const result = await request<Run>(
        `/runs/${run.id}/steps/${state.stepId}`,
        'PATCH',
        {
          status,
          comment: draft?.value ?? state.comment,
          revision: draft?.revision ?? state.revision,
        },
      )
      setComments((old) => {
        const next = { ...old }
        delete next[state.stepId]
        return next
      })
      return result
    })
  const [cancelling, setCancelling] = useState(false)
  const passAll = () => perform(async () => {
    try {
      const result = await request<Run>(`/runs/${run.id}/steps/pass-all`, 'POST', {
        steps: run.stepStates.map(({ stepId, revision }) => ({ stepId, revision })),
      })
      setComments((old) => {
        const next = { ...old }
        for (const state of result.stepStates) {
          const draft = next[state.stepId]
          if (draft && draft.baseComment === state.comment)
            next[state.stepId] = { ...draft, revision: state.revision }
        }
        return next
      })
      return result
    } catch (e) {
      // A lost response may follow a commit: read before another explicit click.
      try { update(await request<Run>(`/runs/${run.id}`)) } catch { /* retain view and drafts */ }
      throw e
    }
  })
  const cancelRun = async () => {
    setCancelling(true)
    setError('')
    try {
      for (const [stepId, draft] of Object.entries(comments))
        await request<Run>(`/runs/${run.id}/steps/${stepId}`, 'PATCH', {
          status: run.stepStates.find((s) => s.stepId === stepId)!.status,
          comment: draft.value,
          revision: draft.revision,
        })
      setComments({})
      update(await request<Run>(`/runs/${run.id}/cancel`, 'POST'))
      onRefresh()
    } catch (e) {
      setError(message(e))
    } finally {
      setCancelling(false)
    }
  }
  const activeBatch = (run.batches || []).find((b) => b.state === 'running')
  const latestBatch = (run.batches || []).at(-1)
  const active =
    !!activeBatch ||
    run.attempts.some((a) => a.state === 'queued' || a.state === 'running')
  const unsaved = Object.keys(comments).length > 0
  return (
    <section className="run-view" ref={viewRef}>
      {exporting && (
        <ExportDialog runId={run.id} onClose={() => setExporting(false)} />
      )}
      <div className="page-heading">
        <div>
          <p className="section-label">
            {editable ? 'ТЕКУЩИЙ ПРОГОН' : 'СНИМОК ПРОВЕРКИ'}
          </p>
          <h2>{run.snapshot.name}</h2>
          <p className="muted small">
            {date(run.startedAt)} · {run.environmentName} · версия{' '}
            {run.snapshot.revision}
          </p>
        </div>
        <div className="inline">
          <button
            className="button secondary"
            onClick={() => setExporting(true)}
          >
            Экспорт результата
          </button>
          <span className={`status ${run.status}`}>
            {statusLabel[run.status]}
          </span>
        </div>
      </div>
      {run.snapshot.readOnly && <ReadOnlyNotice />}
      {editable && (
        <div className="run-toolbar">
          <button className="button secondary" disabled={busy || cancelling} onClick={() => void passAll()}>
            Пометить все пройденным
          </button>
          <button
            className="button"
            disabled={busy || active || cancelling}
            onClick={() =>
              void perform(async () => {
                await command(`/runs/${run.id}/batches`)
                return request<Run>(`/runs/${run.id}`)
              })
            }
          >
            Выполнить всё
          </button>
          <button
            className="button"
            disabled={busy || active || unsaved || cancelling}
            onClick={() => void perform(() => request<Run>(`/runs/${run.id}/finish`, 'POST'))}
          >
            Завершить прогон
          </button>
          <span role="status" className="batch-progress">
            {latestBatch
              ? `Выполнено ${latestBatch.completed}/${latestBatch.items.length} · проблем: ${latestBatch.problems}${latestBatch.state === 'running' ? ' · выполняется' : latestBatch.state === 'completed' ? ' · очередь завершена' : latestBatch.state === 'cancelled' ? ' · отменена' : ' · прервана'}`
              : 'Статусы шагов выставляются вручную'}
          </span>
          <button
            className="button secondary danger"
            disabled={busy || cancelling}
            onClick={() => void cancelRun()}
          >
            {cancelling ? 'Отмена…' : 'Отменить прогон'}
          </button>
          <span className="muted small run-hint">
            {unsaved ? 'Сохраните комментарии перед завершением.' : active ? 'Дождитесь ответа или отмените прогон.' : 'Успешные вызовы не заменяют ручную оценку шагов.'}
          </span>
        </div>
      )}
      <div className="run-reasons">
        {run.retentionReasons.map((reason) => (
          <span className="badge" key={reason}>
            {reasonLabel[reason]}
          </span>
        ))}
      </div>
      {error && <Notice onClose={() => setError('')}>{error}</Notice>}
      {run.snapshot.steps.map((step, index) => {
        const state = run.stepStates.find((v) => v.stepId === step.id)!
        const comment = comments[step.id]?.value ?? state.comment
        return (
          <article className="step-card run-step" key={step.id}>
            <div className="run-step-content">
              <div className="step-heading">
                <span className="step-number">{index + 1}</span>
                <h3>{step.title || `Шаг ${index + 1}`}</h3>
              </div>
              <p className="step-description">{step.description}</p>
              {step.expected?.text && (
                <section className="manual-expectation">
                  <h4>Ожидаемый результат шага</h4>
                  <p>{step.expected.text}</p>
                </section>
              )}
              <ImageGallery
                caseId={run.testCaseId}
                images={step.expected?.images}
              />
              {step.blocks.map((block, b) => (
                <RunCallTabs
                  key={block.id}
                  run={run}
                  block={block}
                  index={b + 1}
                  editable={editable}
                  disabled={busy || active || cancelling}
                  refresh={async () => {
                    update(await request<Run>(`/runs/${run.id}`))
                    onRefresh()
                  }}
                />
              ))}
              <div className="manual-result">
                <label className="field">
                  <span>Комментарий к шагу {index + 1}</span>
                  <textarea
                    aria-label={`Комментарий к шагу ${index + 1}`}
                    disabled={!editable || busy || cancelling}
                    rows={2}
                    value={comment}
                    onChange={(e) => {
                      const value = e.target.value
                      setComments((old) => {
                        const next = { ...old }
                        if (value === state.comment) delete next[step.id]
                        else
                          next[step.id] = {
                            value,
                            revision: old[step.id]?.revision ?? state.revision,
                            baseComment: old[step.id]?.baseComment ?? state.comment,
                          }
                        return next
                      })
                    }}
                  />
                </label>
                {comments[step.id] && (
                  <button
                    className="button secondary"
                    disabled={busy}
                    onClick={() => void updateStep(state)}
                  >
                    Сохранить комментарий
                  </button>
                )}
              </div>
            </div>
            <aside className="run-step-result" aria-label={`Результат шага ${index + 1}`}>
              <div className="run-step-status">
                <span className="section-label">Результат</span>
                <StepStatus
                  state={state}
                  index={index + 1}
                  disabled={busy || cancelling}
                  onChange={editable ? (status) => void updateStep(state, status) : undefined}
                />
              </div>
            </aside>
          </article>
        )
      })}
      {run.status !== 'running' &&
        run.retentionReasons.length === 0 &&
        !readOnly && (
          <div className="action-bar">
            <p className="muted small">
              Прогон доступен в несохранённых. Его можно сохранить отдельно.
            </p>
            <button
              className="button"
              disabled={busy}
              onClick={() =>
                void perform(() => request<Run>(`/runs/${run.id}/save`, 'POST'))
              }
            >
              Сохранить прогон
            </button>
          </div>
        )}
      {run.resources.length > 0 && (
        <details className="snapshot-context">
          <summary>Ресурсы прогона · {run.environmentName}</summary>
          {run.resources.map((r) => (
            <div className="result-meta" key={r.id}>
              <strong>{r.name}</strong>
              <span>{r.config.baseUrl || r.config.address}</span>
              <span>Версия {r.revision}</span>
            </div>
          ))}
        </details>
      )}
    </section>
  )
}
