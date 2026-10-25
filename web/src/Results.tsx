import type { Attempt, Block, Column, TypedValue } from './api'
import { protocolLabel, statusLabel } from './api'
import { Code, Notice } from './ui'

export function SQLTable({ columns, rows }: { columns: Column[]; rows: TypedValue[][] }) {
  return (
    <div className="table-scroll">
      <table className="sql-table">
        <thead>
          <tr>
            {columns.map((c, i) => (
              <th key={i}>
                {c.name}
                <small>{c.type}</small>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {row.map((cell, j) => (
                <td key={j}>
                  {cell.type === 'null' ? (
                    <span className="null-value">NULL</span>
                  ) : cell.type === 'json' ? (
                    <Code text={cell.value ?? ''} json />
                  ) : (
                    <code>{cell.type === 'string' && !cell.value ? '""' : cell.value}</code>
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <p className="muted small">Строк: {rows.length}</p>
    </div>
  )
}

export function Results({
  block,
  attempt,
  cancel,
}: {
  block: Block
  attempt?: Attempt
  cancel?: () => void
}) {
  if (!attempt) return null
  const original = attempt.request ?? block,
    expected = original.expected,
    result = attempt.result
  const busy = attempt.state === 'queued' || attempt.state === 'running'
  return (
    <section className="results" aria-label="Результат вызова">
      <div className="row-heading">
        <div className="inline">
          <strong>
            {protocolLabel[original.kind]} · версия сценария {attempt.revision}
          </strong>
          <span className={`status ${result?.comparison.status ?? attempt.state}`}>
            {statusLabel[result?.comparison.status ?? attempt.state] ?? attempt.state}
          </span>
        </div>
        {busy && cancel && (
          <button
            className="text-button danger"
            disabled={attempt.persistencePending}
            onClick={cancel}
          >
            Отменить вызов
          </button>
        )}
      </div>
      <div className="result-meta">
        <span>
          {attempt.environmentName} / {attempt.resource?.name ?? original.resource}
        </span>
        <span>{attempt.resource?.config.baseUrl || attempt.resource?.config.address}</span>
        <span>{attempt.durationMs} мс</span>
      </div>
      {attempt.persistencePending && (
        <Notice>
          Результат ещё не подтверждён PostgreSQL. Приложение повторяет только сохранение
          результата.
        </Notice>
      )}
      {busy && !result && <p className="muted">{statusLabel[attempt.state]}…</p>}
      {result && (
        <>
          {result.error && <Notice>{result.error}</Notice>}
          {result.statusMessage && <p className="protocol-message">{result.statusMessage}</p>}
          {result.truncated && <Notice>Ответ усечён. Полное совпадение не подтверждено.</Notice>}
          {result.redacted && (
            <p className="muted small">
              Известные секреты скрыты. Сравнение выполнено до скрытия значений.
            </p>
          )}
          {result.comparison.reason && <p className="muted small">{result.comparison.reason}</p>}
          <div className="comparison-grid">
            <div>
              <h5>Ожидалось</h5>
              <div className="protocol-summary">
                {original.kind === 'http'
                  ? `HTTP ${expected.statusCode}`
                  : original.kind === 'grpc'
                    ? expected.grpcCode
                    : expected.bodyMode === 'exec'
                      ? `Затронуто строк: ${expected.rowsAffected}`
                      : 'Строки PostgreSQL'}
              </div>
              {expected.bodyMode === 'rows' ? (
                <SQLTable columns={expected.columns ?? []} rows={expected.rows ?? []} />
              ) : expected.bodyMode === 'none' ? (
                <p className="muted small">Тело не проверяется</p>
              ) : (
                expected.bodyMode !== 'exec' && (
                  <Code text={expected.body ?? ''} json={expected.bodyMode === 'json'} />
                )
              )}
            </div>
            <div>
              <h5>Получено</h5>
              <div className="protocol-summary">
                {result.statusCode
                  ? `HTTP ${result.statusCode}`
                  : result.grpcCode ||
                    (result.rowsAffected !== undefined
                      ? `Затронуто строк: ${result.rowsAffected}`
                      : 'Ответ PostgreSQL')}
              </div>
              {result.columns ? (
                <SQLTable columns={result.columns} rows={result.rows ?? []} />
              ) : (
                <Code text={result.body} json={expected.bodyMode === 'json'} />
              )}
            </div>
          </div>
          {result.comparison.differences.length > 0 && (
            <details className="differences" open>
              <summary>
                Различия · {result.comparison.differences.length}
                {result.comparison.differencesTruncated ? '+' : ''}
              </summary>
              {result.comparison.differences.map((d, i) => (
                <div className="difference" key={i}>
                  <code>{d.path}</code>
                  <div className="comparison-grid">
                    <Code text={d.expected} />
                    <Code text={d.actual} />
                  </div>
                </div>
              ))}
            </details>
          )}
          {(result.headers.length > 0 || (result.trailers?.length ?? 0) > 0) && (
            <details>
              <summary>Заголовки и метаданные ответа</summary>
              <Code
                text={[...result.headers, ...(result.trailers ?? [])]
                  .map((v) => `${v.name}: ${v.value}`)
                  .join('\n')}
              />
            </details>
          )}
          <details>
            <summary>Выполненный запрос</summary>
            <Code
              text={
                original.http
                  ? `${original.http.method} ${original.http.path}\n${original.http.headers.map((v) => `${v.name}: ${v.value}`).join('\n')}\n\n${original.http.body}`
                  : original.grpc
                    ? `${original.grpc.service}/${original.grpc.method}\n${original.grpc.body}`
                    : (original.sql?.query ?? '')
              }
            />
          </details>
        </>
      )}
    </section>
  )
}
