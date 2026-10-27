import { useState } from 'react'
import type { Block, Run, Variant } from './api'
import { command, message, protocolLabel, request } from './api'
import { RequestFields } from './BlockEditor'
import { Results, RequestSummary, ExpectedPreview } from './Results'

type Draft = { name: string; request: Block; revision: number }
function formBlock(b: Block): Block {
  return {
    ...b,
    http: b.http
      ? { ...b.http, query: b.http.query || [], headers: b.http.headers || [] }
      : undefined,
    grpc: b.grpc ? { ...b.grpc, metadata: b.grpc.metadata || [] } : undefined,
    sql: b.sql ? { ...b.sql, parameters: b.sql.parameters || [] } : undefined,
  }
}
export function RunCallTabs({
  run,
  block,
  index,
  editable,
  disabled,
  refresh,
}: {
  run: Run
  block: Block
  index: number
  editable: boolean
  disabled: boolean
  refresh: () => Promise<void>
}) {
  const key = `qag.variant.${run.id}.${block.id}`
  const [selected, setSelected] = useState(() => {
    try {
      return sessionStorage.getItem(key) || ''
    } catch {
      return ''
    }
  })
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})
  const [busy, setBusy] = useState(false),
    [error, setError] = useState('')
  const variants = (run.variants || [])
    .filter((v) => v.blockId === block.id)
    .sort((a, b) => a.position - b.position)
  const variant = variants.find((v) => v.id === selected)
  const version = variant?.versions.find(
    (v) => v.id === variant.currentVersionId,
  )
  const draft = variant ? drafts[variant.id] : undefined
  const shown = formBlock(draft?.request || version?.request || block)
  const attempts = run.attempts
    .filter(
      (a) =>
        a.blockId === block.id && (a.variantId || '') === (variant?.id || ''),
    )
    .sort((a, b) => a.number - b.number)
  const latest = attempts.at(-1)
  const choose = (id: string) => {
    setSelected(id)
    try {
      sessionStorage.setItem(key, id)
    } catch {
      /* selection remains in memory */
    }
  }
  const perform = async (action: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await action()
      await refresh()
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  const change = (next: Partial<Draft>) => {
    if (!variant || !version) return
    setDrafts((old) => ({
      ...old,
      [variant.id]: {
        ...(old[variant.id] || {
          name: variant.name,
          request: version.request,
          revision: variant.revision,
        }),
        ...next,
      },
    }))
  }
  const save = async (v: Variant) => {
    const d = drafts[v.id]
    if (!d) return v
    const saved = await request<Variant>(
      `/runs/${run.id}/variants/${v.id}`,
      'PATCH',
      d,
    )
    setDrafts((old) => {
      const next = { ...old }
      delete next[v.id]
      return next
    })
    return saved
  }
  return (
    <div className="call-block">
      <div className="row-heading">
        <strong>
          <span className={`protocol ${block.kind}`}>
            {protocolLabel[block.kind]}
          </span>{' '}
          Вызов {index} · {block.resource}
        </strong>
      </div>
      <div
        className="call-tabs"
        role="tablist"
        aria-label={`Вкладки вызова ${index}`}
      >
        <button role="tab" aria-selected={!variant} onClick={() => choose('')}>
          Исходный
        </button>
        {variants.map((v) => (
          <button
            role="tab"
            aria-selected={v.id === variant?.id}
            key={v.id}
            onClick={() => choose(v.id)}
          >
            {drafts[v.id] ? '• ' : ''}
            {v.name}
          </button>
        ))}
        {editable && (
          <button
            className="text-button"
            disabled={disabled || busy || variants.length >= 20}
            onClick={() =>
              void perform(async () => {
                const v = await request<Variant>(
                  `/runs/${run.id}/blocks/${block.id}/variants`,
                  'POST',
                  { name: '' },
                )
                choose(v.id)
              })
            }
          >
            + Вариант
          </button>
        )}
      </div>
      {error && (
        <p role="alert" className="field-error">
          {error}
        </p>
      )}
      {variant && (
        <p className="muted small">
          Исследовательский вызов · не влияет на итог прогона · версия{' '}
          {variant.revision}
        </p>
      )}
      {variant && editable ? (
        <fieldset className="variant-editor" disabled={disabled || busy}>
          <label className="field">
            <span>Название варианта</span>
            <input
              aria-label="Название варианта"
              value={draft?.name ?? variant.name}
              onChange={(e) => change({ name: e.target.value })}
            />
          </label>
          <RequestFields
            block={shown}
            onChange={(next) =>
              change({
                request: {
                  ...next,
                  id: block.id,
                  kind: block.kind,
                  resource: block.resource,
                  expected: block.expected,
                },
              })
            }
          />
          <button
            className="button secondary"
            disabled={!draft}
            onClick={() =>
              void perform(async () => {
                await save(variant)
              })
            }
          >
            Сохранить вариант
          </button>
        </fieldset>
      ) : (
        !latest && <RequestSummary block={shown} />
      )}
      {!latest && <ExpectedPreview block={block} caseId={run.testCaseId} />}
      {editable && (
        <button
          className="button small-button"
          disabled={disabled || busy}
          onClick={() =>
            void perform(async () => {
              if (variant) {
                const saved = await save(variant)
                await command(`/runs/${run.id}/variants/${saved.id}/attempts`, {
                  revision: saved.revision,
                })
              } else {
                await command(`/runs/${run.id}/blocks/${block.id}/attempts`)
              }
            })
          }
        >
          {variant ? 'Запустить вариант' : 'Запустить вызов'}
        </button>
      )}
      <Results
        block={latest?.request || shown}
        attempt={latest}
        cancel={
          editable && latest
            ? () =>
                void perform(async () => {
                  await request(`/attempts/${latest.id}/cancel`, 'POST')
                })
            : undefined
        }
      />
      {attempts.length > 1 && (
        <details className="previous-attempts">
          <summary>Предыдущие попытки · {attempts.length - 1}</summary>
          {attempts.slice(0, -1).map((a) => (
            <Results key={a.id} block={a.request} attempt={a} />
          ))}
        </details>
      )}
      {variant && (
        <details className="variant-versions">
          <summary>Сохранённые версии · {variant.versions.length}</summary>
          {variant.versions.map((v) => (
            <section key={v.id}>
              <h4>Версия {v.revision}</h4>
              <RequestSummary block={v.request} />
            </section>
          ))}
        </details>
      )}
    </div>
  )
}
