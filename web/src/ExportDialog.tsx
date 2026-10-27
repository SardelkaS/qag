import { useEffect, useState } from 'react'
import type { Report } from './api'
import { message, request } from './api'
import { Modal } from './ui'
import { callImages } from './reportImages'
import { markdown, plainText, richHTML, type PNGPart } from './reportText'

type Format = 'pdf' | 'text' | 'markdown' | 'rich'
type Prepared = {
  report: Report
  text: string
  html: string
  parts: PNGPart[]
}
export function ExportDialog({
  runId,
  onClose,
}: {
  runId: string
  onClose: () => void
}) {
  const [includeExpected, setExpected] = useState(true),
    [format, setFormat] = useState<Format>('pdf'),
    [prepared, setPrepared] = useState<Prepared | null>(null),
    [snapshot, setSnapshot] = useState<Report | null>(null),
    [fetching, setFetching] = useState(true),
    [preparing, setPreparing] = useState(true),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [notice, setNotice] = useState(''),
    [fallback, setFallback] = useState(''),
    [revision, setRevision] = useState(0)
  useEffect(() => {
    let live = true
    setFetching(true)
    setSnapshot(null)
    setError('')
    setNotice('')
    setFallback('')
    void request<Report>(
      `/runs/${runId}/report?includeExpected=${includeExpected}`,
    )
      .then((report) => {
        if (live) setSnapshot(report)
      })
      .catch((e) => {
        if (live) setError(message(e))
      })
      .finally(() => {
        if (live) setFetching(false)
      })
    return () => {
      live = false
    }
  }, [runId, includeExpected, revision])
  useEffect(() => {
    if (!snapshot) {
      setPrepared(null)
      setPreparing(false)
      return
    }
    let live = true
    const controller = new AbortController()
    setPreparing(true)
    setPrepared(null)
    setError('')
    setNotice('')
    setFallback('')
    void (async () => {
      try {
        const report = snapshot
        const parts =
          format === 'rich' ? await callImages(report, controller.signal) : []
        if (live)
          setPrepared({
            report,
            parts,
            text: format === 'markdown' ? markdown(report) : plainText(report),
            html: format === 'rich' ? richHTML(report, parts) : '',
          })
      } catch (e) {
        if (live) setError(message(e))
      } finally {
        if (live) setPreparing(false)
      }
    })()
    return () => {
      live = false
      controller.abort()
    }
  }, [snapshot, format])
  const publish = async () => {
    if (!prepared) return
    setBusy(true)
    setError('')
    setNotice('')
    try {
      if (format === 'pdf') {
        const { reportPDF, downloadPDF } = await import('./reportPdf')
        downloadPDF(await reportPDF(prepared.report), prepared.report)
        setNotice('PDF скачан')
      } else if (format === 'rich') {
        if (prepared.report.clipboardNative) {
          const payload = {
            snapshotId: prepared.report.snapshotId,
            hash: prepared.report.hash,
            includeExpected: prepared.report.includeExpected,
            parts: prepared.parts,
          }
          if (
            new TextEncoder().encode(JSON.stringify(payload)).length >
            32 * 1024 * 1024
          )
            throw new Error('Пакет изображений превышает 32 MiB')
          await request(`/runs/${runId}/report/clipboard`, 'POST', payload)
        } else {
          if (
            !navigator.clipboard?.write ||
            typeof ClipboardItem === 'undefined'
          )
            throw new Error(
              'Оформленное копирование недоступно в этом браузере. Используйте PDF или текст',
            )
          await navigator.clipboard.write([
            new ClipboardItem({
              'text/html': new Blob([prepared.html], { type: 'text/html' }),
              'text/plain': new Blob([prepared.text], { type: 'text/plain' }),
            }),
          ])
        }
        setNotice('Оформленный текст с изображениями скопирован')
      } else {
        try {
          await navigator.clipboard.writeText(prepared.text)
          setNotice(
            format === 'markdown' ? 'Markdown скопирован' : 'Текст скопирован',
          )
        } catch {
          setFallback(prepared.text)
          throw new Error(
            'Буфер обмена недоступен. Выделите текст ниже и скопируйте вручную',
          )
        }
      }
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title="Экспорт результата" onClose={onClose}>
      <p className="muted small">
        Экспорт включает сохранённые шаги, комментарии, все варианты и попытки.
        Несохранённый ввод не входит в отчёт.
      </p>
      <label className="field">
        <span>Формат экспорта</span>
        <select
          aria-label="Формат экспорта"
          value={format}
          disabled={busy}
          onChange={(e) => setFormat(e.target.value as Format)}
        >
          <option value="pdf">PDF-файл</option>
          <option value="text">Скопировать как текст</option>
          <option value="markdown">Скопировать как Markdown</option>
          <option value="rich">Текст с вызовами в виде картинок</option>
        </select>
      </label>
      <label className="export-toggle">
        <input
          type="checkbox"
          checked={includeExpected}
          disabled={busy}
          onChange={(e) => setExpected(e.target.checked)}
        />
        Включать ожидаемые результаты
      </label>
      {prepared?.report.partial && (
        <p className="export-partial">
          Частичный отчёт текущего прогона · новые результаты после подготовки в
          этот срез не входят.
        </p>
      )}
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {(preparing || fetching) && <p role="status">Подготовка отчёта…</p>}
      {fallback && (
        <textarea
          className="copy-fallback mono"
          aria-label="Текст для копирования"
          readOnly
          value={fallback}
          onFocus={(e) => e.target.select()}
        />
      )}
      <div className="action-bar">
        <button
          className="button secondary"
          disabled={busy || preparing || fetching}
          onClick={() => setRevision((v) => v + 1)}
        >
          Подготовить заново
        </button>
        <button
          className="button"
          disabled={busy || preparing || fetching || !prepared}
          onClick={() => void publish()}
        >
          {busy
            ? 'Подготовка…'
            : format === 'pdf'
              ? 'Скачать PDF'
              : 'Скопировать'}
        </button>
      </div>
    </Modal>
  )
}
