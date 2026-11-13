import { useEffect, useRef, useState, type ReactNode } from 'react'
import type { CallLog, Environment, LogSummary, Page, Resource } from './api'
import { all, message, protocolLabel, request, statusLabel } from './api'
import { CallLogContext, type LogTarget } from './CallLogContext'
import { Code, Field, Modal, Notice } from './ui'
import { SQLTable } from './Results'
import './diagnostics.css'

const stages: Record<string, string> = { main: 'Основной вызов', header_source: 'Источник заголовка', environment_variable_source: 'Переменная окружения', resource_variable_source: 'Переменная ресурса', variable_probe: 'Проверка переменной' }
const origins: Record<string, string> = { editor: 'Редактор', run: 'Прогон', variant: 'Исследовательский вариант', variable_probe: 'Проверка переменной' }
const stamp = (value: string) => new Date(value).toLocaleString()

export function CallLogsProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<LogTarget | null>(null)
  const previousFocus = useRef<HTMLElement | null>(null)
  const close = () => { setTarget(null); previousFocus.current?.focus() }
  return <CallLogContext.Provider value={value => { previousFocus.current = document.activeElement as HTMLElement; setTarget(value) }}>
    {children}
    {target && <div className="call-log-overlay"><Modal title="Журнал вызовов" onClose={close}>
      <CallLogs key={JSON.stringify(target)} target={target} compact />
    </Modal></div>}
  </CallLogContext.Provider>
}

function HeaderList({ values }: { values?: { name: string; value: string }[] }) {
  if (!values?.length) return <p className="muted small">Нет заголовков</p>
  return <Code text={values.map(h => `${h.name}: ${h.value}`).join('\n')} />
}

export function LogDetails({ id }: { id: string }) {
  const [log, setLog] = useState<CallLog | null>(null), [error, setError] = useState('')
  useEffect(() => {
    let active = true, timer: ReturnType<typeof setTimeout> | undefined
    const load = async () => {
      try {
        const value = await request<CallLog>(`/call-logs/${encodeURIComponent(id)}`)
        if (!active) return
        setLog(value); setError('')
        if (['queued', 'running'].includes(value.summary.state) || value.summary.persistencePending) timer = setTimeout(() => void load(), 700)
      } catch (e) { if (active) setError(message(e)) }
    }
    setLog(null); void load()
    return () => { active = false; clearTimeout(timer) }
  }, [id])
  if (error) return <Notice>{error}</Notice>
  if (!log) return <p className="muted">Загрузка записи…</p>
  const { summary: s, request: q, response: r } = log
  return <section className="log-details" aria-label="Детали вызова">
    <div className="row-heading"><h3>{stages[s.stage] ?? s.stage}{s.sourceName && ` · ${s.sourceName}`}</h3><span className={`status ${s.state}`}>{statusLabel[s.state] ?? s.state}</span></div>
    <p className="muted small">{stamp(s.startedAt)} · {s.durationMs} мс · {origins[s.origin]} · {s.environmentName}{s.resourceName && ` / ${s.resourceName}`}</p>
    {s.persistencePending && <Notice>Запись результата ещё не подтверждена хранилищем. Внешний вызов не повторяется.</Notice>}
    {log.incompleteReason && <Notice>{log.incompleteReason}</Notice>}
    {s.legacy && <p className="muted">Доступно сохранённое определение. Фактические параметры и тела источников этой прежней попытки отсутствуют.</p>}
    {!s.legacy && !s.requestSent && <p className="muted">{s.state === 'interrupted' ? 'Неизвестно, успел ли запрос повлиять на внешний сервис.' : ['running', 'queued'].includes(s.state) ? 'Выполнение зарегистрировано; подтверждённого ответа пока нет.' : 'Прикладной запрос не отправлен.'}</p>}
    <div className="log-request-response">
      <section><h4>Запрос</h4><p className="protocol-summary">{protocolLabel[q.protocol]} · {q.method} {q.url || q.address}</p>
        {q.truncated && <Notice>Диагностическая копия запроса усечена. Отправленные данные не изменялись.</Notice>}
        {q.protocol === 'postgresql' ? <><Code text={q.sql ?? ''} /><h5>Связанные параметры</h5><Code text={JSON.stringify(q.parameters ?? [], null, 2)} json /><p className="muted small">Режим: {q.mode}</p></> : <><h5>Заголовки / метаданные</h5><HeaderList values={q.headers} /><h5>Тело</h5><Code text={q.body} json={/^[\s]*[\[{]/.test(q.body)} /></>}
      </section>
      <section><h4>Ответ</h4>{!r ? <p className="muted">Ответ не получен.</p> : <>
        <p className="protocol-summary">{r.statusCode ? `HTTP ${r.statusCode}` : r.grpcCode || (q.protocol === 'postgresql' ? 'PostgreSQL' : 'Нет HTTP-ответа')}</p>
        {r.error && <Notice>{r.error}</Notice>}{r.statusMessage && <p className="protocol-message">{r.statusMessage}</p>}
        {r.truncated && <Notice>Ответ усечён.</Notice>}{log.bodyOmittedReason && <Notice>{log.bodyOmittedReason}</Notice>}
        {q.protocol === 'postgresql' ? r.rowsAffected !== undefined ? <p>Затронуто строк: {r.rowsAffected}</p> : <SQLTable columns={r.columns ?? []} rows={r.rows ?? []} /> : <>
          <h5>Заголовки / метаданные</h5><HeaderList values={r.headers} />{!!r.trailers?.length && <><h5>Trailers</h5><HeaderList values={r.trailers} /></>}
          {!log.bodyOmittedReason && <><h5>Тело</h5><Code text={r.body} json={/^[\s]*[\[{]/.test(r.body)} /></>}
        </>}
      </>}</section>
    </div>
    {s.redacted && <p className="muted small">Известные секреты и полученные значения скрыты.</p>}
    <details><summary>Связь с выполнением</summary><Code text={JSON.stringify({ id: s.id, attemptId: s.attemptId, probeId: s.probeId, runId: s.runId, testCaseId: s.testCaseId, blockId: s.blockId, variantId: s.variantId, environmentVersionId: s.environmentVersionId, resourceVersionId: s.resourceVersionId, position: s.position }, null, 2)} json /></details>
  </section>
}

export default function CallLogs({ target = {}, compact = false }: { target?: LogTarget; compact?: boolean }) {
  const initial = { attemptId: target.attemptId ?? '', probeId: target.probeId ?? '' }
  const [draft, setDraft] = useState<Record<string, string>>(initial), [filters, setFilters] = useState<Record<string, string>>(initial)
  const [items, setItems] = useState<LogSummary[]>([]), [cursor, setCursor] = useState(''), [next, setNext] = useState('')
  const [selected, setSelected] = useState(target.id ?? ''), [error, setError] = useState(''), [busy, setBusy] = useState(false), [version, setVersion] = useState(0)
  const [environments, setEnvironments] = useState<Environment[]>([]), [resources, setResources] = useState<Resource[]>([])
  const filterKey = JSON.stringify(filters)
  useEffect(() => { let active = true; void all<Environment>('/environments').then(values => { if (active) setEnvironments(values) }).catch(() => {}); return () => { active = false } }, [])
  useEffect(() => { let active = true; setResources([]); if (draft.environmentId) void all<Resource>(`/environments/${draft.environmentId}/resources`).then(values => { if (active) setResources(values) }).catch(() => {}); return () => { active = false } }, [draft.environmentId])
  useEffect(() => {
    if (target.id) return
    let active = true
    const query = new URLSearchParams({ limit: '50', ...Object.fromEntries(Object.entries(JSON.parse(filterKey) as Record<string, string>).filter(([, v]) => v)) })
    if (cursor) query.set('cursor', cursor)
    setBusy(true); setError('')
    void request<Page<LogSummary>>(`/call-logs?${query}`).then(page => { if (active) { setItems(old => cursor ? [...old, ...page.items] : page.items); setNext(page.nextCursor ?? '') } }).catch(e => { if (active) setError(message(e)) }).finally(() => { if (active) setBusy(false) })
    return () => { active = false }
  }, [filterKey, cursor, version, target.id])
  const field = (name: string, value: string) => setDraft(old => ({ ...old, [name]: value }))
  return <section className={`call-logs-page ${compact ? 'compact' : ''}`}>
    {!compact && <div className="page-heading"><div><p className="section-label">ЗАПРОСЫ И ОТВЕТЫ</p><h1>Журнал вызовов</h1></div></div>}
    {!target.id && <>
      <form className="log-filters" onSubmit={e => { e.preventDefault(); const value = { ...draft }; for (const key of ['from', 'to']) if (value[key]) value[key] = new Date(value[key]).toISOString(); setCursor(''); setFilters(value); setVersion(n => n + 1) }}>
        <div className="form-grid three">
          <Field label="Протокол"><select value={draft.protocol ?? ''} onChange={e => field('protocol', e.target.value)}><option value="">Все протоколы</option>{['http', 'grpc', 'postgresql'].map(p => <option key={p} value={p}>{protocolLabel[p]}</option>)}</select></Field>
          <Field label="Этап"><select value={draft.stage ?? ''} onChange={e => field('stage', e.target.value)}><option value="">Все этапы</option>{Object.entries(stages).map(([key, text]) => <option key={key} value={key}>{text}</option>)}</select></Field>
          <Field label="Состояние"><select value={draft.state ?? ''} onChange={e => field('state', e.target.value)}><option value="">Все состояния</option>{['queued', 'running', 'completed', 'error', 'timed_out', 'cancelled', 'interrupted'].map(p => <option key={p} value={p}>{statusLabel[p] ?? p}</option>)}</select></Field>
          <Field label="Окружение журнала"><select value={draft.environmentId ?? ''} onChange={e => setDraft(old => ({ ...old, environmentId: e.target.value, resourceId: '' }))}><option value="">Все окружения</option>{environments.map(env => <option key={env.id} value={env.id}>{env.name}</option>)}</select></Field>
          <Field label="Ресурс журнала"><select disabled={!draft.environmentId} value={draft.resourceId ?? ''} onChange={e => field('resourceId', e.target.value)}><option value="">Все ресурсы</option>{resources.map(res => <option key={res.id} value={res.id}>{res.name}</option>)}</select></Field>
          <Field label="Способ запуска"><select value={draft.origin ?? ''} onChange={e => field('origin', e.target.value)}><option value="">Все способы</option>{Object.entries(origins).map(([key, text]) => <option key={key} value={key}>{text}</option>)}</select></Field>
          <Field label="С начала периода"><input type="datetime-local" value={draft.from ?? ''} onChange={e => field('from', e.target.value)} /></Field>
          <Field label="До конца периода"><input type="datetime-local" value={draft.to ?? ''} onChange={e => field('to', e.target.value)} /></Field>
        </div>
        <details><summary>Связь с выполнением</summary><div className="form-grid">{[['testCaseId', 'ID тест-кейса'], ['runId', 'ID прогона'], ['attemptId', 'ID попытки'], ['probeId', 'ID проверки']].map(([key, label]) => <Field label={label} key={key}><input value={draft[key] ?? ''} onChange={e => field(key, e.target.value)} /></Field>)}</div></details>
        <div className="inline"><button type="submit" className="button secondary" disabled={busy}>Применить фильтры</button><button type="button" className="text-button" disabled={busy} onClick={() => { setCursor(''); setVersion(v => v + 1) }}>Обновить журнал</button></div>
      </form>
      {error && <Notice>{error}</Notice>}
      {busy && <p className="muted" role="status">Загрузка журнала…</p>}
      {!busy && !items.length && !error && <p className="muted">Вызовов по выбранным фильтрам нет.</p>}
      {!!items.length && <div className="table-scroll"><table className="call-log-table"><thead><tr><th>Время / запуск</th><th>Вызов</th><th>Окружение / ресурс</th><th>Результат</th></tr></thead><tbody>{items.map(s => <tr key={s.id}><td>{stamp(s.startedAt)}<small>{origins[s.origin]}</small></td><td><button type="button" className="text-button" onClick={() => setSelected(s.id)}>{protocolLabel[s.protocol]} · {stages[s.stage]}{s.sourceName && ` · ${s.sourceName}`}</button>{s.legacy && <small>Прежняя попытка · неполные сведения</small>}</td><td>{s.environmentName}<small>{s.resourceName}</small></td><td><span className={`status ${s.state}`}>{statusLabel[s.state] ?? s.state}</span><small>{s.durationMs} мс{s.persistencePending ? ' · запись не подтверждена' : ''}</small></td></tr>)}</tbody></table></div>}
      {next && <button type="button" className="button secondary" disabled={busy} onClick={() => setCursor(next)}>Ещё вызовы</button>}
    </>}
    {selected && <LogDetails key={selected} id={selected} />}
  </section>
}
