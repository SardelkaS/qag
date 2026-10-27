import { PDFDocument, rgb, type PDFPage, type PDFFont } from 'pdf-lib'
import fontkit from '@pdf-lib/fontkit'
import type { Report } from './api'
import { bitmapImage, wrapText } from './reportImages'
import { valueText, type ReportNode } from './reportText'

const width = 595.28,
  height = 841.89,
  margin = 42,
  bottom = 42,
  bodyWidth = width - margin * 2
export async function reportPDF(report: Report): Promise<Uint8Array> {
  const pdf = await PDFDocument.create()
  pdf.registerFontkit(fontkit)
  pdf.setTitle(report.title)
  pdf.setAuthor('qag')
  pdf.setSubject(report.partial ? 'Частичный отчёт' : 'Результат прогона')
  const fonts = await Promise.all(
    ['NotoSans-Regular.ttf', 'NotoSansMono-Regular.ttf'].map(async (file) => {
      const response = await fetch('/fonts/' + file)
      if (!response.ok) throw new Error('Шрифт отчёта недоступен')
      return response.arrayBuffer()
    }),
  )
  const font = await pdf.embedFont(fonts[0], { subset: true }),
    mono = await pdf.embedFont(fonts[1], { subset: true })
  let page: PDFPage = pdf.addPage([width, height]),
    y = height - margin
  const nextPage = () => {
    page = pdf.addPage([width, height])
    y = height - margin
  }
  const ensure = (space: number) => {
    if (y - space < bottom) nextPage()
  }
  const lines = (
    text: string,
    font: PDFFont,
    size: number,
    available = bodyWidth,
  ) => wrapText(text, available, (s) => font.widthOfTextAtSize(s, size))
  const drawText = (
    text: string,
    size = 10,
    code = false,
    available = bodyWidth,
  ) => {
    const chosen = code ? mono : font
    const lineHeight = size * 1.5
    for (const line of lines(text, chosen, size, available)) {
      ensure(lineHeight)
      page.drawText(line, {
        x: margin,
        y: y - size,
        font: chosen,
        size,
        color: rgb(0.12, 0.18, 0.15),
      })
      y -= lineHeight
    }
    y -= 5
  }
  const status = (state: string, at: number) => {
    const x = width - margin - 8
    if (state === 'passed') {
      const color = rgb(0.15, 0.48, 0.23)
      page.drawLine({
        start: { x: x - 5, y: at },
        end: { x: x - 1, y: at - 4 },
        thickness: 1.7,
        color,
      })
      page.drawLine({
        start: { x: x - 1, y: at - 4 },
        end: { x: x + 6, y: at + 5 },
        thickness: 1.7,
        color,
      })
    } else if (state === 'failed') {
      const color = rgb(0.7, 0.17, 0.15)
      page.drawLine({
        start: { x: x - 4, y: at - 4 },
        end: { x: x + 4, y: at + 4 },
        thickness: 1.7,
        color,
      })
      page.drawLine({
        start: { x: x - 4, y: at + 4 },
        end: { x: x + 4, y: at - 4 },
        thickness: 1.7,
        color,
      })
    } else page.drawCircle({ x, y: at, size: 4, color: rgb(0.77, 0.8, 0.82) })
  }
  const drawTable = (node: ReportNode) => {
    const columns = node.columns || []
    if (!columns.length) return
    const colWidth = bodyWidth / columns.length,
      pad = 4,
      size = Math.min(8.5, columns.length > 10 ? 7 : 8.5),
      lineHeight = size * 1.45
    const header = columns.map((c) =>
      lines(c.name + ' (' + c.type + ')', mono, size, colWidth - pad * 2),
    )
    const headerHeight =
      Math.max(...header.map((v) => v.length)) * lineHeight + pad * 2
    if (headerHeight > height - margin - bottom - 60)
      throw new Error('Заголовок SQL-таблицы слишком велик для страницы PDF')
    const row = (
      cells: string[][],
      offset: number,
      count: number,
      headerRow = false,
    ) => {
      const cellHeight = count * lineHeight + pad * 2
      for (let column = 0; column < columns.length; column++) {
        page.drawRectangle({
          x: margin + column * colWidth,
          y: y - cellHeight,
          width: colWidth,
          height: cellHeight,
          borderColor: rgb(0.82, 0.85, 0.81),
          borderWidth: 0.5,
          ...(headerRow ? { color: rgb(0.94, 0.96, 0.93) } : {}),
        })
        for (let line = 0; line < count; line++) {
          const text = cells[column]?.[offset + line] || ''
          if (text)
            page.drawText(text, {
              x: margin + column * colWidth + pad,
              y: y - pad - size - line * lineHeight,
              size,
              font: mono,
              color: rgb(0.12, 0.18, 0.15),
            })
        }
      }
      y -= cellHeight
    }
    const repeatHeader = () => {
      ensure(headerHeight + 30)
      row(header, 0, Math.max(...header.map((v) => v.length)), true)
    }
    repeatHeader()
    for (const source of node.rows || []) {
      const cells = columns.map((_, index) =>
        lines(
          valueText(source[index] || { type: 'null' }),
          mono,
          size,
          colWidth - pad * 2,
        ),
      )
      const total = Math.max(...cells.map((v) => v.length))
      let offset = 0
      while (offset < total) {
        let capacity = Math.floor((y - bottom - pad * 2) / lineHeight)
        if (capacity < 1) {
          nextPage()
          repeatHeader()
          capacity = Math.floor((y - bottom - pad * 2) / lineHeight)
        }
        const count = Math.min(total - offset, capacity)
        row(cells, offset, count)
        offset += count
        if (offset < total) {
          nextPage()
          repeatHeader()
        }
      }
    }
    y -= 12
  }
  const imageCache = new Map<string, Awaited<ReturnType<typeof pdf.embedPng>>>()
  for (const node of report.document) {
    if (node.kind === 'heading') {
      const size = node.level === 1 ? 20 : node.level === 2 ? 14 : 11
      ensure(size * 3)
      if (node.status) status(node.status, y - size / 2)
      drawText(node.text, size, false, node.status ? bodyWidth - 28 : bodyWidth)
    } else if (node.kind === 'table') {
      drawTable(node)
    } else if (node.kind === 'image') {
      const source = report.images.find((i) => i.id === node.imageId)
      if (!source) throw new Error('Эталон отчёта недоступен')
      let image = imageCache.get(source.id)
      if (!image) {
        if (source.mime === 'image/jpeg') {
          image = await pdf.embedJpg(
            Uint8Array.from(atob(source.data), (ch) => ch.charCodeAt(0)),
          )
        } else if (source.mime === 'image/png') {
          image = await pdf.embedPng(source.data)
        } else {
          const bitmap = await bitmapImage(source, 2048, 2048)
          const canvas = document.createElement('canvas')
          canvas.width = bitmap.width
          canvas.height = bitmap.height
          canvas.getContext('2d')!.drawImage(bitmap, 0, 0)
          bitmap.close()
          image = await pdf.embedPng(canvas.toDataURL('image/png'))
        }
        imageCache.set(source.id, image)
      }
      const scale = Math.min(
        1,
        bodyWidth / image.width,
        (height - margin - bottom - 50) / image.height,
      )
      const w = image.width * scale,
        h = image.height * scale
      ensure(h + 30)
      page.drawImage(image, { x: margin, y: y - h, width: w, height: h })
      y -= h + 8
      if (node.text) drawText(node.text, 9)
    } else {
      drawText(node.text, node.kind === 'code' ? 8.5 : 10, node.kind === 'code')
    }
  }
  const pages = pdf.getPages()
  pages.forEach((page, index) =>
    page.drawText(`${index + 1} / ${pages.length}`, {
      x: width - margin - 45,
      y: 22,
      size: 8,
      font,
      color: rgb(0.4, 0.45, 0.4),
    }),
  )
  return pdf.save()
}

export function downloadPDF(bytes: Uint8Array, report: Report) {
  const blob = new Blob([new Uint8Array(bytes).buffer], {
    type: 'application/pdf',
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `qag-${report.runId}.pdf`
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 30_000)
}
