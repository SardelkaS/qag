import type { Report } from './api'
import {
  groupID,
  tableLines,
  type PNGPart,
  type ReportNode,
} from './reportText'

let fonts: Promise<void> | undefined
export function loadReportFonts() {
  return (fonts ??= Promise.all(
    [
      ['QAG Text', 'NotoSans-Regular.ttf'],
      ['QAG Mono', 'NotoSansMono-Regular.ttf'],
    ].map(async ([family, file]) => {
      const face = await new FontFace(family, `url(/fonts/${file})`).load()
      document.fonts.add(face)
    }),
  )
    .then(() => undefined)
    .catch((e) => {
      fonts = undefined
      throw e
    }))
}
export function wrapText(
  text: string,
  width: number,
  measure: (s: string) => number,
): string[] {
  const result: string[] = []
  for (const raw of text
    .replaceAll('\r\n', '\n')
    .replaceAll('\t', '    ')
    .split('\n')) {
    let line = ''
    for (const ch of raw) {
      if (line && measure(line + ch) > width) {
        result.push(line)
        line = ch
      } else {
        line += ch
      }
    }
    result.push(line)
  }
  return result
}
export async function bitmapImage(
  image: Report['images'][number],
  width = 1072,
  height = 1500,
) {
  const data = Uint8Array.from(atob(image.data), (ch) => ch.charCodeAt(0))
  const scale = Math.min(1, width / image.width, height / image.height)
  return createImageBitmap(new Blob([data], { type: image.mime }), {
    resizeWidth: Math.max(1, Math.round(image.width * scale)),
    resizeHeight: Math.max(1, Math.round(image.height * scale)),
    resizeQuality: 'high',
  })
}
type Line = { text: string; heading: boolean } | { image: ImageBitmap }
export async function callImages(
  report: Report,
  signal?: AbortSignal,
): Promise<PNGPart[]> {
  await loadReportFonts()
  const groups = new Map<string, ReportNode[]>()
  for (const n of report.document) {
    const id = groupID(n)
    if (id) groups.set(id, [...(groups.get(id) || []), n])
  }
  const parts: PNGPart[] = []
  let bytes = 0
  const measure = document.createElement('canvas').getContext('2d')!
  measure.font = '14px "QAG Mono"'
  const bitmaps = new Map<string, ImageBitmap>()
  try {
    for (const [id, nodes] of groups) {
      signal?.throwIfAborted()
      const lines: Line[] = []
      for (const node of nodes) {
        if (node.kind === 'image') {
          let bitmap = bitmaps.get(node.imageId!)
          if (!bitmap) {
            const source = report.images.find((i) => i.id === node.imageId)
            if (!source) throw new Error('Не удалось прочитать эталон')
            bitmap = await bitmapImage(source)
            bitmaps.set(source.id, bitmap)
          }
          lines.push(
            ...wrapText(
              node.text,
              1072,
              (s) => measure.measureText(s).width,
            ).map((text) => ({ text, heading: false })),
            { image: bitmap },
          )
        } else {
          const text =
            node.kind === 'table' ? tableLines(node).join('\n') : node.text
          measure.font =
            node.kind === 'heading' ? 'bold 14px "QAG Text"' : '14px "QAG Mono"'
          lines.push(
            ...wrapText(text, 1072, (s) => measure.measureText(s).width).map(
              (text) => ({ text, heading: node.kind === 'heading' }),
            ),
            { text: '', heading: false },
          )
        }
      }
      const chunks: Line[][] = [[]]
      let height = 0
      for (const line of lines) {
        const h = 'image' in line ? line.image.height + 16 : 21
        if (height + h > 1840 && chunks.at(-1)!.length) {
          chunks.push([])
          height = 0
        }
        chunks.at(-1)!.push(line)
        height += h
      }
      for (let index = 0; index < chunks.length; index++) {
        signal?.throwIfAborted()
        const chunk = chunks[index]
        const height =
          chunk.reduce(
            (sum, line) =>
              sum + ('image' in line ? line.image.height + 16 : 21),
            0,
          ) + 140
        const canvas = document.createElement('canvas')
        canvas.width = 1120
        canvas.height = height
        const ctx = canvas.getContext('2d')
        if (!ctx) throw new Error('Canvas недоступен')
        ctx.fillStyle = '#ffffff'
        ctx.fillRect(0, 0, 1120, height)
        ctx.fillStyle = '#20372e'
        ctx.font = 'bold 16px "QAG Text"'
        const title = nodes[0].text || 'Эталон'
        wrapText(title, 1072, (s) => ctx.measureText(s).width)
          .slice(0, 2)
          .forEach((line, i) => ctx.fillText(line, 24, 27 + i * 23))
        ctx.font = '12px "QAG Text"'
        ctx.fillStyle = '#65765f'
        ctx.fillText(`Часть ${index + 1}/${chunks.length}`, 24, 85)
        ctx.strokeStyle = '#d6dfd0'
        ctx.beginPath()
        ctx.moveTo(24, 96)
        ctx.lineTo(1096, 96)
        ctx.stroke()
        let y = 120
        for (const line of chunk) {
          if ('image' in line) {
            ctx.drawImage(line.image, 24, y)
            y += line.image.height + 16
          } else {
            ctx.fillStyle = '#20372e'
            ctx.font = line.heading ? 'bold 14px "QAG Text"' : '14px "QAG Mono"'
            ctx.fillText(line.text, 24, y + 14)
            y += 21
          }
        }
        const dataBase64 = canvas.toDataURL('image/png').split(',')[1]
        canvas.width = 1
        canvas.height = 1
        bytes += dataBase64.length + id.length + 100
        if (bytes > 31 * 1024 * 1024 || parts.length >= 512)
          throw new Error(
            'Изображения превышают лимит буфера 32 MiB. Выберите PDF или текст',
          )
        parts.push({
          groupId: id,
          part: index + 1,
          total: chunks.length,
          dataBase64,
        })
        if (parts.length % 8 === 0) await new Promise((r) => setTimeout(r, 0))
      }
    }
  } finally {
    for (const bitmap of bitmaps.values()) bitmap.close()
  }
  return parts
}
