import type { ResourceConfig, VariableDefinition } from './api'
import { HTTPSourceFields, newHTTPSource } from './HeaderEditor'

export type VariableRow = VariableDefinition & { id: string; name: string }
export function variableMap(rows: VariableRow[]): NonNullable<ResourceConfig['variables']> {
  const named = rows.filter(row => row.name.trim())
  if (new Set(named.map(row => row.name)).size !== named.length)
    throw new Error('Имена переменных ресурса должны быть уникальными')
  return Object.fromEntries(named.map(({ name, mode, value, source }) => [name,
    mode === 'http' ? { mode, source } : { mode, value: value ?? '' },
  ]))
}
export function ResourceVariables({ rows, change, secrets }: {
  rows: VariableRow[]
  change: (rows: VariableRow[]) => void
  secrets: string[]
}) {
  const update = (id: string, patch: Partial<VariableRow>) => change(rows.map(row => row.id === id ? { ...row, ...patch } : row))
  return <fieldset className="pair-editor">
    <legend>Переменные ресурса</legend>
    <p className="muted small">Используйте {'${NAME}'} в HTTP, gRPC и SQL-параметрах этого ресурса. Пустые имена пропускаются. HTTP-источники используют только статические переменные и секреты.</p>
    {rows.map((row, i) => <article className="header-row" key={row.id}>
      <div className="header-line">
        <input aria-label={`Переменная ресурса: имя ${i + 1}`} placeholder="VARIABLE_NAME" value={row.name} onChange={e => update(row.id, { name: e.target.value })} />
        <select aria-label={`Переменная ресурса: способ ${i + 1}`} value={row.mode} onChange={e => update(row.id,
          e.target.value === 'http' ? { mode: 'http', value: undefined, source: newHTTPSource() } : { mode: 'literal', value: '', source: undefined })}>
          <option value="literal">Значение</option><option value="http">Из HTTP-запроса</option>
        </select>
        <button type="button" className="icon-button" aria-label={`Удалить переменную ресурса ${i + 1}`} onClick={() => change(rows.filter(r => r.id !== row.id))}>×</button>
      </div>
      {row.mode === 'literal' ? <input aria-label={`Переменная ресурса: значение ${i + 1}`} value={row.value ?? ''} onChange={e => update(row.id, { value: e.target.value })} />
        : <HTTPSourceFields value={row.source!} change={patch => update(row.id, { source: { ...row.source!, ...patch } })} secrets={secrets} />}
    </article>)}
    <button type="button" className="text-button" onClick={() => change([...rows, { id: crypto.randomUUID(), name: '', mode: 'literal', value: '' }])}>+ Переменная ресурса</button>
  </fieldset>
}
