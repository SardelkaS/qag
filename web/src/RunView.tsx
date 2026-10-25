import { useState } from 'react'
import type { Block, Run, StepState } from './api'
import {
  command,
  date,
  message,
  protocolLabel,
  reasonLabel,
  request,
  statusLabel,
} from './api'
import { Notice } from './ui'
import { Results, RequestSummary, ExpectedPreview } from './Results'
import { ImageGallery } from './ImageGallery'

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
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const [comments, setComments] = useState<
    Record<string, { value: string; revision: number }>
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
  const launch = async (block: Block) => {
    setBusy(true)
    setError('')
    try {
      await command(`/runs/${run.id}/blocks/${block.id}/attempts`)
      update(await request<Run>(`/runs/${run.id}`))
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  const cancelAttempt = async (id: string) => {
    setBusy(true)
    try {
      await request(`/attempts/${id}/cancel`, 'POST')
      update(await request<Run>(`/runs/${run.id}`))
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  const active = run.attempts.some(
    (a) => a.state === 'queued' || a.state === 'running',
  )
  const unsaved = Object.keys(comments).length > 0
  return (
    <section className="run-view">
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
        <span className={`status ${run.status}`}>
          {statusLabel[run.status]}
        </span>
      </div>
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
          <article className="step-card" key={step.id}>
            <div className="step-heading">
              <span className="step-number">{index + 1}</span>
              <h3>{step.title || `Шаг ${index + 1}`}</h3>
              <span className={`status ${state.status}`}>
                {statusLabel[state.status]}
              </span>
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
            {step.blocks.map((block, b) => {
              const attempts = run.attempts
                  .filter((v) => v.blockId === block.id)
                  .sort((a, z) => a.number - z.number),
                latest = attempts.at(-1)
              return (
                <div className="call-block" key={block.id}>
                  <div className="row-heading">
                    <strong>
                      <span className={`protocol ${block.kind}`}>
                        {protocolLabel[block.kind]}
                      </span>{' '}
                      Вызов {b + 1} · {block.resource}
                    </strong>
                    {editable && (
                      <button
                        className="button small-button"
                        disabled={busy || active}
                        onClick={() => void launch(block)}
                      >
                        Запустить вызов
                      </button>
                    )}
                  </div>
                  {!latest && (
                    <>
                      <RequestSummary block={block} />
                      <ExpectedPreview block={block} caseId={run.testCaseId} />
                    </>
                  )}
                  <Results
                    block={block}
                    attempt={latest}
                    cancel={
                      editable && latest
                        ? () => void cancelAttempt(latest.id)
                        : undefined
                    }
                  />
                  {attempts.length > 1 && (
                    <details className="previous-attempts">
                      <summary>
                        Предыдущие попытки · {attempts.length - 1}
                      </summary>
                      {attempts.slice(0, -1).map((a) => (
                        <Results key={a.id} block={block} attempt={a} />
                      ))}
                    </details>
                  )}
                </div>
              )
            })}
            <div className="manual-result">
              <label className="field">
                <span>Статус шага {index + 1}</span>
                <select
                  aria-label={`Статус шага ${index + 1}`}
                  disabled={!editable || busy}
                  value={state.status}
                  onChange={(e) =>
                    void updateStep(
                      state,
                      e.target.value as StepState['status'],
                    )
                  }
                >
                  {['not_run', 'passed', 'failed', 'blocked', 'skipped'].map(
                    (s) => (
                      <option key={s} value={s}>
                        {statusLabel[s]}
                      </option>
                    ),
                  )}
                </select>
              </label>
              <label className="field">
                <span>Комментарий к шагу {index + 1}</span>
                <textarea
                  aria-label={`Комментарий к шагу ${index + 1}`}
                  disabled={!editable || busy}
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
          </article>
        )
      })}
      {editable && (
        <div className="action-bar">
          <span className="muted small">
            {unsaved
              ? 'Сохраните комментарии перед завершением.'
              : active
                ? 'Дождитесь ответа или отмените прогон.'
                : 'Успешные вызовы не заменяют ручную оценку шагов.'}
          </span>
          <button
            className="button secondary danger"
            disabled={busy}
            onClick={() =>
              void perform(() => request<Run>(`/runs/${run.id}/cancel`, 'POST'))
            }
          >
            Отменить прогон
          </button>
          <button
            className="button"
            disabled={busy || active || unsaved}
            onClick={() =>
              void perform(() => request<Run>(`/runs/${run.id}/finish`, 'POST'))
            }
          >
            Завершить прогон
          </button>
        </div>
      )}
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
