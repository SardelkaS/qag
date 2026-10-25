import { useEffect, useState } from 'react'
import type { Page, Run } from './api'
import { date, message, reasonLabel, request, statusLabel } from './api'
import { Modal, Notice } from './ui'
import RunView from './RunView'

export default function History({ caseId, version }: { caseId: string; version: number }) {
  const [retained, setRetained] = useState(true),
    [items, setItems] = useState<Run[]>([]),
    [cursor, setCursor] = useState(''),
    [selected, setSelected] = useState<Run | null>(null),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false),
    [refresh, setRefresh] = useState(0),
    [remove, setRemove] = useState<Run | null>(null)
  const load = async (next = '') => {
    setBusy(true)
    setError('')
    try {
      const page = await request<Page<Run>>(
        `/test-cases/${caseId}/runs?retained=${retained}&limit=20${next ? `&cursor=${encodeURIComponent(next)}` : ''}`,
      )
      setItems((old) => (next ? [...old, ...page.items] : page.items))
      setCursor(page.nextCursor ?? '')
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  useEffect(() => {
    void load()
  }, [caseId, retained, version, refresh])
  const save = async (run: Run) => {
    setBusy(true)
    try {
      const result = await request<Run>(`/runs/${run.id}/save`, 'POST')
      if (selected?.id === result.id) setSelected(result)
      setRefresh((v) => v + 1)
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="history-page">
      <div className="page-heading">
        <div>
          <h2>История прогонов</h2>
          <p className="muted small">Первый и первый успешный прогон сохраняются автоматически.</p>
        </div>
        <div className="segmented">
          <button className={retained ? 'selected' : ''} onClick={() => setRetained(true)}>
            Сохранённые
          </button>
          <button className={!retained ? 'selected' : ''} onClick={() => setRetained(false)}>
            Несохранённые
          </button>
        </div>
      </div>
      {error && <Notice onClose={() => setError('')}>{error}</Notice>}
      {items.length === 0 ? (
        <div className="empty-card">
          <h3>
            {busy
              ? 'Загрузка…'
              : retained
                ? 'Сохранённых прогонов пока нет'
                : 'Нет несохранённых прогонов'}
          </h3>
          <p className="muted">Здесь появятся результаты завершённых ручных проверок.</p>
        </div>
      ) : (
        <div className="table-scroll">
          <table className="history-table">
            <thead>
              <tr>
                <th>Завершён</th>
                <th>Результат</th>
                <th>Окружение</th>
                <th>Версия</th>
                <th>Сохранение</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {items.map((run) => (
                <tr key={run.id}>
                  <td>
                    <button className="text-button" onClick={() => setSelected(run)}>
                      {date(run.finishedAt ?? run.startedAt)}
                    </button>
                  </td>
                  <td>
                    <span className={`status ${run.status}`}>{statusLabel[run.status]}</span>
                  </td>
                  <td>{run.environmentName}</td>
                  <td>v{run.snapshot.revision}</td>
                  <td>
                    <div className="reason-list">
                      {run.retentionReasons.length ? (
                        run.retentionReasons.map((r) => (
                          <span className="badge" key={r}>
                            {reasonLabel[r]}
                          </span>
                        ))
                      ) : (
                        <span className="muted small">Не сохранён</span>
                      )}
                    </div>
                  </td>
                  <td>
                    <div className="inline">
                      <button className="text-button" onClick={() => setSelected(run)}>
                        Открыть
                      </button>
                      {!run.retentionReasons.length && (
                        <>
                          <button
                            className="text-button"
                            disabled={busy}
                            onClick={() => void save(run)}
                          >
                            Сохранить
                          </button>
                          <button
                            className="text-button danger"
                            disabled={busy}
                            onClick={() => setRemove(run)}
                          >
                            Удалить
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {cursor && (
        <button className="button secondary" disabled={busy} onClick={() => void load(cursor)}>
          Показать ещё
        </button>
      )}
      {selected && (
        <Modal title="Снимок прогона" onClose={() => setSelected(null)}>
          <RunView
            key={selected.id}
            run={selected}
            readOnly
            update={setSelected}
            onRefresh={() => setRefresh((v) => v + 1)}
          />
          {!selected.retentionReasons.length && (
            <div className="modal-actions">
              <button className="button" disabled={busy} onClick={() => void save(selected)}>
                Сохранить прогон
              </button>
            </div>
          )}
        </Modal>
      )}
      {remove && (
        <Modal title="Удалить несохранённый прогон?" onClose={() => setRemove(null)}>
          <p>
            Прогон от {date(remove.finishedAt ?? remove.startedAt)} будет удалён вместе с
            результатами. Сохранённые прогоны защищены от удаления.
          </p>
          <div className="modal-actions">
            <button className="button secondary" onClick={() => setRemove(null)}>
              Оставить
            </button>
            <button
              className="button danger-button"
              disabled={busy}
              onClick={() => {
                setBusy(true)
                void request(`/runs/${remove.id}`, 'DELETE')
                  .then(() => {
                    setRemove(null)
                    setRefresh((v) => v + 1)
                  })
                  .catch((e) => setError(message(e)))
                  .finally(() => setBusy(false))
              }}
            >
              Удалить прогон
            </button>
          </div>
        </Modal>
      )}
    </section>
  )
}
