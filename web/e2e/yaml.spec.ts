import { test, expect } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { resolve, join, sep } from 'node:path'
import { createServer } from 'node:net'
import { verifyHTTPSourceWorkflow } from './http-source-workflow'

let app: ChildProcess, directory = '', base = ''
const pause = (ms: number) => new Promise((r) => setTimeout(r, ms))
test.beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), 'qag-yaml-ui-'))
  const listener = createServer()
  await new Promise<void>((r) => listener.listen(0, '127.0.0.1', r))
  const address = listener.address() as { port: number }
  const port = address.port
  await new Promise<void>((r) => listener.close(() => r()))
  base = `http://127.0.0.1:${port}`
  await startYAMLApp()
})

async function startYAMLApp() {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('QAG_')))
  app = spawn(process.env.QAG_E2E_BINARY || resolve(import.meta.dirname, '../../.local/verify/qag-yaml.exe'), ['serve', '--data-file', join(directory, 'workspace.yaml'), '--address', new URL(base).host], {
    cwd: directory, windowsHide: true, env: { ...env, QAG_DEMO_GRPC_ADDRESS: '127.0.0.1:0' },
    stdio: 'ignore',
  })
  for (let i = 0; i < 100; i++) {
    if (await fetch(base + '/api/v1/health').then((r) => r.ok).catch(() => false)) return
    await pause(100)
  }
  throw new Error('YAML server did not become ready')
}

test('HTTP-источники окружения, полное сохранение и перезапуск YAML', async ({ page }) => {
  await verifyHTTPSourceWorkflow(page, base, async () => {
    if (app?.exitCode === null) { const closed = new Promise(resolve => app.once('close', resolve)); app.kill(); await closed }
    await startYAMLApp()
  })
})
test.afterAll(async () => {
  if (app?.exitCode === null) {
    const closed = new Promise((r) => app.once('close', r)); app.kill(); await closed
  }
  const path = resolve(directory)
  if (directory && path.startsWith(resolve(tmpdir()) + sep) && path.includes('qag-yaml-ui-')) await rm(path, { recursive: true, force: true })
})

test('YAML: сохранение, встроенный прогон, история и причина недоступности SQL', async ({ page }) => {
  await page.goto(base)
  await expect(page.locator('link[rel="icon"][type="image/x-icon"]')).toHaveAttribute('href', /favicon\.ico\?v=/)
  await page.locator('.sidebar').getByRole('button', { name: 'Создать папку', exact: true }).click()
  await page.getByLabel('Название', { exact: true }).fill('Проверки YAML')
  await page.getByRole('button', { name: 'Сохранить название', exact: true }).click()
  await page.getByRole('button', { name: 'Добавить тест-кейс в Проверки YAML', exact: true }).click()
  await page.getByLabel('Название', { exact: true }).fill('Кейс YAML')
  await page.getByRole('button', { name: 'Сохранить название', exact: true }).click()
  await page.getByRole('button', { name: '+ Добавить шаг', exact: true }).click()
  await page.locator('.step-card textarea').first().fill('Ручная проверка сохраняется в файл')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  await page.reload()
  await page.getByRole('button', { name: 'Папка Проверки YAML', exact: true }).click()
  await page.getByRole('button', { name: 'Открыть тест-кейс Кейс YAML', exact: true }).click()
  await expect(page.locator('.step-card textarea').first()).toHaveValue('Ручная проверка сохраняется в файл')
  await page.getByRole('button', { name: 'Сценарий для Jira', exact: true }).click()
  await page.getByRole('button', { name: /предпросмотр/ }).click()
  await expect(page.getByLabel('Jira wiki markup')).toHaveValue(/Ручная проверка сохраняется в файл/)
  await page.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click()
  await page.getByRole('button', { name: 'Открыть встроенный пример', exact: true }).click()
  await expect(page.getByLabel('Название тест-кейса')).toBeDisabled()
  await page.getByRole('button', { name: 'Выполнить всё', exact: true }).click()
  await expect(page.getByText(/Демонстрационная PostgreSQL недоступна в YAML-режиме/).first()).toBeVisible()
  const info = await (await fetch(base + '/api/v1/demo')).json()
  let run: any
  await expect.poll(async () => {
    const history = await (await fetch(base + `/api/v1/test-cases/${info.testCaseId}/runs?active=true`)).json()
    run = history.items[0]
    return run?.batches[0]?.state
  }).toBe('completed')
  expect(run.attempts).toHaveLength(5)
  expect(run.attempts.filter((a: any) => a.result?.errorCode === 'demo_sql_unavailable')).toHaveLength(1)
  await page.getByRole('button', { name: 'Пометить все пройденным', exact: true }).click()
  await page.getByRole('button', { name: 'Завершить прогон', exact: true }).click()
  await expect.poll(async () => (await (await fetch(base + `/api/v1/runs/${run.id}`)).json()).status).not.toBe('running')
  await page.getByRole('button', { name: /Окружения и ресурсы/ }).click()
  await page.getByRole('button', { name: /DEMO_SQL/ }).click()
  await expect(page.getByText(/В YAML-режиме демонстрационная PostgreSQL не настроена/)).toBeVisible()
})
