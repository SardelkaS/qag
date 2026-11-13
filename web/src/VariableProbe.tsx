import { useEffect, useRef, useState } from 'react'
import type { HTTPHeaderSource, HTTPVariableProbe, ProbeInput, ProbeView } from './api'
import { message, request, statusLabel } from './api'
import { useCallLogs } from './CallLogContext'
import { Code, Notice } from './ui'

export type ProbeContext = {
  build: () => Promise<Omit<ProbeInput, 'source' | 'variableName' | 'idempotencyKey'>>
  version: string
  disabled: boolean
  failed: (error: string) => void
}

export default function VariableProbe({ name, source, context }: { name: string; source: HTTPHeaderSource; context: ProbeContext }) {
  const openLogs = useCallLogs()
  const [view, setView] = useState<ProbeView | null>(null), [error, setError] = useState(''), [busy, setBusy] = useState(false), [activeId, setActiveId] = useState('')
  const generation = useRef(0), retry = useRef<{ fingerprint: string; key: string } | null>(null), alive = useRef(true)
  const fingerprint = JSON.stringify({ name, source, context: context.version })
  const latest = useRef(context); latest.current = context
  useEffect(() => { alive.current = true; return () => { alive.current = false; generation.current++ } }, [])
  useEffect(() => { generation.current++; setView(null); setError(''); setBusy(false); setActiveId(''); retry.current = null }, [fingerprint])
  useEffect(() => {
    if (!view?.valueAvailable) return
    const expires = new Date(view.probe.finishedAt ?? view.probe.startedAt).getTime() + 10 * 60_000
    const timer = setTimeout(() => setView(old => old ? { ...old, valueAvailable: false, extractedValue: undefined, formattedValue: undefined, valueUnavailable: 'Время показа значения истекло. Выполните новую проверку при необходимости.' } : null), Math.max(0, expires - Date.now()))
    return () => clearTimeout(timer)
  }, [view?.probe.id, view?.valueAvailable])
  useEffect(() => {
    if (!activeId) return
    let current = true, timer: ReturnType<typeof setTimeout> | undefined
    const sequence = generation.current
    const load = async () => {
      try {
        const value = await request<ProbeView>(`/http-variable-probes/${activeId}`)
        if (!current || sequence !== generation.current) return
        setView(value); setError('')
        if (['queued', 'running'].includes(value.probe.state) || value.probe.persistencePending) timer = setTimeout(() => void load(), 400)
        else { setBusy(false); setActiveId(''); if (value.probe.error) latest.current.failed(value.probe.error) }
      } catch (e) { if (current && sequence === generation.current) { const text = message(e); setError(text); latest.current.failed(text); timer = setTimeout(() => void load(), 1500) } }
    }
    void load(); return () => { current = false; clearTimeout(timer) }
  }, [activeId])
  const start = async () => {
    if (busy || context.disabled) return
    const sequence = generation.current
    setBusy(true); setView(null); setError('')
    try {
      const input = await context.build()
      if (!alive.current || sequence !== generation.current) return
      const key = retry.current?.fingerprint === fingerprint ? retry.current.key : crypto.randomUUID()
      retry.current = { fingerprint, key }
      const probe = await request<HTTPVariableProbe>('/http-variable-probes', 'POST', { ...input, source, variableName: name, idempotencyKey: key }, key)
      if (!alive.current || sequence !== generation.current) return
      retry.current = null; setActiveId(probe.id)
    } catch (e) { if (alive.current && sequence === generation.current) { const text = message(e); setError(text); context.failed(text); setBusy(false) } }
  }
  const cancel = async () => {
    try { await request(`/http-variable-probes/${activeId}/cancel`, 'POST') }
    catch (e) { const text = message(e); setError(text); context.failed(text) }
  }
  return <section className="variable-probe" aria-label={`Проверка переменной ${name || 'без имени'}`}>
    <div className="inline"><button type="button" className="button secondary" disabled={busy || context.disabled} onClick={() => void start()}>Проверить получение</button>
      {busy && <span className="muted small" role="status">Получение значения…</span>}
      {activeId && <button type="button" className="text-button danger" onClick={() => void cancel()}>Отменить проверку</button>}
      {view?.probe.logId && openLogs && <button type="button" className="text-button" onClick={() => openLogs({ id: view.probe.logId })}>Запрос и ответ проверки</button>}
    </div>
    {error && <Notice>{error}</Notice>}
    {view && <div className="probe-result"><span className={`status ${view.probe.state}`}>{statusLabel[view.probe.state] ?? view.probe.state}</span><span className="muted small"> {view.probe.statusCode && `HTTP ${view.probe.statusCode} · `}{view.probe.durationMs} мс</span>
      {view.probe.error && <Notice>{view.probe.error}</Notice>}
      {view.valueAvailable && <><h5>Извлечённое значение · {view.valueType}</h5><Code text={view.extractedValue === '' ? '""' : view.extractedValue ?? ''} /><h5>Значение с текстом до и после</h5><Code text={view.formattedValue === '' ? '""' : view.formattedValue ?? ''} /><p className="muted small">Показано временно. В журнале и сохранённых данных значение скрыто.</p></>}
      {view.valueUnavailable && <p className="muted small">{view.valueUnavailable}</p>}
    </div>}
  </section>
}
