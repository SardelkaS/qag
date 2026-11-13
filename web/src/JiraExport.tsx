import { useState } from 'react'
import { message, request, type CaseExport, type TestCase } from './api'
import { Modal, Notice } from './ui'

export default function JiraExport({ saved, dirty, save, close }: {
  saved: TestCase
  dirty: boolean
  save: () => Promise<TestCase | undefined>
  close: () => void
}) {
  const [includeExpected, setIncludeExpected] = useState(true)
  const [text, setText] = useState(''), [error, setError] = useState(''), [copied, setCopied] = useState(false), [busy, setBusy] = useState(false)
  const preview = async (saveFirst: boolean) => {
    setBusy(true); setError(''); setCopied(false); setText('')
    try {
      const c = saveFirst ? await save() : saved
      if (!c) return
      const result = await request<CaseExport>(`/test-cases/${c.id}/export?revision=${c.revision}&includeExpected=${includeExpected}&format=jira-wiki`)
      setText(result.text)
    } catch (e) { setError(message(e)) }
    finally { setBusy(false) }
  }
  return <Modal title="Сценарий для Jira" onClose={close}>
    <p>Jira wiki markup: только определение сценария. Изображения нужно приложить к комментарию вручную.</p>
    {dirty && <Notice>Есть несохранённые изменения. Выберите сохранённую версию или сначала сохраните правки.</Notice>}
    {error && <Notice>{error}</Notice>}
    <label className="inline"><input type="checkbox" disabled={busy} checked={includeExpected} onChange={e => { setIncludeExpected(e.target.checked); setText(''); setCopied(false) }} />Включить ожидаемые результаты</label>
    <div className="modal-actions">
      <button className="button secondary" disabled={busy} onClick={() => void preview(false)}>Версия {saved.revision}: предпросмотр</button>
      {dirty && <button className="button" disabled={busy} onClick={() => void preview(true)}>Сохранить и подготовить</button>}
    </div>
    {text && <>
      <textarea className="mono" aria-label="Jira wiki markup" rows={18} readOnly value={text} onFocus={e => e.target.select()} />
      <div className="modal-actions"><button className="button" disabled={busy} onClick={async () => {
        setBusy(true); setError(''); setCopied(false)
        try { await navigator.clipboard.writeText(text); setCopied(true) }
        catch { setError('Буфер обмена недоступен. Выделите и скопируйте текст из поля вручную.') }
        finally { setBusy(false) }
      }}>Скопировать для Jira</button></div>
      {copied && <p role="status">Сценарий скопирован</p>}
    </>}
  </Modal>
}
