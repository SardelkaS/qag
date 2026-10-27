import { chromium, expect } from '@playwright/test'
import { readFile, unlink } from 'node:fs/promises'
const [base, runId] = process.argv.slice(2)
const browser = await chromium.launch()
try {
  const context = await browser.newContext({permissions: ['clipboard-read', 'clipboard-write']})
  const page = await context.newPage()
  let nativeWrites = 0
  page.on('request', r => { if (r.url().endsWith('/report/clipboard')) nativeWrites++ })
  await page.goto(base)
  await page.getByRole('button', {name: 'Открыть встроенный пример', exact: true}).click()
  await page.getByRole('button', {name: 'История', exact: true}).click()
  await page.getByRole('button', {name: 'Открыть', exact: true}).first().click()
  await page.getByRole('button', {name: 'Экспорт результата', exact: true}).click()
  const dialog = page.getByRole('dialog', {name: 'Экспорт результата', exact: true})
  const report = await (await page.request.get(base + '/api/v1/runs/' + runId + '/report')).json()
  if (report.clipboardNative !== false) throw new Error('Linux must advertise browser clipboard')
  const downloadEvent = page.waitForEvent('download')
  await dialog.getByRole('button', {name: 'Скачать PDF', exact: true}).click()
  const download = await downloadEvent
  const file = await download.path()
  const pdf = await readFile(file)
  if (pdf.length < 1000 || pdf.subarray(0,4).toString() !== '%PDF') throw new Error('Invalid downloaded PDF')
  for (const format of ['text', 'markdown', 'rich']) {
    await dialog.getByLabel('Формат экспорта').selectOption(format)
    await dialog.getByRole('button', {name: 'Скопировать', exact: true}).click()
    await expect(dialog.getByRole('status')).toContainText(format === 'rich' ? 'изображениями скопирован' : 'скопирован')
    const items = await page.evaluate(async () => {
      const out = []
      for (const item of await navigator.clipboard.read()) for (const type of item.types) out.push({type, text: await (await item.getType(type)).text()})
      return out
    })
    if (!items.some(x => x.type === 'text/plain' && x.text.length > 100)) throw new Error('Empty clipboard text')
    if (format === 'rich' && !items.some(x => x.type === 'text/html' && x.text.includes('data:image/png;base64,'))) throw new Error('Rich clipboard has no inline call images')
  }
  if (nativeWrites !== 0) throw new Error('Linux frontend incorrectly selected native Windows clipboard')
  await dialog.getByLabel('Включать ожидаемые результаты').uncheck()
  await dialog.getByLabel('Формат экспорта').selectOption('text')
  await dialog.getByRole('button', {name: 'Скопировать', exact: true}).click()
  await expect(dialog.getByRole('status')).toContainText('Текст скопирован')
  console.log('Chromium: UI, Linux capability, downloaded PDF, text, Markdown and HTML clipboard with PNG passed')
} finally { await browser.close() }
