import type { Environment, ResourceConfig, VariableDefinition } from './api'
import { HTTPSourceFields, newHTTPSource } from './HeaderEditor'

export type VariableRow = VariableDefinition & { id: string; name: string }
export function variableMap(rows: VariableRow[], scope = 'ресурса'): NonNullable<ResourceConfig['variables']> {
  const named = rows.filter(row => row.name.trim())
  if (new Set(named.map(row => row.name)).size !== named.length)
    throw new Error(`Имена переменных ${scope} должны быть уникальными`)
  return Object.fromEntries(named.map(({ name, mode, value, source }) => [name,
    mode === 'http' ? { mode, source } : { mode, value: value ?? '' },
  ]))
}
export function environmentVariableWrite(rows: VariableRow[]) {
  const definitions = variableMap(rows, 'окружения')
  return {
    variables: Object.fromEntries(Object.entries(definitions).filter(([, def]) => def.mode === 'literal').map(([name, def]) => [name, def.value ?? ''])),
    httpVariables: Object.fromEntries(Object.entries(definitions).filter(([, def]) => def.mode === 'http').map(([name, def]) => [name, def.source!])) as NonNullable<Environment['httpVariables']>,
  }
}

export function ResourceVariables({ rows, change, secrets, scope = 'resource' }: {
  rows: VariableRow[]
  change: (rows: VariableRow[]) => void
  secrets: string[]
  scope?: 'resource' | 'environment'
}) {
  const owner = scope === 'resource' ? 'ресурса' : 'окружения'
  const update = (id: string, patch: Partial<VariableRow>) => change(rows.map(row => row.id === id ? { ...row, ...patch } : row))
  return <fieldset className="pair-editor">
    <legend>Переменные {owner}</legend>
    <p className="muted small">Используйте {'${NAME}'} в HTTP, gRPC и SQL-параметрах {scope === 'resource' ? 'этого ресурса' : 'любого ресурса окружения'}. Пустые имена пропускаются. HTTP-источники используют только статические переменные и секреты {scope === 'resource' ? 'окружения и ресурса' : 'своего окружения'}.</p>
    {rows.map((row, i) => <article className="header-row" key={row.id}>
      <div className="header-line">
        <input aria-label={`Переменная ${owner}: имя ${i + 1}`} placeholder="VARIABLE_NAME" value={row.name} onChange={e => update(row.id, { name: e.target.value })} />
        <select aria-label={`Переменная ${owner}: способ ${i + 1}`} value={row.mode} onChange={e => update(row.id,
          e.target.value === 'http' ? { mode: 'http', value: undefined, source: newHTTPSource() } : { mode: 'literal', value: '', source: undefined })}>
          <option value="literal">Значение</option><option value="http">Из HTTP-запроса</option>
        </select>
        <button type="button" className="icon-button" aria-label={`Удалить переменную ${owner} ${i + 1}`} onClick={() => change(rows.filter(r => r.id !== row.id))}>×</button>
      </div>
      {row.mode === 'literal' ? <input aria-label={`Переменная ${owner}: значение ${i + 1}`} value={row.value ?? ''} onChange={e => update(row.id, { value: e.target.value })} />
        : <HTTPSourceFields value={row.source!} change={patch => update(row.id, { source: { ...row.source!, ...patch } })} secrets={secrets} />}
    </article>)}
    <button type="button" className="text-button" onClick={() => change([...rows, { id: crypto.randomUUID(), name: '', mode: 'literal', value: '' }])}>+ Переменная {owner}</button>
  </fieldset>
}
