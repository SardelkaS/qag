import type { components } from './generated/api'
import type { Report, TypedValue } from './api'
import { manualLabels } from './StepStatus'
export type ReportNode = components['schemas']['ReportNode']
export type PNGPart = components['schemas']['PNGPart']
export const valueText = (v: TypedValue) =>
  v.type === 'null'
    ? 'NULL'
    : v.type === 'string' && !v.value
      ? '""'
      : (v.value ?? '')
export function tableLines(n: ReportNode): string[] {
  return [
    (n.columns || []).map((c) => `${c.name} (${c.type})`).join(' | '),
    ...(n.rows || []).map((row) => row.map(valueText).join(' | ')),
  ]
}
export const statusText = (status: string) =>
  (manualLabels as Record<string, string>)[status] || 'Не проверен'
export const nodeText = (n: ReportNode) =>
  n.kind === 'table'
    ? tableLines(n).join('\n')
    : n.kind === 'image'
      ? `[Эталон: ${n.text}; ${n.imageId}]`
      : n.text + (n.status ? ` — ${statusText(n.status)}` : '')
export const plainText = (r: Report) =>
  r.document.map(nodeText).join('\n\n').trim()
const escapeMD = (s: string) => s.replace(/[\\`*_{}\[\]()#+\-.!|<>]/g, '\\$&')
function fenced(s: string) {
  const runs = s.match(/`+/g) || []
  const fence = '`'.repeat(Math.max(3, ...runs.map((v) => v.length + 1)))
  return `${fence}\n${s}\n${fence}`
}
export function markdown(r: Report) {
  return r.document
    .map((n) => {
      if (n.kind === 'code' || n.kind === 'table') return fenced(nodeText(n))
      if (n.kind === 'image') {
        const image = r.images.find((i) => i.id === n.imageId)
        if (!image) throw new Error('Эталон недоступен')
        return `![${escapeMD(n.text || 'Эталон')}](data:${image.mime};base64,${image.data})`
      }
      if (n.kind === 'heading')
        return (
          '#'.repeat(Math.min(n.level || 2, 6)) +
          ' ' +
          escapeMD(nodeText(n)).replaceAll('\n', ' ')
        )
      return escapeMD(n.text)
    })
    .join('\n\n')
}
export const escapeHTML = (s: string) =>
  s.replace(
    /[&<>"']/g,
    (ch) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[
        ch
      ]!,
  )
export function groupID(n: ReportNode) {
  return n.callId || (n.kind === 'image' ? `image:${n.id}` : '')
}
export function richHTML(r: Report, parts: PNGPart[]) {
  const seen = new Set<string>()
  return (
    '<html><body style="font-family:Arial,sans-serif;color:#20372e;background:#fff">' +
    r.document
      .map((n) => {
        const group = groupID(n)
        if (group) {
          if (seen.has(group)) return ''
          seen.add(group)
          return (
            `<h3>${escapeHTML(n.text)}</h3>` +
            parts
              .filter((p) => p.groupId === group)
              .map(
                (p) =>
                  `<p>Часть ${p.part}/${p.total}</p><img alt="Часть ${p.part}/${p.total}" style="max-width:100%;height:auto" src="data:image/png;base64,${p.dataBase64}" />`,
              )
              .join('')
          )
        }
        if (n.kind === 'heading')
          return `<table style="width:100%"><tr><td><h2>${escapeHTML(n.text)}</h2></td><td style="text-align:right">${n.status ? escapeHTML(statusText(n.status)) : ''}</td></tr></table>`
        return `<${n.kind === 'code' || n.kind === 'table' ? 'pre' : 'p'} style="white-space:pre-wrap">${escapeHTML(nodeText(n))}</${n.kind === 'code' || n.kind === 'table' ? 'pre' : 'p'}>`
      })
      .join('') +
    '</body></html>'
  )
}
