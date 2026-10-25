import type { Block, Expectation, Resource, TypedValue } from './api'
import { newBlock, protocolLabel } from './api'
import { Field, Pairs, ValueInput } from './ui'

type Props = {
  block: Block
  resources: Resource[]
  onChange: (b: Block) => void
  disabled: boolean
  fields: Record<string, string>
  prefix: string
}
export function BlockEditor({ block: b, resources, onChange, disabled, fields, prefix }: Props) {
  const error = (key: string) =>
    fields[`${prefix}.${key}`] ||
    (key === 'resource' &&
    b.resource &&
    !resources.some((r) => r.name === b.resource && r.kind === b.kind)
      ? 'Ресурс отсутствует в окружении или имеет другой тип'
      : '')
  const expected = (change: Partial<Expectation>) =>
    onChange({ ...b, expected: { ...b.expected, ...change } })
  const compatible = resources.filter((r) => r.kind === b.kind)
  return (
    <fieldset className="block-fields" disabled={disabled}>
      <div className="form-grid three">
        <Field label="Протокол" error={error('kind')}>
          <select
            value={b.kind}
            onChange={(e) => onChange({ ...newBlock(e.target.value as Block['kind']), id: b.id })}
          >
            {Object.entries(protocolLabel).map(([kind, label]) => (
              <option key={kind} value={kind}>
                {label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Ресурс" error={error('resource')}>
          <input
            list={`resources-${b.id}`}
            placeholder="AI_GW_SERVISE"
            value={b.resource}
            onChange={(e) => onChange({ ...b, resource: e.target.value })}
          />
          <datalist id={`resources-${b.id}`}>
            {compatible.map((r) => (
              <option key={r.id}>{r.name}</option>
            ))}
          </datalist>
        </Field>
        <Field label="Тайм-аут, сек." error={error('timeoutSeconds')}>
          <input
            type="number"
            min="1"
            max="300"
            value={b.timeoutSeconds}
            onChange={(e) => onChange({ ...b, timeoutSeconds: Number(e.target.value) })}
          />
        </Field>
      </div>
      {b.http && (
        <>
          <div className="form-grid http-line">
            <Field label="Метод" error={error('http.method')}>
              <select
                value={b.http.method}
                onChange={(e) => onChange({ ...b, http: { ...b.http!, method: e.target.value } })}
              >
                {['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </select>
            </Field>
            <Field
              label="Путь или ссылка на ресурс"
              error={error('http.path')}
              hint="Например /task или AI_GW_SERVISE/task. Переменные: ${TASK_ID}."
            >
              <input
                className="mono"
                value={b.http.path}
                onChange={(e) => {
                  const match = /^([A-Z][A-Z0-9_]*)\/(.*)$/.exec(e.target.value)
                  onChange({
                    ...b,
                    resource: match?.[1] ?? b.resource,
                    http: { ...b.http!, path: match ? `/${match[2]}` : e.target.value },
                  })
                }}
              />
            </Field>
          </div>
          <Pairs
            label="Query-параметры"
            value={b.http.query}
            onChange={(v) => onChange({ ...b, http: { ...b.http!, query: v } })}
          />
          <div className={error('http.headers') ? 'invalid' : ''}>
            <Pairs
              label="Заголовки запроса"
              value={b.http.headers}
              onChange={(v) => onChange({ ...b, http: { ...b.http!, headers: v } })}
            />
            {error('http.headers') && (
              <small className="field-error">{error('http.headers')}</small>
            )}
          </div>
          <Field label="Формат тела" error={error('http.bodyMode')}>
            <select
              value={b.http.bodyMode}
              onChange={(e) =>
                onChange({
                  ...b,
                  http: { ...b.http!, bodyMode: e.target.value as 'none' | 'text' | 'json' },
                })
              }
            >
              <option value="none">Без тела</option>
              <option value="json">JSON</option>
              <option value="text">Текст</option>
            </select>
          </Field>
          {b.http.bodyMode !== 'none' && (
            <Field label="Тело запроса" error={error('http.body')}>
              <textarea
                className="mono"
                rows={5}
                value={b.http.body}
                onChange={(e) => onChange({ ...b, http: { ...b.http!, body: e.target.value } })}
              />
            </Field>
          )}
        </>
      )}
      {b.grpc && (
        <>
          <div className="form-grid">
            <Field label="Сервис gRPC" error={error('grpc.method')}>
              <input
                className="mono"
                placeholder="package.Service"
                value={b.grpc.service}
                onChange={(e) => onChange({ ...b, grpc: { ...b.grpc!, service: e.target.value } })}
              />
            </Field>
            <Field label="Метод gRPC">
              <input
                className="mono"
                placeholder="Method"
                value={b.grpc.method}
                onChange={(e) => onChange({ ...b, grpc: { ...b.grpc!, method: e.target.value } })}
              />
            </Field>
          </div>
          <Field label="JSON сообщения" error={error('grpc.body')}>
            <textarea
              className="mono"
              rows={5}
              value={b.grpc.body}
              onChange={(e) => onChange({ ...b, grpc: { ...b.grpc!, body: e.target.value } })}
            />
          </Field>
          <Pairs
            label="Метаданные gRPC"
            value={b.grpc.metadata}
            onChange={(v) => onChange({ ...b, grpc: { ...b.grpc!, metadata: v } })}
          />
        </>
      )}
      {b.sql && (
        <>
          <Field
            label="SQL-запрос"
            error={error('sql.query')}
            hint="Значения передаются как $1, $2… Для стабильного порядка строк используйте ORDER BY."
          >
            <textarea
              className="mono"
              rows={5}
              placeholder="SELECT * FROM tasks WHERE id = $1"
              value={b.sql.query}
              onChange={(e) => onChange({ ...b, sql: { ...b.sql!, query: e.target.value } })}
            />
          </Field>
          <fieldset className="pair-editor">
            <legend>SQL-параметры</legend>
            {b.sql.parameters.map((v, i) => (
              <div className="parameter-row" key={i}>
                <code>${i + 1}</code>
                {error(`sql.parameters.${i}`) && (
                  <small className="field-error" role="alert">
                    {error(`sql.parameters.${i}`)}
                  </small>
                )}
                <ValueInput
                  value={v}
                  label={`Параметр $${i + 1}`}
                  onChange={(value) =>
                    onChange({
                      ...b,
                      sql: {
                        ...b.sql!,
                        parameters: b.sql!.parameters.map((p, n) => (n === i ? value : p)),
                      },
                    })
                  }
                />
                <button
                  type="button"
                  className="icon-button"
                  aria-label={`Удалить параметр ${i + 1}`}
                  onClick={() =>
                    onChange({
                      ...b,
                      sql: { ...b.sql!, parameters: b.sql!.parameters.filter((_, n) => n !== i) },
                    })
                  }
                >
                  ×
                </button>
              </div>
            ))}
            <button
              type="button"
              className="text-button"
              onClick={() =>
                onChange({
                  ...b,
                  sql: {
                    ...b.sql!,
                    parameters: [...b.sql!.parameters, { type: 'string', value: '' }],
                  },
                })
              }
            >
              + Параметр
            </button>
          </fieldset>
          <Field label="Результат SQL" error={error('sql.mode')}>
            <select
              value={b.sql.mode}
              onChange={(e) =>
                onChange({
                  ...b,
                  sql: { ...b.sql!, mode: e.target.value as 'rows' | 'exec' },
                  expected:
                    e.target.value === 'rows'
                      ? { bodyMode: 'rows', columns: [], rows: [] }
                      : { bodyMode: 'exec', rowsAffected: '1' },
                })
              }
            >
              <option value="rows">Строки</option>
              <option value="exec">Команда — число затронутых строк</option>
            </select>
          </Field>
        </>
      )}
      <div className="expectation-editor">
        <h4>Ожидаемый результат</h4>
        {b.kind === 'http' && (
          <>
            <Field label="Ожидаемый HTTP-статус" error={error('expected.statusCode')}>
              <input
                type="number"
                min="100"
                max="599"
                value={b.expected.statusCode ?? 200}
                onChange={(e) => expected({ statusCode: Number(e.target.value) })}
              />
            </Field>
            <Pairs
              label="Ожидаемые заголовки"
              value={b.expected.headers ?? []}
              onChange={(v) => expected({ headers: v })}
            />
          </>
        )}
        {b.kind === 'grpc' && (
          <Field label="Ожидаемый gRPC-код" error={error('expected.grpcCode')}>
            <select
              value={b.expected.grpcCode ?? 'OK'}
              onChange={(e) => expected({ grpcCode: e.target.value })}
            >
              {[
                'OK',
                'CANCELLED',
                'UNKNOWN',
                'INVALID_ARGUMENT',
                'DEADLINE_EXCEEDED',
                'NOT_FOUND',
                'ALREADY_EXISTS',
                'PERMISSION_DENIED',
                'RESOURCE_EXHAUSTED',
                'FAILED_PRECONDITION',
                'ABORTED',
                'OUT_OF_RANGE',
                'UNIMPLEMENTED',
                'INTERNAL',
                'UNAVAILABLE',
                'DATA_LOSS',
                'UNAUTHENTICATED',
              ].map((v) => (
                <option key={v}>{v}</option>
              ))}
            </select>
          </Field>
        )}
        {b.kind !== 'postgresql' && (
          <>
            <Field label="Проверка тела">
              <select
                value={b.expected.bodyMode}
                onChange={(e) => expected({ bodyMode: e.target.value as 'none' | 'json' | 'text' })}
              >
                <option value="none">Только статус — тело не проверяется</option>
                <option value="json">JSON — структурное сравнение</option>
                <option value="text">Текст — точное сравнение</option>
              </select>
            </Field>
            {b.expected.bodyMode !== 'none' && (
              <Field label="Ожидаемое тело" error={error('expected.body')}>
                <textarea
                  className="mono"
                  rows={5}
                  value={b.expected.body ?? ''}
                  onChange={(e) => expected({ body: e.target.value })}
                />
              </Field>
            )}
          </>
        )}
        {b.sql?.mode === 'exec' && (
          <Field label="Ожидаемое число затронутых строк" error={error('expected.rowsAffected')}>
            <input
              className="mono"
              inputMode="numeric"
              value={b.expected.rowsAffected ?? ''}
              onChange={(e) => expected({ rowsAffected: e.target.value })}
            />
          </Field>
        )}
        {b.sql?.mode === 'rows' && (
          <ExpectedRows expected={b.expected} onChange={expected} error={error('expected.rows')} />
        )}
      </div>
    </fieldset>
  )
}

function ExpectedRows({
  expected,
  onChange,
  error,
}: {
  expected: Expectation
  onChange: (v: Partial<Expectation>) => void
  error?: string
}) {
  const columns = expected.columns ?? [],
    rows = expected.rows ?? []
  return (
    <div className={error ? 'invalid' : ''}>
      <p className="muted small">
        Колонки и строки сравниваются в указанном порядке; NULL отличается от пустой строки.
      </p>
      {error && <p className="field-error">{error}</p>}
      {columns.map((c, i) => (
        <div className="pair" key={i}>
          <input
            aria-label={`Ожидаемая колонка ${i + 1}`}
            placeholder="Имя колонки"
            value={c.name}
            onChange={(e) =>
              onChange({
                columns: columns.map((v, n) => (n === i ? { ...v, name: e.target.value } : v)),
              })
            }
          />
          <input
            aria-label={`PostgreSQL-тип колонки ${i + 1}`}
            placeholder="Тип PostgreSQL: text, int4, numeric…"
            value={c.type}
            onChange={(e) =>
              onChange({
                columns: columns.map((v, n) => (n === i ? { ...v, type: e.target.value } : v)),
              })
            }
          />
          <button
            type="button"
            className="icon-button"
            aria-label={`Удалить колонку ${i + 1}`}
            onClick={() =>
              onChange({
                columns: columns.filter((_, n) => n !== i),
                rows: rows.map((row) => row.filter((_, n) => n !== i)),
              })
            }
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        onClick={() =>
          onChange({
            columns: [...columns, { name: '', type: 'text' }],
            rows: rows.map((row) => [...row, { type: 'string', value: '' }]),
          })
        }
      >
        + Колонка
      </button>
      {rows.map((row, r) => (
        <div className="expected-row" key={r}>
          <div className="row-heading">
            <strong>Строка {r + 1}</strong>
            <button
              type="button"
              className="text-button danger"
              onClick={() => onChange({ rows: rows.filter((_, n) => n !== r) })}
            >
              Удалить строку
            </button>
          </div>
          {row.map((value, c) => (
            <Field key={c} label={columns[c]?.name || `Колонка ${c + 1}`}>
              <ValueInput
                value={value}
                label={`Строка ${r + 1}, колонка ${c + 1}`}
                onChange={(v) =>
                  onChange({
                    rows: rows.map((old, n) =>
                      n === r ? old.map((cell, index) => (index === c ? v : cell)) : old,
                    ),
                  })
                }
              />
            </Field>
          ))}
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        disabled={!columns.length}
        onClick={() =>
          onChange({
            rows: [...rows, columns.map((): TypedValue => ({ type: 'string', value: '' }))],
          })
        }
      >
        + Ожидаемая строка
      </button>
    </div>
  )
}
