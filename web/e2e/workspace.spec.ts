import { test, expect, chromium, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { readFile, access } from 'node:fs/promises'
import { createWriteStream } from 'node:fs'
import { resolve } from 'node:path'
import { randomUUID } from 'node:crypto'
import { createServer } from 'node:net'

const root = resolve(import.meta.dirname, '../..'),
  base = 'http://127.0.0.1:18080'
let fixture: ChildProcess,
  app: ChildProcess,
  config: any,
  csrf = '',
  logs = ''
const pause = (ms: number) => new Promise((r) => setTimeout(r, ms))
async function eventually(action: () => Promise<boolean>) {
  for (let n = 0; n < 100; n++) {
    if (await action().catch(() => false)) return
    await pause(200)
  }
  throw new Error('Локальный стенд не готов')
}
async function startApp() {
  app = spawn(
    process.env.QAG_E2E_BINARY || resolve(root, '.local/verify/qag.exe'),
    [],
    {
      cwd: root,
      windowsHide: true,
      env: {
        ...process.env,
        QAG_ADDRESS: '127.0.0.1:18080',
        QAG_DATABASE_URL: config.databaseUrl,
        QAG_TEST_MODE: '1',
        QAG_SCHEMA: config.appSchema,
        QAG_DEMO_SCHEMA: config.demoSchema,
        QAG_DEMO_DATABASE_URL: config.demoDatabaseUrl,
        QAG_DEMO_GRPC_ADDRESS: '127.0.0.1:0',
      },
    },
  )
  app.stdout?.on('data', (v) => {
    logs += String(v)
  })
  app.stderr?.on('data', (v) => {
    logs += String(v)
  })
  await eventually(async () => (await fetch(base + '/api/v1/health')).ok)
  csrf = (await (await fetch(base + '/api/v1/session')).json()).csrfToken
}
async function stopApp() {
  if (app?.exitCode === null) {
    const closed = new Promise((r) => app.once('close', r))
    app.kill()
    await closed
  }
}
async function api(path: string, method = 'GET', body?: unknown, key?: string) {
  const r = await fetch(base + '/api/v1' + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      Origin: base,
      'X-QAG-CSRF': csrf,
      ...(key ? { 'Idempotency-Key': key } : {}),
    },
    body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
  })
  const value = await r.json()
  if (!r.ok) throw new Error(`${r.status}: ${value.message}`)
  return value
}
test.beforeAll(async () => {
  const path = resolve(root, '.local/e2e.json')
  fixture = spawn(resolve(root, 'dist/testfixture.exe'), ['-config', path], {
    cwd: root,
    windowsHide: true,
    env: process.env,
  })
  fixture.stderr?.pipe(
    createWriteStream(resolve(root, '.local/e2e-fixture.log')),
  )
  await eventually(async () => {
    await access(path)
    return true
  })
  config = JSON.parse(await readFile(path, 'utf8'))
  await startApp()
})
test.afterAll(async () => {
  await stopApp()
  if (config) await fetch(config.shutdown, { method: 'POST' }).catch(() => {})
  if (fixture?.exitCode === null)
    await Promise.race([
      new Promise((r) => fixture.once('close', r)),
      pause(5000),
    ])
  if (fixture?.exitCode === null) fixture.kill()
})

async function addResource(page: Page, name: string, kind: string) {
  await page
    .locator('.resource-list')
    .getByRole('button', { name: '+ Добавить', exact: true })
    .click()
  const form = page.locator('.resource-form')
  await form.getByLabel('Имя ресурса', { exact: true }).fill(name)
  await form.getByLabel('Тип ресурса', { exact: true }).selectOption(kind)
  await form
    .getByLabel('Защита подключения', { exact: true })
    .selectOption('plaintext')
  if (kind === 'http')
    await form
      .getByLabel('Базовый URL', { exact: false })
      .fill(config.http + '/api')
  if (kind === 'grpc')
    await form.getByLabel('Адрес сервера', { exact: true }).fill(config.grpc)
  if (kind === 'postgresql') {
    await form
      .getByLabel('Адрес сервера', { exact: true })
      .fill(config.target.address)
    await form
      .getByLabel('База данных', { exact: true })
      .fill(config.target.database)
    await form
      .getByLabel('Пользователь PostgreSQL', { exact: true })
      .fill(config.target.username)
    await form
      .getByLabel('Действие: PASSWORD', { exact: true })
      .selectOption('replace')
    await form
      .getByLabel('Новое значение: PASSWORD', { exact: false })
      .fill(config.target.password)
  }
  await form
    .getByRole('button', { name: 'Сохранить ресурс', exact: true })
    .click()
  await expect(
    page.locator('.resource-item').filter({ hasText: name }),
  ).toBeVisible()
}
async function executeAll(page: Page) {
  const calls = page.locator('.run-view .call-block')
  for (let i = 0; i < 3; i++) {
    await calls
      .nth(i)
      .getByRole('button', { name: 'Запустить вызов', exact: true })
      .click()
    await expect(
      calls.nth(i).getByText('Совпадает', { exact: true }).first(),
    ).toBeVisible()
  }
}

test('встроенный пример: просмотр, три протокола, история и перезапуск', async ({
  page,
}) => {
  const info = await api('/demo'),
    c = await api(`/test-cases/${info.testCaseId}`)
  expect(c.readOnly).toBe(true)
  expect(c.steps).toHaveLength(5)
  await page.goto('/')
  const pinned = page.getByRole('button', {
    name: 'Открыть встроенный пример',
    exact: true,
  })
  await pinned.waitFor()

  await pinned.click()
  await expect(
    page.getByRole('heading', { name: c.name, exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText('Встроенный пример · только просмотр', { exact: true }),
  ).toBeVisible()
  await expect(page.getByLabel('Название тест-кейса')).toBeDisabled()
  await expect(
    page.getByRole('button', { name: '+ Добавить шаг', exact: true }),
  ).toBeDisabled()
  await expect(page.getByLabel('Текущее окружение')).toBeDisabled()
  await expect(page.getByLabel('Текущее окружение')).toHaveValue(
    info.environmentId,
  )
  await expect(page.locator('.folder-tree')).not.toContainText(
    'Системные примеры',
  )
  // Read-only is enforced by the API for every managed object.
  await expect(
    api(`/test-cases/${c.id}`, 'PUT', {
      name: c.name,
      revision: c.revision,
      steps: c.steps,
    }),
  ).rejects.toThrow('409')
  await expect(
    api(`/test-cases/${c.id}`, 'PATCH', { archived: true }),
  ).rejects.toThrow('409')
  await expect(
    api(`/folders/${c.folderId}`, 'PATCH', { name: 'Изменено' }),
  ).rejects.toThrow('409')
  await expect(
    api(`/folders/${c.folderId}/test-cases`, 'POST', { name: 'Изменено' }),
  ).rejects.toThrow('409')
  const env = (await api('/environments')).items.find(
    (e: any) => e.id === info.environmentId,
  )
  await expect(
    api(`/environments/${env.id}`, 'PUT', {
      name: env.name,
      description: env.description,
      revision: env.revision,
    }),
  ).rejects.toThrow('409')
  const resources = (await api(`/environments/${env.id}/resources`)).items
  for (const resource of resources) {
    await expect(
      api(`/resources/${resource.id}`, 'PUT', {
        name: resource.name,
        kind: resource.kind,
        revision: resource.revision,
        config: resource.config,
        secrets: { replace: {}, clear: [] },
      }),
    ).rejects.toThrow('409')
  }
  await expect(
    api(`/resources/${info.grpcResourceId}/proto-bundles`, 'POST', {
      files: {},
      revision: resources.find((r: any) => r.id === info.grpcResourceId)
        .revision,
    }),
  ).rejects.toThrow('409')
  await expect(
    api(
      `/test-cases/${c.id}/blocks/${c.steps[0].blocks[0].id}/attempts`,
      'POST',
      { environmentId: randomUUID(), revision: c.revision },
      randomUUID(),
    ),
  ).rejects.toThrow('400')
  const httpConfig = resources.find((r: any) => r.kind === 'http').config
  await expect(
    api(`/environments/${env.id}/resources`, 'POST', {
      name: 'EXTRA',
      kind: 'http',
      config: httpConfig,
      secrets: { replace: {}, clear: [] },
    }),
  ).rejects.toThrow('409')
  const editorCalls = page.locator('.editor .call-block')
  await expect(editorCalls).toHaveCount(5)
  await editorCalls
    .first()
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    editorCalls.first().getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await api(`/test-cases/${c.id}/runs`)).items).toHaveLength(0)
  for (let cycle = 0; cycle < 2; cycle++) {
    if (cycle)
      await page
        .getByRole('button', { name: 'Редактор 5', exact: true })
        .click()
    await page
      .getByRole('button', { name: 'Начать прогон', exact: true })
      .click()
    await expect(page.locator('.run-view .readonly-notice')).toContainText('Сценарий и его системное окружение нельзя редактировать')
    const calls = page.locator('.run-view .call-block')
    for (let n = 0; n < 5; n++) {
      await calls
        .nth(n)
        .getByRole('button', { name: 'Запустить вызов', exact: true })
        .click()
      await expect(
        calls.nth(n).getByText('Совпадает', { exact: true }).first(),
      ).toBeVisible()
    }
    for (let n = 1; n <= 5; n++) {
      await page
        .getByLabel(`Статус шага ${n}`, { exact: true })
        .selectOption('passed')
      await expect(
        page.getByLabel(`Статус шага ${n}`, { exact: true }),
      ).toBeEnabled()
    }
    await page
      .getByLabel('Комментарий к шагу 5', { exact: true })
      .fill('Проверены три интерфейса')
    await page
      .getByRole('button', { name: 'Сохранить комментарий', exact: true })
      .click()
    await page
      .getByRole('button', { name: 'Завершить прогон', exact: true })
      .click()
    await expect(page.locator('.run-view > .page-heading .status')).toHaveText(
      'Пройден',
    )
  }
  const history = (await api(`/test-cases/${c.id}/runs`)).items
  expect(history).toHaveLength(2)
  expect(history[1].retentionReasons.sort()).toEqual([
    'first_run',
    'first_success',
  ])
  expect(history[0].retentionReasons).toEqual([])
  await stopApp()
  const occupied = createServer()
  await new Promise<void>((r) => occupied.listen(0, '127.0.0.1', r))
  const occupiedAddress = occupied.address() as { port: number }
  const rejected = spawn(
    process.env.QAG_E2E_BINARY || resolve(root, '.local/verify/qag.exe'),
    [],
    {
      cwd: root,
      windowsHide: true,
      env: {
        ...process.env,
        QAG_ADDRESS: '127.0.0.1:18080',
        QAG_DATABASE_URL: config.databaseUrl,
        QAG_TEST_MODE: '1',
        QAG_SCHEMA: config.appSchema,
        QAG_DEMO_SCHEMA: config.demoSchema,
        QAG_DEMO_DATABASE_URL: config.demoDatabaseUrl,
        QAG_DEMO_GRPC_ADDRESS: `127.0.0.1:${occupiedAddress.port}`,
      },
    },
  )
  let rejectedLog = ''
  rejected.stderr?.on('data', (v) => (rejectedLog += String(v)))
  const exitCode = await new Promise<number | null>((r) =>
    rejected.once('close', r),
  )
  await new Promise<void>((r) => occupied.close(() => r()))
  expect(exitCode).toBe(1)
  expect(rejectedLog).toContain('gRPC-порт недоступен')
  expect(rejectedLog).not.toContain(new URL(config.demoDatabaseUrl).password)
  await startApp()
  await page.reload()
  expect(await api('/demo')).toEqual(info)
  const old = await api(`/runs/${history[1].id}`)
  expect(old.stepStates[4].comment).toBe('Проверены три интерфейса')
  expect(old.attempts.map((a: any) => a.result.comparison.status)).toEqual([
    'match',
    'match',
    'match',
    'match',
    'match',
  ])
  await page.getByRole('button', { name: 'Архив', exact: true }).click()
  await expect(pinned).toBeVisible()
  await expect(
    page.getByRole('button', { name: 'Начать прогон', exact: true }),
  ).toBeEnabled()
  await page
    .getByRole('button', { name: 'Окружения и ресурсы', exact: false })
    .click()
  await expect(
    page.getByRole('button', { name: 'Настроить окружение', exact: true }),
  ).toBeDisabled()
  await page.locator('.resource-item').filter({ hasText: 'DEMO_SQL' }).click()
  await expect(page.getByLabel('Адрес сервера', { exact: true })).toBeDisabled()
  await expect(
    page.getByLabel('Действие: PASSWORD', { exact: true }),
  ).toBeDisabled()
  await pinned.click()
  await page.screenshot({
    path: resolve(root, '.local/builtin-demo.png'),
    fullPage: true,
  })
})

test('каталог → три протокола → история → архив → перезапуск', async ({
  page,
}) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Окружения и ресурсы', exact: false })
    .click()
  await page
    .getByRole('button', { name: 'Создать окружение', exact: true })
    .click()
  await page.getByLabel('Имя окружения', { exact: true }).fill('dev')
  await page
    .getByRole('button', { name: 'Сохранить окружение', exact: true })
    .click()
  for (const [name, kind] of [
    ['AI_GW_SERVISE', 'http'],
    ['HEALTH', 'grpc'],
    ['TARGET', 'postgresql'],
  ])
    await addResource(page, name, kind)
  await page
    .getByRole('button', { name: 'Тест-кейсы', exact: false })
    .first()
    .click()
  await page
    .locator('aside')
    .getByRole('button', { name: 'Создать папку', exact: true })
    .click()
  await page.getByLabel('Название', { exact: true }).fill('Проверки API')
  await page
    .getByRole('button', { name: 'Сохранить название', exact: true })
    .click()
  await page
    .getByRole('button', {
      name: 'Добавить тест-кейс в Проверки API',
      exact: true,
    })
    .click()
  await page.getByLabel('Название', { exact: true }).fill('Смешанный сценарий')
  await page
    .getByRole('button', { name: 'Сохранить название', exact: true })
    .click()
  await page
    .getByRole('button', { name: '+ Добавить шаг', exact: true })
    .click()
  await page
    .getByLabel('Описание шага 1', { exact: true })
    .fill('Проверить ответы и оценить результат вручную')
  for (const kind of ['HTTP', 'gRPC', 'PostgreSQL'])
    await page.getByRole('button', { name: '+ ' + kind, exact: true }).click()
  const blocks = page.locator('.editor .call-block')
  await blocks
    .nth(0)
    .getByLabel('Путь или ссылка на ресурс', { exact: false })
    .fill('AI_GW_SERVISE/task')
  await blocks
    .nth(0)
    .getByLabel('Ожидаемое тело', { exact: true })
    .fill('{"ok":true,"number":9007199254740993123,"token":""}')
  await blocks
    .nth(1)
    .getByLabel('Ресурс', { exact: true })
    .selectOption('HEALTH')
  await blocks
    .nth(1)
    .getByLabel('Сервис gRPC', { exact: true })
    .fill('grpc.health.v1.Health')
  await blocks.nth(1).getByLabel('Метод gRPC', { exact: true }).fill('Check')
  await blocks
    .nth(1)
    .getByLabel('Ожидаемое тело', { exact: true })
    .fill('{"status":"SERVING"}')
  const sql = blocks.nth(2)
  await sql.getByLabel('Ресурс', { exact: true }).selectOption('TARGET')
  await sql
    .getByLabel('SQL-запрос', { exact: false })
    .fill('SELECT $1::numeric AS amount')
  await sql.getByRole('button', { name: '+ Параметр', exact: true }).click()
  await sql
    .getByLabel('Параметр $1: тип', { exact: true })
    .selectOption('decimal')
  await sql
    .getByLabel('Параметр $1', { exact: true })
    .fill('9007199254740993123.125')
  await sql.getByRole('button', { name: '+ Колонка', exact: true }).click()
  await sql.getByLabel('Ожидаемая колонка 1', { exact: true }).fill('amount')
  await sql
    .getByLabel('PostgreSQL-тип колонки 1', { exact: true })
    .fill('numeric')
  await sql
    .getByRole('button', { name: '+ Ожидаемая строка', exact: true })
    .click()
  await sql
    .getByLabel('Строка 1, колонка 1: тип', { exact: true })
    .selectOption('decimal')
  await sql
    .getByLabel('Строка 1, колонка 1', { exact: true })
    .fill('9007199254740993123.125')
  await sql.getByLabel('Параметр $1', { exact: true }).fill('wrong')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(
    page.getByRole('region', { name: 'Готовность к запуску' }),
  ).toBeVisible()
  await expect(sql.getByLabel('Параметр $1', { exact: true })).toHaveValue(
    'wrong',
  )
  await sql
    .getByLabel('Параметр $1', { exact: true })
    .fill('9007199254740993123.125')
  await blocks
    .nth(0)
    .getByLabel('Ожидаемое тело', { exact: true })
    .fill('{broken')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(
    page.getByRole('region', { name: 'Готовность к запуску' }),
  ).toBeVisible()
  await page.reload()
  await expect(
    blocks.nth(0).getByLabel('Ожидаемое тело', { exact: true }),
  ).toHaveValue('{broken')
  await blocks
    .nth(0)
    .getByLabel('Ожидаемое тело', { exact: true })
    .fill('{"ok":true,"number":9007199254740993123,"token":""}')
  // Navigating with unsaved input offers a real choice and preserves the draft.
  await page
    .getByRole('button', { name: 'Окружения и ресурсы', exact: false })
    .click()
  await expect(
    page.getByRole('dialog', { name: 'Несохранённые изменения' }),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Остаться', exact: true }).click()
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  const folders = await api('/folders'),
    folder = folders.items[0],
    cases = await api(`/folders/${folder.id}/test-cases`),
    c = cases.items[0]
  expect(c.steps[0].blocks.map((b: any) => b.kind)).toEqual([
    'http',
    'grpc',
    'postgresql',
  ])
  // Editor trials create attempts without creating a run.
  await blocks
    .nth(0)
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    blocks.nth(0).getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await api(`/test-cases/${c.id}/runs`)).items).toHaveLength(0)
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await executeAll(page)
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('failed')
  await page
    .getByRole('button', { name: 'Завершить прогон', exact: true })
    .click()
  await expect(page.getByText('Первый прогон', { exact: true })).toBeVisible()
  const first = (await api(`/test-cases/${c.id}/runs`)).items[0]
  expect(first.status).toBe('failed')
  expect(first.retentionReasons).toEqual(['first_run'])
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await executeAll(page)
  await page
    .getByLabel('Комментарий к шагу 1', { exact: true })
    .fill('Ответы проверены')
  await page
    .getByRole('button', { name: 'Сохранить комментарий', exact: true })
    .click()
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('passed')
  await expect(page.getByLabel('Статус шага 1', { exact: true })).toBeEnabled()
  await page.evaluate(() => sessionStorage.clear())
  await page.reload()
  await page.getByRole('button', { name: 'Прогон', exact: true }).click()
  await expect(
    page.getByLabel('Комментарий к шагу 1', { exact: true }),
  ).toHaveValue('Ответы проверены')
  await page
    .getByRole('button', { name: 'Завершить прогон', exact: true })
    .click()
  await expect(page.getByText('Первый успешный', { exact: true })).toBeVisible()
  const success = (await api(`/test-cases/${c.id}/runs`)).items[0]
  expect(success.status).toBe('passed')
  // Another terminal run can be retained explicitly; a retry preserves both attempts.
  const third = await api(
    `/test-cases/${c.id}/runs`,
    'POST',
    { environmentId: success.environmentId, revision: c.revision },
    randomUUID(),
  )
  const bid = c.steps[0].blocks[0].id
  for (let i = 0; i < 2; i++) {
    const a = await api(
      `/runs/${third.id}/blocks/${bid}/attempts`,
      'POST',
      {},
      randomUUID(),
    )
    await eventually(
      async () =>
        !['queued', 'running'].includes((await api(`/attempts/${a.id}`)).state),
    )
  }
  const terminal = await api(`/runs/${third.id}/finish`, 'POST')
  expect(terminal.attempts.map((a: any) => a.number)).toEqual([1, 2])
  expect(terminal.status).toBe('blocked')
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await page.getByRole('button', { name: 'Несохранённые', exact: true }).click()
  await page
    .locator('.history-table')
    .getByRole('button', { name: 'Сохранить', exact: true })
    .click()
  await page.getByRole('button', { name: 'Сохранённые', exact: true }).click()
  await expect(
    page.getByText('Сохранён вручную', { exact: true }),
  ).toBeVisible()
  // Current edits cannot change the retained scenario snapshot.
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await page
    .getByLabel('Описание шага 1', { exact: true })
    .fill('Новое описание')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  expect((await api(`/runs/${first.id}`)).snapshot.steps[0].description).toBe(
    c.steps[0].description,
  )
  const folderToggle = page.getByRole('button', {
    name: 'Папка Проверки API',
    exact: true,
  })
  if ((await folderToggle.getAttribute('aria-expanded')) === 'false')
    await folderToggle.click()
  await page
    .getByRole('button', {
      name: 'Архивировать тест-кейс Смешанный сценарий',
      exact: true,
    })
    .click()
  await expect(page.locator('.case-row.archived')).toContainText('Архив')
  await page
    .getByRole('button', {
      name: 'Архивировать папку Проверки API',
      exact: true,
    })
    .click()
  await page.getByRole('button', { name: 'Архив', exact: true }).click()
  await expect(
    page.getByRole('button', { name: 'Папка Проверки API', exact: true }),
  ).toBeVisible()
  await page
    .getByRole('button', {
      name: 'Восстановить папку Проверки API',
      exact: true,
    })
    .click()
  await page.getByRole('button', { name: 'Активные', exact: true }).click()
  await expect(page.locator('.case-row.archived')).toContainText(
    'Смешанный сценарий',
  )
  await stopApp()
  await startApp()
  await page.reload()
  await expect(
    page.getByText('Архив · только просмотр', { exact: true }),
  ).toBeVisible()
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await expect(page.locator('.history-table tbody tr')).toHaveCount(3)
  await page.screenshot({
    path: resolve(root, '.local/workspace.png'),
    fullPage: true,
  })
  expect(errors).toEqual([])
  expect(logs).not.toContain(config.target.password)
})

test('остановка процесса не повторяет внешний эффект и сохраняет ручной шаг', async ({
  page,
}) => {
  let env = (await api('/environments')).items.find((e: any) => !e.readOnly)
  if (!env) {
    env = await api('/environments', 'POST', {
      name: 'recovery',
      description: '',
    })
    await api(`/environments/${env.id}/resources`, 'POST', {
      name: 'AI_GW_SERVISE',
      kind: 'http',
      config: {
        baseUrl: config.http + '/api',
        headers: [],
        tls: { mode: 'plaintext' },
        protoFiles: {},
      },
      secrets: { replace: {}, clear: [] },
    })
  }
  const folder = await api('/folders', 'POST', { name: 'Восстановление' })
  let c = await api(`/folders/${folder.id}/test-cases`, 'POST', {
    name: 'Прерванный вызов',
  })
  const step = randomUUID(),
    block = randomUUID()
  c = await api(`/test-cases/${c.id}`, 'PUT', {
    name: c.name,
    revision: c.revision,
    steps: [
      {
        id: step,
        description: 'Сохранённый ручной шаг',
        blocks: [
          {
            id: block,
            kind: 'http',
            resource: 'AI_GW_SERVISE',
            timeoutSeconds: 120,
            http: {
              method: 'POST',
              path: '/task',
              query: [],
              headers: [
                {
                  id: randomUUID(),
                  name: 'Authorization',
                  source: {
                    method: 'POST',
                    url: config.http + '/token?delay=1',
                    query: [],
                    headers: [],
                    bodyMode: 'none',
                    body: '',
                    tls: { mode: 'plaintext' },
                    jsonPointer: '/tokens/0/value',
                    prefix: 'Bearer ',
                    fetchMode: 'per_run',
                  },
                },
              ],
              bodyMode: 'none',
              body: '',
            },
            expected: { statusCode: 200, bodyMode: 'none' },
          },
        ],
      },
    ],
  })
  const run = await api(
    `/test-cases/${c.id}/runs`,
    'POST',
    { environmentId: env.id, revision: c.revision },
    randomUUID(),
  )
  await api(`/runs/${run.id}/steps/${step}`, 'PATCH', {
    status: 'passed',
    comment: 'До остановки',
    revision: run.stepStates[0].revision,
  })
  const before = await (await fetch(config.http + '/stats')).json()
  const a = await api(
    `/runs/${run.id}/blocks/${block}/attempts`,
    'POST',
    {},
    randomUUID(),
  )
  await eventually(
    async () =>
      (await (await fetch(config.http + '/stats')).json()).sources ===
      before.sources + 1,
  )
  await stopApp()
  await startApp()
  const restored = await api(`/runs/${run.id}`)
  expect(restored.status).toBe('interrupted')
  expect(restored.stepStates[0].comment).toBe('До остановки')
  expect(restored.attempts[0].id).toBe(a.id)
  expect(restored.attempts[0].state).toBe('interrupted')
  const after = await (await fetch(config.http + '/stats')).json()
  expect(after.calls).toBe(before.calls)
  expect(after.sources).toBe(before.sources + 1)
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Открыть встроенный пример', exact: true })
    .waitFor()
  await page.evaluate((id) => localStorage.setItem('qag.case', id), c.id)
  await page.reload()
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await expect(page.getByText('Прерван', { exact: true })).toBeVisible()
})

test('сортировка, конфликт редактора, секреты и повтор потерянной команды', async ({
  page,
}) => {
  const folder = await api('/folders', 'POST', { name: 'Порядок и конфликты' })
  const names = ['Ядро', 'бета', 'Альфа', 'Beta', 'alpha'],
    created = []
  for (const name of names)
    created.push(
      await api(`/folders/${folder.id}/test-cases`, 'POST', { name }),
    )
  let values: any[] = [],
    cursor = ''
  do {
    const p = await api(
      `/folders/${folder.id}/test-cases?limit=2&cursor=${encodeURIComponent(cursor)}`,
    )
    values.push(...p.items)
    cursor = p.nextCursor ?? ''
  } while (cursor)
  expect(values.map((v) => v.name)).toEqual(
    [...names].sort(new Intl.Collator('ru', { sensitivity: 'base' }).compare),
  )
  const rename = await api(`/test-cases/${created[0].id}`, 'PATCH', {
    name: 'Авангард',
  })
  await api(`/test-cases/${rename.id}`, 'PATCH', { archived: true })
  expect((await api(`/folders/${folder.id}/test-cases`)).items.at(-1).id).toBe(
    rename.id,
  )
  const env = await api('/environments', 'POST', {
    name: 'security',
    description: '',
  })
  const token = 'sentinel-"<>&-e2e-token'
  const resource = await api(`/environments/${env.id}/resources`, 'POST', {
    name: 'GW',
    kind: 'http',
    config: {
      baseUrl: config.http + '/api',
      headers: [{ name: 'Authorization', value: '${TOKEN}' }],
      tls: { mode: 'plaintext' },
      protoFiles: {},
    },
    secrets: { replace: { TOKEN: token }, clear: [] },
  })
  expect(JSON.stringify(resource)).not.toContain(token)
  const other = await api('/environments', 'POST', {
    name: 'stage',
    description: '',
  })
  const stage = await api(`/environments/${other.id}/resources`, 'POST', {
    name: 'GW',
    kind: 'http',
    config: {
      baseUrl: config.http + '/stage',
      headers: [],
      tls: { mode: 'plaintext' },
      protoFiles: {},
    },
    secrets: { replace: {}, clear: [] },
  })
  expect(stage.config.baseUrl).not.toBe(resource.config.baseUrl)
  let c = created[1]
  const block = randomUUID(),
    step = randomUUID()
  c = await api(`/test-cases/${c.id}`, 'PUT', {
    name: c.name,
    revision: c.revision,
    steps: [
      { id: step, description: 'Первый ручной шаг', blocks: [] },
      {
        id: randomUUID(),
        description: 'Второй ручной шаг',
        blocks: [
          {
            id: block,
            kind: 'http',
            resource: 'GW',
            timeoutSeconds: 30,
            http: {
              method: 'GET',
              path: '/flaky',
              headers: [],
              query: [],
              bodyMode: 'none',
              body: '',
            },
            expected: { statusCode: 200, bodyMode: 'none' },
          },
        ],
      },
    ],
  })
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Открыть встроенный пример', exact: true })
    .waitFor()
  await page.evaluate(
    ({ id, env }) => {
      localStorage.setItem('qag.case', id)
      localStorage.setItem('qag.environment', env)
    },
    { id: c.id, env: env.id },
  )
  await page.reload()
  // Empty manual titles and author order survive moving and reopening.
  await page
    .getByRole('button', { name: 'Переместить шаг 2 вверх', exact: true })
    .click()
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Второй ручной шаг',
  )
  c = await api(`/test-cases/${c.id}`)
  await api(`/test-cases/${c.id}`, 'PUT', {
    name: c.name,
    revision: c.revision,
    steps: c.steps,
  })
  await page
    .getByLabel('Описание шага 1', { exact: true })
    .fill('Мой несохранённый текст')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByRole('alert').first()).toBeVisible()
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Мой несохранённый текст',
  )
  await page.reload()
  await page
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    page.locator('.results').getByText('Есть различия', { exact: true }),
  ).toBeVisible()
  expect(await page.locator('.results').textContent()).not.toContain(token)
  // Lose one response after the server accepted it, then explicitly retry.
  const before = (await (await fetch(config.http + '/stats')).json()).calls
  let accepted = ''
  await page.route(
    `**/test-cases/${c.id}/blocks/${block}/attempts`,
    async (route) => {
      const response = await route.fetch()
      accepted = (await response.json()).id
      await route.abort('failed')
      await page.unroute(`**/test-cases/${c.id}/blocks/${block}/attempts`)
    },
  )
  await page
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(page.getByRole('alert').first()).toBeVisible()
  await page
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    page.locator('.results').getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await (await fetch(config.http + '/stats')).json()).calls).toBe(
    before + 1,
  )
  expect((await api(`/attempts/${accepted}`)).result.redacted).toBe(true)
  // Retry within a run preserves both outcomes; the last completed attempt wins.
  c = await api(`/test-cases/${c.id}`)
  const run = await api(
    `/test-cases/${c.id}/runs`,
    'POST',
    { environmentId: env.id, revision: c.revision },
    randomUUID(),
  )
  for (const s of run.stepStates)
    await api(`/runs/${run.id}/steps/${s.stepId}`, 'PATCH', {
      status: 'passed',
      comment: 'проверено',
      revision: s.revision,
    })
  for (let n = 0; n < 2; n++) {
    const a = await api(
      `/runs/${run.id}/blocks/${block}/attempts`,
      'POST',
      {},
      randomUUID(),
    )
    await eventually(
      async () =>
        !['queued', 'running'].includes((await api(`/attempts/${a.id}`)).state),
    )
  }
  const finished = await api(`/runs/${run.id}/finish`, 'POST')
  expect(finished.status).toBe('passed')
  expect(finished.retentionReasons.sort()).toEqual([
    'first_run',
    'first_success',
  ])
  expect(finished.attempts.map((a: any) => a.result.comparison.status)).toEqual(
    ['mismatch', 'match'],
  )
  expect(logs).not.toContain(token)
  expect(await page.content()).not.toContain(token)
  const previousEnvironment = await page
    .getByLabel('Текущее окружение')
    .inputValue()
  await page
    .getByLabel('Описание шага 1', { exact: true })
    .fill('Черновик перед примером')
  const pinned = page.getByRole('button', {
    name: 'Открыть встроенный пример',
    exact: true,
  })
  await pinned.click()
  await expect(
    page.getByRole('dialog', { name: 'Несохранённые изменения' }),
  ).toBeVisible()
  await page.getByRole('button', { name: 'Остаться', exact: true }).click()
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Черновик перед примером',
  )
  await pinned.click()
  await page
    .getByRole('button', { name: 'Отбросить изменения', exact: true })
    .click()
  await expect(page.getByLabel('Текущее окружение')).toBeDisabled()
  await page
    .getByRole('button', { name: `Папка ${folder.name}`, exact: true })
    .click()
  await page
    .getByRole('button', { name: `Открыть тест-кейс ${c.name}`, exact: true })
    .click()
  await expect(page.getByLabel('Текущее окружение')).toBeEnabled()
  await expect(page.getByLabel('Текущее окружение')).toHaveValue(
    previousEnvironment,
  )
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Второй ручной шаг',
  )
})

test('новые формы: черновики, изображения, переменные, заголовки и темы', async ({
  page,
}) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Окружения и ресурсы', exact: false })
    .click()
  await page.getByRole('button', { name: '+ Окружение', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Новое окружение' })
  await expect(dialog.locator('input')).toHaveCount(1)
  await expect(dialog.locator('textarea')).toHaveCount(1)
  await dialog.getByLabel('Имя окружения', { exact: true }).fill('authoring')
  await dialog
    .getByLabel('Описание окружения', { exact: false })
    .fill('Окружение для новых форм')
  await dialog
    .getByRole('button', { name: 'Сохранить окружение', exact: true })
    .click()
  await page
    .getByRole('button', { name: 'Настроить переменные', exact: true })
    .click()
  const vars = page.getByRole('dialog', { name: 'Переменные окружения' })
  const pairs = vars.getByRole('group', {
    name: 'Открытые переменные',
    exact: true,
  })
  for (const [i, name, value] of [
    [1, 'API_HOST', config.http],
    [2, 'AUTH_HOST', config.http],
  ] as const) {
    await pairs.getByRole('button', { name: '+ Добавить', exact: true }).click()
    await pairs
      .getByLabel(`Открытые переменные: имя ${i}`, { exact: true })
      .fill(name)
    await pairs
      .getByLabel(`Открытые переменные: значение ${i}`, { exact: true })
      .fill(value)
  }
  await vars
    .getByRole('button', { name: '+ Секретная переменная', exact: true })
    .click()
  await vars.getByLabel('Имя секрета', { exact: false }).fill('CLIENT_SECRET')
  await vars
    .getByLabel('Новое значение: CLIENT_SECRET', { exact: false })
    .fill('e2e-source-secret')
  await vars
    .getByRole('button', { name: 'Сохранить переменные', exact: true })
    .click()
  await expect(page.locator('.environment-variables')).toContainText('API_HOST')
  await expect(page.locator('.environment-variables')).not.toContainText(
    'e2e-source-secret',
  )
  const env = (await api('/environments')).items.find(
    (e: any) => e.name === 'authoring',
  )
  await page
    .locator('.resource-list')
    .getByRole('button', { name: '+ Добавить', exact: true })
    .click()
  const form = page.locator('.resource-form')
  await form.getByLabel('Имя ресурса', { exact: true }).fill('AUTO_GW')
  await form
    .getByLabel('Базовый URL', { exact: false })
    .fill(config.http + '/api')
  await form
    .getByLabel('Защита подключения', { exact: true })
    .selectOption('plaintext')
  const common = form.getByRole('group', {
    name: 'Общие заголовки',
    exact: true,
  })
  for (const [i, name, mode] of [
    [0, 'Authorization', 'per_run'],
    [1, 'X-Request-Token', 'per_attempt'],
  ] as const) {
    await common
      .getByRole('button', { name: '+ Добавить', exact: true })
      .last()
      .click()
    const row = common.locator('.header-row').nth(i)
    await row
      .getByLabel(`Общие заголовки: имя ${i + 1}`, { exact: true })
      .fill(name)
    await row
      .getByLabel(`Общие заголовки: способ значения ${i + 1}`, { exact: true })
      .selectOption('http')
    await row
      .getByLabel('Источник: метод', { exact: true })
      .selectOption('POST')
    await row
      .getByLabel('Источник: URL', { exact: false })
      .fill(config.http + '/token?host=${AUTH_HOST}')
    await row
      .getByLabel('Источник: формат тела', { exact: true })
      .selectOption('json')
    await row
      .getByLabel('Источник: тело запроса', { exact: false })
      .fill('{"secret":"${CLIENT_SECRET}"}')
    await row
      .getByLabel('Источник: Защита подключения', { exact: true })
      .selectOption('plaintext')
    await row
      .getByLabel('Источник: поле JSON', { exact: false })
      .fill('/tokens/0/value')
    await row.getByLabel('Источник: префикс', { exact: false }).fill('Bearer ')
    await row
      .getByLabel('Источник: когда получать', { exact: false })
      .selectOption(mode)
  }
  const stats = async () => await (await fetch(config.http + '/stats')).json()
  const initial = await stats()
  await form
    .getByRole('button', { name: 'Сохранить ресурс', exact: true })
    .click()
  await expect(
    page.locator('.resource-item').filter({ hasText: 'AUTO_GW' }),
  ).toBeVisible()
  expect((await stats()).sources).toBe(initial.sources)
  await page
    .getByRole('button', { name: 'Тест-кейсы', exact: false })
    .first()
    .click()
  await page
    .locator('aside')
    .getByRole('button', { name: 'Создать папку', exact: true })
    .click()
  await page.getByLabel('Название', { exact: true }).fill('Новые возможности')
  await page
    .getByRole('button', { name: 'Сохранить название', exact: true })
    .click()
  await page
    .getByRole('button', {
      name: 'Добавить тест-кейс в Новые возможности',
      exact: true,
    })
    .click()
  await page
    .getByRole('button', { name: 'Сохранить название', exact: true })
    .click()
  await expect(page.getByLabel('Название тест-кейса')).toHaveValue(
    'Новый тест-кейс',
  )
  await page.getByLabel('Название тест-кейса').fill('')
  await page
    .getByRole('button', { name: '+ Добавить шаг', exact: true })
    .click()
  await page.getByRole('button', { name: '+ HTTP', exact: true }).click()
  const blocks = page.locator('.editor .call-block')
  await blocks
    .first()
    .getByLabel('Ожидаемый HTTP-статус', { exact: true })
    .fill('')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(
    page.getByRole('region', { name: 'Готовность к запуску' }),
  ).toBeVisible()
  await page.reload()
  await expect(
    blocks.first().getByLabel('Ресурс', { exact: true }),
  ).toHaveValue('')
  await expect(
    blocks.first().getByLabel('Ожидаемый HTTP-статус', { exact: true }),
  ).toHaveValue('')
  await expect(
    page.getByRole('button', { name: 'Начать прогон', exact: true }),
  ).toBeDisabled()
  await page
    .getByLabel('Описание шага 1', { exact: true })
    .fill('Сравнить эталоны вручную')
  await page
    .getByLabel('Ожидаемый результат шага 1', { exact: false })
    .fill('Экран соответствует изображению')
  const manual = page.locator('.editor .step-card > .image-gallery')
  await manual
    .getByLabel('Добавить изображения', { exact: true })
    .setInputFiles(resolve(root, 'internal/catalog/testdata/pixel.webp'))
  await expect(manual.locator('.image-reference')).toHaveCount(1)
  await manual
    .getByLabel('Подпись изображения 1', { exact: true })
    .fill('Эталон шага')
  await page.getByRole('button', { name: '+ HTTP', exact: true }).click()
  for (let i = 0; i < 2; i++) {
    const b = blocks.nth(i),
      select = b.getByLabel('Ресурс', { exact: true })
    await select.focus()
    await select.press('Home')
    await select.press('ArrowDown')
    await select.press('Enter')
    await expect(select).toHaveValue('AUTO_GW')
    await b
      .getByLabel('Путь или ссылка на ресурс', { exact: false })
      .fill('/task')
    await b.getByLabel('Ожидаемый HTTP-статус', { exact: true }).fill('200')
    await b.getByLabel('Проверка тела', { exact: true }).selectOption('none')
  }
  await blocks
    .first()
    .getByLabel('Добавить изображения', { exact: true })
    .setInputFiles(resolve(root, 'internal/catalog/testdata/pixel.webp'))
  await expect(blocks.first().locator('.image-reference')).toHaveCount(1)
  await blocks
    .first()
    .getByLabel('Подпись изображения 1', { exact: true })
    .fill('Эталон вызова')
  await page.getByLabel('Цветовая тема').selectOption('dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Сравнить эталоны вручную',
  )
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(
    manual.getByLabel('Подпись изображения 1', { exact: true }),
  ).toHaveValue('Эталон шага')
  await manual
    .getByRole('button', { name: 'Открыть изображение 1', exact: true })
    .click()
  await expect(
    page.getByRole('dialog', { name: 'Изображение ожидаемого результата' }),
  ).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(
    page.getByRole('dialog', { name: 'Изображение ожидаемого результата' }),
  ).toHaveCount(0)
  const folder = (await api('/folders')).items.find(
    (f: any) => f.name === 'Новые возможности',
  )
  const c = (await api(`/folders/${folder.id}/test-cases`)).items[0]
  const before = await stats()
  await blocks
    .first()
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    blocks.first().getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await stats()).sources).toBe(before.sources + 2)
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  expect((await stats()).sources).toBe(before.sources + 2)
  const calls = page.locator('.run-view .call-block')
  await calls
    .first()
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    calls.first().getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  await page.reload()
  await page
    .getByRole('button', { name: 'Прогон', exact: false })
    .filter({ hasText: /^Прогон/ })
    .first()
    .click()
  await calls
    .nth(1)
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    calls.nth(1).getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await stats()).sources).toBe(before.sources + 5)
  await expect(calls.nth(1)).toContainText('Значение из текущего прогона')
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('passed')
  await page
    .getByRole('button', { name: 'Завершить прогон', exact: true })
    .click()
  await expect(page.getByText('Первый успешный', { exact: true })).toBeVisible()
  const completed = (await api(`/test-cases/${c.id}/runs`)).items[0]
  expect(completed.retentionReasons).toEqual(['first_run', 'first_success'])
  const full = await api(`/runs/${completed.id}`)
  expect(JSON.stringify(full)).not.toContain('fixture-secret-token-')
  expect(JSON.stringify(full)).not.toContain('e2e-source-secret')
  for (const theme of ['light', 'dark']) {
    await page.getByLabel('Цветовая тема').selectOption(theme)
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.screenshot({
      path: resolve(root, `.local/authoring-run-${theme}.png`),
      fullPage: true,
    })
  }
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await manual
    .getByRole('button', { name: 'Удалить изображение', exact: true })
    .click()
  await blocks
    .first()
    .getByRole('button', { name: 'Удалить изображение', exact: true })
    .click()
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await page
    .locator('.history-table')
    .getByRole('button', { name: 'Открыть', exact: true })
    .first()
    .click()
  await expect(page.locator('.run-view')).toContainText('Эталон шага')
  await expect(page.locator('.run-view')).toContainText('Эталон вызова')
  await page
    .getByRole('dialog')
    .getByRole('button', { name: 'Закрыть', exact: true })
    .click()
  // A new run obtains fresh values; cancelling clears its cache.
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await calls
    .first()
    .getByRole('button', { name: 'Запустить вызов', exact: true })
    .click()
  await expect(
    calls.first().getByText('Совпадает', { exact: true }),
  ).toBeVisible()
  expect((await stats()).sources).toBe(before.sources + 7)
  await page
    .getByRole('button', { name: 'Отменить прогон', exact: true })
    .click()
  await page.getByLabel('Цветовая тема').selectOption('system')
  await page.emulateMedia({ colorScheme: 'dark' })
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.emulateMedia({ colorScheme: 'light' })
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  expect(errors).toEqual([])
  expect(env.description).toBe('Окружение для новых форм')
})

test('тема работает без localStorage и до загрузки интерфейса', async ({
  browser,
}) => {
  const context = await browser.newContext({ colorScheme: 'dark' })
  await context.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', {
      get() {
        throw new DOMException('disabled', 'SecurityError')
      },
    })
  })
  const page = await context.newPage()
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto(base)
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.getByLabel('Цветовая тема').selectOption('light')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  expect(errors).toEqual([])
  const csp = (await page.request.get(base)).headers()[
    'content-security-policy'
  ]
  expect(csp.split(';').find((v) => v.trim().startsWith('script-src'))).toBe(
    " script-src 'self'",
  )
  await context.close()
})

test('mTLS в форме ресурса и отдельного источника, сохранённый ключ не раскрывается', async ({
  page,
}) => {
  let env = await api('/environments', 'POST', {
    name: 'mtls-ui',
    description: 'Два защищённых подключения',
  })
  env = await api(`/environments/${env.id}/variables`, 'PUT', {
    revision: env.revision,
    variables: {},
    secrets: { replace: { SOURCE_KEY: config.privateKey }, clear: [] },
  })
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Окружения и ресурсы', exact: false })
    .click()
  await page
    .getByLabel('Текущее окружение', { exact: true })
    .selectOption(env.id)
  await page
    .locator('.resource-list')
    .getByRole('button', { name: '+ Добавить', exact: true })
    .click()
  const form = page.locator('.resource-form')
  await form.getByLabel('Имя ресурса', { exact: true }).fill('SECURE_GW')
  await form
    .getByLabel('Базовый URL', { exact: false })
    .fill(config.mtlsHttp + '/api')
  await form
    .getByLabel('Защита подключения', { exact: true })
    .selectOption('mtls')
  await form.getByLabel('CA-сертификат PEM', { exact: false }).fill(config.ca)
  await form
    .getByLabel('Клиентский сертификат PEM', { exact: false })
    .fill(config.certificate)
  await form
    .getByLabel('Действие: PRIVATE_KEY', { exact: true })
    .selectOption('replace')
  await form
    .getByLabel('Новое значение: PRIVATE_KEY', { exact: false })
    .fill(config.privateKey)
  const common = form.getByRole('group', {
    name: 'Общие заголовки',
    exact: true,
  })
  await common.getByRole('button', { name: '+ Добавить', exact: true }).click()
  await common
    .getByLabel('Общие заголовки: имя 1', { exact: true })
    .fill('Authorization')
  await common
    .getByLabel('Общие заголовки: способ значения 1', { exact: true })
    .selectOption('http')
  await common
    .getByLabel('Источник: URL', { exact: false })
    .fill(config.mtlsHttp + '/token')
  await common
    .getByLabel('Источник: Защита подключения', { exact: true })
    .selectOption('mtls')
  await common
    .getByLabel('Источник: CA-сертификат PEM', { exact: false })
    .fill(config.ca)
  await common
    .getByLabel('Источник: Клиентский сертификат PEM', { exact: false })
    .fill(config.certificate)
  await common
    .getByLabel('Источник: Секрет приватного ключа', { exact: true })
    .fill('SOURCE_KEY')
  await common
    .getByLabel('Источник: поле JSON', { exact: false })
    .fill('/tokens/0/value')
  await common
    .getByLabel('Источник: когда получать', { exact: false })
    .selectOption('per_run')
  await form
    .getByRole('button', { name: 'Сохранить ресурс', exact: true })
    .click()
  await expect(
    page.locator('.resource-item').filter({ hasText: 'SECURE_GW' }),
  ).toBeVisible()
  await expect(
    form.getByLabel('Защита подключения', { exact: true }),
  ).toHaveValue('mtls')
  await expect(
    form.getByLabel('Новое значение: PRIVATE_KEY', { exact: false }),
  ).toHaveCount(0)
  let resource = (await api(`/environments/${env.id}/resources`)).items[0]
  expect(JSON.stringify(resource)).not.toContain(config.privateKey)
  expect(resource.config.headers[0].source.privateKeySecret).toBe('SOURCE_KEY')
  const folder = await api('/folders', 'POST', {
    name: 'Защищённые подключения',
  })
  let c = await api(`/folders/${folder.id}/test-cases`, 'POST', {})
  c = await api(`/test-cases/${c.id}`, 'PUT', {
    name: c.name,
    revision: c.revision,
    steps: [
      {
        id: randomUUID(),
        description: 'Проверить mTLS',
        blocks: [
          {
            id: randomUUID(),
            kind: 'http',
            resource: 'SECURE_GW',
            timeoutSeconds: 5,
            http: {
              method: 'GET',
              path: '/task',
              query: [],
              headers: [],
              bodyMode: 'none',
              body: '',
            },
            expected: { statusCode: 200, bodyMode: 'none' },
          },
        ],
      },
    ],
  })
  let attempt = await api(
    `/test-cases/${c.id}/blocks/${c.steps[0].blocks[0].id}/attempts`,
    'POST',
    { environmentId: env.id, revision: c.revision },
    randomUUID(),
  )
  await eventually(async () => {
    attempt = await api(`/attempts/${attempt.id}`)
    return attempt.state === 'completed'
  })
  expect(attempt.result.comparison.status).toBe('match')
  expect(attempt.result.headerSources[0].state).toBe('fetched')
  // Switching off client authentication changes the public mode, keeping the
  // encrypted private key until an explicit secret-delete action.
  await form
    .getByLabel('Защита подключения', { exact: true })
    .selectOption('tls')
  await form
    .getByRole('button', { name: 'Сохранить ресурс', exact: true })
    .click()
  await expect(
    form.getByLabel('Защита подключения', { exact: true }),
  ).toHaveValue('tls')
  await eventually(async () => {
    resource = (await api(`/environments/${env.id}/resources`)).items[0]
    return resource.config.tls.mode === 'tls'
  })
  expect(resource.secretNames).toContain('PRIVATE_KEY')
  expect(resource.config.tls.certificate ?? '').toBe('')
})

test('оформление: favicon, предупреждения и закреплённая колонка результата', async ({ page }) => {
  const demo = await api('/demo'), source = await api(`/test-cases/${demo.testCaseId}`)
  const system = (await api('/environments')).items.find((e: any) => e.id === demo.environmentId)
  const ordinary = await api('/environments', 'POST', { name: 'Пользовательское окружение', description: 'Проверка совпадающего отображаемого имени' })
  // Stored environment names are unique. Simulate a colliding display label
  // without changing that database rule: only the readOnly flag controls UI.
  await page.route('**/api/v1/environments', async route => {
    const response = await route.fetch(), value = await response.json()
    for (const env of value.items) if (env.id === ordinary.id) env.name = system.name
    await route.fulfill({ response, json: value })
  })
  const folder = await api('/folders', 'POST', { name: 'Проверка оформления' })
  let doc = await api(`/folders/${folder.id}/test-cases`, 'POST', { name: source.name })
  const image = await api(`/test-cases/${doc.id}/images`, 'POST', {
    fileName: 'reference.png', dataBase64: (await readFile(resolve(root, 'web/public/qag-32.png'))).toString('base64'),
  })
  doc = await api(`/test-cases/${doc.id}`, 'PUT', {
    name: source.name, revision: doc.revision,
    steps: [{
      id: randomUUID(), title: 'Длинный шаг с вызовами',
      description: Array.from({ length: 80 }, (_, n) => `Строка ${n + 1}: проверить описание и сохранить ручной результат.`).join('\n'),
      expected: { text: 'Ручной эталон шага', images: [{ imageId: image.id, caption: 'Иллюстрация' }] },
      blocks: source.steps.slice(0, 3).map((s: any) => ({ ...s.blocks[0], id: randomUUID() })),
    }, { id: randomUUID(), title: 'Следующий шаг', description: 'Управление предыдущего шага не должно перекрывать этот шаг.', blocks: [] }],
  })
  await page.goto('/')
  const icon = page.locator('link[rel="icon"][type="image/png"]')
  await expect(page.getByText('Локально', { exact: true })).toHaveCount(0)
  await expect(page.getByLabel('Текущее окружение')).toBeVisible()
  await expect(icon).toHaveAttribute('sizes', '32x32')
  const iconUrl = await icon.getAttribute('href')
  expect(iconUrl).toMatch(/^\/qag-32\.png\?v=[a-f0-9]{12}$/)
  for (const link of await page.locator('link[rel="icon"]').all()) {
    const url = (await link.getAttribute('href'))!, response = await page.request.get(url)
    expect(response.ok()).toBe(true)
    expect(response.headers()['content-type']).toMatch(/^image\//)
    expect(await response.body()).toEqual(await readFile(resolve(root, 'web/public', url.split('?')[0].slice(1))))
  }
  expect(await page.evaluate(async (url) => {
    const image = new Image(); image.src = url!; await image.decode(); return [image.naturalWidth, image.naturalHeight]
  }, iconUrl)).toEqual([32, 32])
  expect((await page.request.get('/')).headers()['cache-control']).toBe('no-cache')
  await page.getByRole('button', { name: 'Открыть встроенный пример', exact: true }).click()
  const scenarioNotice = page.getByRole('region', { name: 'Ограничения встроенного примера' })
  await expect(scenarioNotice).toContainText('Сценарий и его системное окружение нельзя редактировать')
  for (const theme of ['dark', 'light']) {
    await page.getByLabel('Цветовая тема', { exact: true }).selectOption(theme)
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.screenshot({ path: resolve(root, `.local/appearance-editor-${theme}.png`) })
  }
  await page.getByRole('button', { name: /Окружения и ресурсы/ }).click()
  const environmentNotice = page.getByRole('region', { name: 'Ограничения системного окружения' })
  for (const resource of await page.locator('.resource-item').all()) {
    await resource.click()
    await expect(environmentNotice).toBeVisible()
    await expect(page.getByRole('button', { name: 'Системное подключение · только просмотр', exact: true })).toBeDisabled()
  }
  for (const theme of ['dark', 'light']) {
    await page.getByLabel('Цветовая тема', { exact: true }).selectOption(theme)
    await page.screenshot({ path: resolve(root, `.local/appearance-resources-${theme}.png`) })
  }
  await page.getByLabel('Текущее окружение').selectOption(ordinary.id)
  await expect(environmentNotice).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Настроить окружение', exact: true })).toBeEnabled()
  await page.goto('/')
  await page.getByRole('button', { name: `Папка ${folder.name}`, exact: true }).click()
  await page.getByRole('button', { name: `Открыть тест-кейс ${doc.name}`, exact: true }).click()
  await expect(scenarioNotice).toHaveCount(0)
  await expect(page.getByLabel('Название тест-кейса')).toBeEnabled()
  await page.getByLabel('Текущее окружение').selectOption(system.id)
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await expect(page.locator('.run-step')).toHaveCount(2)
  let run = (await api(`/test-cases/${doc.id}/runs?active=true`)).items[0]
  const variant = await api(`/runs/${run.id}/blocks/${doc.steps[0].blocks[0].id}/variants`, 'POST', { name: 'Исследование' })
  await expect(page.getByRole('tab', { name: 'Исследование', exact: true })).toHaveCount(1)
  const first = page.locator('.run-step').first(), next = page.locator('.run-step').nth(1)
  await expect(first.locator('.call-block')).toHaveCount(3)
  const control = first.getByLabel('Статус шага 1', { exact: true })
  await expect(control.locator('option')).toHaveCount(3)
  await control.focus()
  await control.press('ArrowDown')
  await expect(control).toHaveValue('passed')
  await expect(control).toBeEnabled()
  const comment = first.getByLabel('Комментарий к шагу 1', { exact: true })
  await comment.fill('Черновик после ручной оценки')
  const writes: string[] = []
  page.on('request', r => { if (r.method() !== 'GET' && r.url().includes('/api/')) writes.push(r.url()) })
  for (const theme of ['dark', 'light']) {
    await page.getByLabel('Цветовая тема', { exact: true }).selectOption(theme)
    for (const width of [1440, 768, 375]) {
      await page.setViewportSize({ width, height: 1000 })
      await comment.focus()
      await first.evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY + 700))
      await expect.poll(async () => first.evaluate(el => {
        const content = el.querySelector('.run-step-content')!.getBoundingClientRect(), status = el.querySelector('.run-step-status')!.getBoundingClientRect()
        const panels = [...document.querySelectorAll('.toolbar, .document-header, .run-toolbar')].filter(p => ['sticky','fixed'].includes(getComputedStyle(p).position))
        const bottom = Math.max(0, ...panels.map(p => p.getBoundingClientRect().bottom))
        const buttons = [...document.querySelectorAll('.run-toolbar button')].map(b => b.getBoundingClientRect())
        return status.left >= content.right && status.top >= bottom && status.bottom <= innerHeight && status.right <= innerWidth && buttons.every(b => b.top >= 0 && b.bottom <= innerHeight && b.right <= innerWidth)
      })).toBe(true)
      await expect(comment).toBeFocused()
      await expect(comment).toHaveValue('Черновик после ручной оценки')
      await expect(control).toHaveValue('passed')
      await page.screenshot({ path: resolve(root, `.local/appearance-run-${theme}-${width}.png`) })
    }
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  await first.evaluate(el => window.scrollTo(0, el.getBoundingClientRect().bottom + window.scrollY - 250))
  const end = await first.locator('.run-step-status').boundingBox(), edge = await first.boundingBox(), following = await next.boundingBox()
  expect(end!.y + end!.height).toBeLessThanOrEqual(edge!.y + edge!.height)
  expect(end!.y + end!.height).toBeLessThan(following!.y)
  await first.evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY + 700))
  await page.locator('.document-header h1').evaluate(el => { el.textContent = 'Очень длинный заголовок для проверки изменения высоты закреплённой панели '.repeat(3) })
  await expect.poll(async () => first.locator('.run-step-status').evaluate(el => el.getBoundingClientRect().top >= document.querySelector('.document-header')!.getBoundingClientRect().bottom)).toBe(true)
  await page.setViewportSize({ width: 720, height: 450 })
  await first.evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY + 700))
  await expect.poll(async () => first.locator('.run-step-status').evaluate(el => el.getBoundingClientRect().top >= 0 && el.getBoundingClientRect().bottom <= innerHeight)).toBe(true)
  await page.setViewportSize({ width: 960, height: 720 })
  await page.evaluate(() => { document.body.style.zoom = '1.25' })
  await first.evaluate(el => window.scrollTo(0, el.getBoundingClientRect().top + window.scrollY + 700))
  await expect.poll(async () => first.locator('.run-step-status').evaluate(el => {
    const status = el.getBoundingClientRect(), toolbar = document.querySelector('.run-toolbar')!.getBoundingClientRect()
    return status.top >= toolbar.bottom && status.bottom <= innerHeight && toolbar.top >= 0
  })).toBe(true)
  await page.evaluate(() => { document.body.style.zoom = '' })
  await next.scrollIntoViewIfNeeded()
  await next.getByLabel('Статус шага 2', { exact: true }).focus()
  await expect.poll(async () => page.locator('.run-toolbar button').evaluateAll(buttons => buttons.every(button => {
    const r = button.getBoundingClientRect()
    return r.top >= 0 && r.bottom <= innerHeight && r.right <= innerWidth
  }))).toBe(true)
  expect(writes).toEqual([])
  await page.setViewportSize({ width: 1440, height: 1000 })
  await first.getByRole('button', { name: 'Сохранить комментарий', exact: true }).click()
  await expect(first.getByRole('button', { name: 'Сохранить комментарий', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Отменить прогон', exact: true }).click()
  await expect(control).toHaveCount(0)
  run = await api(`/runs/${run.id}`)
  expect(run.stepStates[0].status).toBe('passed')
  expect(run.stepStates[0].comment).toBe('Черновик после ручной оценки')
  expect(run.variants.some((v: any) => v.id === variant.id)).toBe(true)
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await page.getByRole('button', { name: 'Открыть', exact: true }).first().click()
  const history = page.getByRole('dialog', { name: 'Снимок прогона', exact: true })
  await expect(history.locator('.run-step-result .step-status-icon.passed')).toHaveCount(1)
  await expect(history.locator('.step-status-control select')).toHaveCount(0)
  await expect(history.getByRole('button', { name: 'Пометить все пройденным', exact: true })).toHaveCount(0)
  await expect(history.getByRole('button', { name: 'Завершить прогон', exact: true })).toHaveCount(0)
  await page.screenshot({ path: resolve(root, '.local/appearance-history.png') })
})

test('очередь и вкладки: три протокола, ручные статусы, reload и история', async ({
  page,
}) => {
  const info = await api('/demo'),
    doc = await api(`/test-cases/${info.testCaseId}`)
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Открыть встроенный пример', exact: true })
    .click()
  await page
    .locator('.document-header')
    .getByRole('button', { name: 'Выполнить всё', exact: true })
    .click()
  await expect(page.locator('.batch-progress')).toContainText('Выполнено 5/5')
  await expect(page.locator('.batch-progress')).toContainText('проблем: 0')
  let run = (await api(`/test-cases/${doc.id}/runs?active=true`)).items[0]
  expect(run.stepStates.every((s: any) => s.status === 'not_run')).toBe(true)
  expect(run.attempts).toHaveLength(5)
  for (let index = 0; index < 3; index++) {
    const call = page.locator('.run-view .call-block').nth(index)
    await call.getByRole('button', { name: '+ Вариант', exact: true }).click()
    await expect(
      call.getByRole('tab', { name: 'Вариант 1', exact: true }),
    ).toHaveAttribute('aria-selected', 'true')
    await expect(call.getByText(/Исследовательский вызов/)).toBeVisible()
    expect(await call.getByLabel('Ресурс', { exact: true }).count()).toBe(0)
    expect(
      await call.getByLabel('Ожидаемый HTTP-статус', { exact: true }).count(),
    ).toBe(0)
    if (index === 0)
      await call
        .getByLabel('Путь или ссылка на ресурс', { exact: true })
        .fill('/items/missing-item')
    if (index === 1)
      await call
        .getByLabel('JSON сообщения', { exact: true })
        .fill('{"id":"missing-item"}')
    if (index === 2) {
      await call.getByLabel('Результат SQL', { exact: true }).selectOption('exec')
      await expect(call.getByText('Ожидаемый результат', { exact: true })).toHaveCount(0)
      await call.getByLabel('Результат SQL', { exact: true }).selectOption('rows')
      await call.getByLabel('Параметр $1', { exact: true }).fill('missing-item')
    }
    await call
      .getByRole('button', { name: 'Запустить вариант', exact: true })
      .click()
    await expect(call.locator('.results .status').first()).toHaveText('Ответ получен')
    await expect(call.locator('.actual-result')).toHaveCount(1)
    await expect(call.locator('.comparison-grid, .expected-images')).toHaveCount(0)
    await expect(call.getByText('Есть различия', { exact: true })).toHaveCount(0)
  }
  await page.reload()
  await page
    .getByRole('button', { name: 'Открыть встроенный пример', exact: true })
    .click()
  await expect(
    page
      .locator('.run-view .call-block')
      .first()
      .getByRole('tab', { name: 'Вариант 1', exact: true }),
  ).toHaveAttribute('aria-selected', 'true')
  await expect(
    page
      .locator('.run-view .call-block')
      .first()
      .getByLabel('Путь или ссылка на ресурс', { exact: true }),
  ).toHaveValue('/items/missing-item')
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('failed')
  await expect(page.getByLabel('Статус шага 1', { exact: true })).toBeEnabled()
  await page.getByLabel('Статус шага 2', { exact: true }).selectOption('passed')
  await expect(page.getByLabel('Статус шага 2', { exact: true })).toBeEnabled()
  const draft = page.getByLabel('Комментарий к шагу 1', { exact: true })
  await draft.fill('Ручная проверка завершена')
  // A concurrent tab changes a different step after this tab sends its revisions.
  await page.route('**/steps/pass-all', async route => {
    const current = await api(`/runs/${run.id}`), state = current.stepStates[1]
    await api(`/runs/${run.id}/steps/${state.stepId}`, 'PATCH', { revision: state.revision, status: 'passed', comment: 'Другая вкладка' })
    await route.continue()
  }, { times: 1 })
  const conflict = page.waitForResponse(r => r.url().endsWith('/steps/pass-all') && r.status() === 409)
  const bulk = page.getByRole('button', { name: 'Пометить все пройденным', exact: true })
  await bulk.click()
  await conflict
  await expect(bulk).toBeEnabled()
  await expect(draft).toHaveValue('Ручная проверка завершена')
  await bulk.click()
  for (let i = 0; i < 5; i++) await expect(page.getByLabel(`Статус шага ${i + 1}`, { exact: true })).toHaveValue('passed')
  await expect(draft).toHaveValue('Ручная проверка завершена')
  await expect(page.getByRole('button', { name: 'Завершить прогон', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: 'Сохранить комментарий', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Сохранить комментарий', exact: true })).toHaveCount(0)
  const afterBulk = await api(`/runs/${run.id}`)
  expect(afterBulk.status).toBe('running')
  expect(afterBulk.attempts).toHaveLength(8)
  expect(afterBulk.stepStates[1].comment).toBe('Другая вкладка')
  await page
    .getByRole('button', { name: 'Завершить прогон', exact: true })
    .click()
  await expect(page.locator('.run-view .page-heading .status')).toHaveText(
    'Пройден',
  )
  run = await api(`/runs/${run.id}`)
  expect(run.status).toBe('passed')
  expect(run.variants).toHaveLength(3)
  expect(run.attempts.filter((a: any) => a.variantId)).toHaveLength(3)
  expect(run.attempts.filter((a: any) => !a.variantId)).toHaveLength(5)
  for (const theme of ['light', 'dark']) {
    await page.getByLabel('Цветовая тема', { exact: true }).selectOption(theme)
    await expect(
      page.locator('.run-view .run-step-result .step-status-icon.passed'),
    ).toHaveCount(5)
    const position = await page
      .locator('.run-view .run-step')
      .first()
      .evaluate((el) => {
        const content = el.querySelector('.run-step-content')!.getBoundingClientRect(),
          status = el
            .querySelector('.step-status-control')!
            .getBoundingClientRect()
        return status.left >= content.right
      })
    expect(position).toBe(true)
  }
  expect((await api(`/test-cases/${doc.id}`)).steps).toEqual(doc.steps)
  await page
    .getByRole('button', { name: 'Экспорт результата', exact: true })
    .click()
  const exportDialog = page.getByRole('dialog', {
    name: 'Экспорт результата',
    exact: true,
  })
  const downloadPromise = page.waitForEvent('download')
  await exportDialog
    .getByRole('button', { name: 'Скачать PDF', exact: true })
    .click()
  const downloaded = await downloadPromise
  const reader = await import('pdfjs-dist/legacy/build/pdf.mjs')
  const loading = reader.getDocument({
    data: new Uint8Array(await readFile((await downloaded.path())!)),
  })
  const pdf = await loading.promise
  let text = ''
  for (let i = 1; i <= pdf.numPages; i++)
    text += (await (await pdf.getPage(i)).getTextContent()).items
      .map((item: any) => item.str || '')
      .join('')
  expect(text.replace(/\s/g, '')).toContain('9007199254740993.125')
  expect(text).toContain('numeric')
  await loading.destroy()
})

test('экспорт: PDF, текст, Markdown, изображения, Word и автономная вставка', async ({
  page,
  context,
}) => {
  test.setTimeout(240_000)
  const { createServer: httpServer } = await import('node:http')
  const { writeFile, unlink } = await import('node:fs/promises')
  const marker = 'КОНЕЦОТЧЕТА98765',
    expectedMarker = 'EXPECTEDONLY753159'
  let sources = 0,
    calls = 0
  const target = httpServer((req, res) => {
    if (req.url === '/token') {
      sources++
      res.setHeader('Content-Type', 'application/json')
      res.end('{"token":"export-derived-secret"}')
      return
    }
    calls++
    res.setHeader('Content-Type', 'application/json')
    res.end(
      '{"amount":9007199254740993123.125,"token":' +
        JSON.stringify(req.headers.authorization || '') +
        ',"body":"<script>never execute</script>","payload":' +
        JSON.stringify(
          Array.from(
            { length: 220 },
            (_, i) =>
              `Длинный ответ ${i + 1}: точные данные для проверки всех частей изображения.`,
          ),
        ) +
        '}',
    )
  })
  await new Promise<void>((r) => target.listen(0, '127.0.0.1', r))
  const address = target.address() as import('node:net').AddressInfo
  const targetURL = `http://127.0.0.1:${address.port}`
  let pasteBrowser: import('@playwright/test').Browser | undefined
  try {
    let env = await api('/environments', 'POST', {
      name: 'Экспорт ' + randomUUID(),
      description: '',
    })
    env = await api(`/environments/${env.id}/variables`, 'PUT', {
      revision: env.revision,
      variables: {},
      secrets: { replace: { TOKEN: 'export-vault-secret' }, clear: [] },
    })
    await api(`/environments/${env.id}/resources`, 'POST', {
      name: 'EXPORT',
      kind: 'http',
      config: { baseUrl: targetURL, headers: [], tls: { mode: 'plaintext' } },
      secrets: { replace: {}, clear: [] },
    })
    const folder = await api('/folders', 'POST', {
      name: 'Отчёты ' + randomUUID(),
    })
    let doc = await api(`/folders/${folder.id}/test-cases`, 'POST', {
      name: 'Русский отчёт экспорта',
    })
    const imageData = await page.evaluate(() => {
      const c = document.createElement('canvas')
      c.width = 40
      c.height = 20
      const x = c.getContext('2d')!
      x.fillStyle = '#abc78b'
      x.fillRect(0, 0, 40, 20)
      return c.toDataURL('image/png').split(',')[1]
    })
    const image = await api(`/test-cases/${doc.id}/images`, 'POST', {
      filename: 'reference.png',
      dataBase64: imageData,
    })
    const blockId = randomUUID(),
      stepId = randomUUID()
    doc = await api(`/test-cases/${doc.id}`, 'PUT', {
      name: doc.name,
      revision: doc.revision,
      steps: [
        {
          id: stepId,
          title: 'Первый шаг',
          description: Array.from(
            { length: 140 },
            (_, i) => `Строка ${i + 1}: проверка длинного русского отчёта.`,
          ).join('\n'),
          expected: {
            text: expectedMarker,
            images: [{ imageId: image.id, caption: expectedMarker }],
          },
          blocks: [
            {
              id: blockId,
              kind: 'http',
              resource: 'EXPORT',
              timeoutSeconds: 30,
              http: {
                method: 'GET',
                path: '/main',
                query: [],
                headers: [
                  {
                    id: randomUUID(),
                    name: 'Authorization',
                    source: {
                      method: 'GET',
                      url: targetURL + '/token',
                      query: [],
                      headers: [],
                      bodyMode: 'none',
                      body: '',
                      tls: { mode: 'plaintext' },
                      jsonPointer: '/token',
                      prefix: 'Bearer ',
                      fetchMode: 'per_run',
                    },
                  },
                ],
                bodyMode: 'none',
                body: '',
              },
              expected: {
                statusCode: 200,
                bodyMode: 'none',
                images: [{ imageId: image.id, caption: expectedMarker }],
              },
            },
          ],
        },
        {
          id: randomUUID(),
          title: 'Последний шаг',
          description: marker + ' <img src=x onerror=alert(1)> ```',
          blocks: [],
        },
      ],
    })
    let run = await api(
      `/test-cases/${doc.id}/runs`,
      'POST',
      { environmentId: env.id, revision: doc.revision, executeAll: true },
      randomUUID(),
    )
    await eventually(async () => {
      run = await api(`/runs/${run.id}`)
      return run.batches[0].state === 'completed'
    })
    let v = await api(`/runs/${run.id}/blocks/${blockId}/variants`, 'POST', {
      name: 'Исследование',
    })
    await api(
      `/runs/${run.id}/variants/${v.id}/attempts`,
      'POST',
      { revision: v.revision },
      randomUUID(),
    )
    await eventually(async () => {
      run = await api(`/runs/${run.id}`)
      return (
        run.attempts.length === 2 &&
        run.attempts.every((a: any) => a.finishedAt)
      )
    })
    v = await api(`/runs/${run.id}/variants/${v.id}`, 'PATCH', { name: v.name, revision: v.revision, request: v.versions[0].request })
    await api(`/runs/${run.id}/variants/${v.id}/attempts`, 'POST', { revision: v.revision }, randomUUID())
    await eventually(async () => {
      run = await api(`/runs/${run.id}`)
      return run.attempts.length === 3 && run.attempts.every((a: any) => a.finishedAt)
    })
    expect(sources).toBe(1)
    expect(calls).toBe(3)
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (text: string) => {
            ;(window as any).__copied = text
          },
        },
      })
    })
    await page.goto('/')
    await page
      .getByRole('button', {
        name: 'Папка ' + folder.name,
        exact: true,
      })
      .click()
      .catch(async () => {
        await page.getByText(folder.name, { exact: true }).first().click()
      })
    await page
      .getByRole('button', {
        name: 'Открыть тест-кейс ' + doc.name,
        exact: true,
      })
      .click()
    await page
      .getByRole('button', { name: 'Экспорт результата', exact: true })
      .click()
    const dialog = page.getByRole('dialog', {
      name: 'Экспорт результата',
      exact: true,
    })
    await expect(dialog.getByText(/Частичный отчёт/)).toBeVisible()
    const reader = await import('pdfjs-dist/legacy/build/pdf.mjs')
    for (const include of [true, false]) {
      const report = await api(`/runs/${run.id}/report?includeExpected=${include}`)
      const researchIds = new Set([
        ...run.variants.flatMap((v: any) => v.versions.map((version: any) => 'version:' + version.id)),
        ...run.attempts.filter((a: any) => a.variantId).map((a: any) => 'attempt:' + a.id),
      ])
      const nodes = report.document.filter((node: any) => researchIds.has(node.callId))
      expect(nodes.length).toBeGreaterThan(4)
      expect(JSON.stringify(nodes)).not.toContain(expectedMarker)
      expect(nodes.some((node: any) => node.kind === 'image' || /Ожидалось|Сравнение:|Ожидаемый результат/.test(node.text))).toBe(false)
      expect(report.steps[0].calls[0].variants[0].versions).toHaveLength(2)
      expect(report.steps[0].calls[0].variants[0].attempts).toHaveLength(2)
      await dialog
        .getByLabel('Включать ожидаемые результаты')
        .setChecked(include)
      await dialog.getByLabel('Формат экспорта').selectOption('pdf')
      const downloadPromise = page.waitForEvent('download')
      await dialog
        .getByRole('button', { name: 'Скачать PDF', exact: true })
        .click()
      const download = await downloadPromise
      expect(download.suggestedFilename()).toMatch(/\.pdf$/)
      const path = await download.path()
      const loading = reader.getDocument({
        data: new Uint8Array(await readFile(path!)),
        useSystemFonts: false,
      })
      const pdf = await loading.promise
      expect(pdf.numPages).toBeGreaterThan(3)
      let text = '',
        images = 0
      for (let index = 1; index <= pdf.numPages; index++) {
        const p = await pdf.getPage(index)
        text += (await p.getTextContent()).items
          .map((i: any) => i.str || '')
          .join('')
        const ops = await p.getOperatorList()
        images += ops.fnArray.filter(
          (fn: number) => fn === reader.OPS.paintImageXObject,
        ).length
      }
      expect(text).toContain('Русский')
      expect(text.replace(/\s/g, '')).toContain('9007199254740993123.125')
      expect(text).toContain(marker)
      expect(text.includes(expectedMarker)).toBe(include)
      expect(images > 0).toBe(include)
      expect(text).not.toContain('export-derived-secret')
      expect(text).not.toContain('export-vault-secret')
      await loading.destroy()
      for (const format of ['text', 'markdown']) {
        await dialog.getByLabel('Формат экспорта').selectOption(format)
        await dialog
          .getByRole('button', { name: 'Скопировать', exact: true })
          .click()
        const text = await page.evaluate(
          () => (window as any).__copied as string,
        )
        expect(text).toContain(marker)
        expect(text).toContain('9007199254740993123.125')
        expect(text.includes(expectedMarker)).toBe(include)
        expect(text).toContain('Исследовательский')
        expect(text).not.toContain('export-derived-secret')
      }
    }
    // Clipboard rejection provides selectable data and never announces success.
    await page.evaluate(() => {
      ;(navigator.clipboard as any).writeText = async () => {
        throw new Error('denied')
      }
    })
    await dialog.getByLabel('Формат экспорта').selectOption('text')
    await dialog
      .getByRole('button', { name: 'Скопировать', exact: true })
      .click()
    await expect(dialog.getByLabel('Текст для копирования')).toBeVisible()
    await expect(dialog.getByRole('alert')).toContainText('недоступен')
    await dialog.getByRole('button', { name: 'Закрыть', exact: true }).click()
    for (const s of run.stepStates)
      await api(`/runs/${run.id}/steps/${s.stepId}`, 'PATCH', {
        status: 'passed',
        comment: 'Редактируемый русский комментарий',
        revision: s.revision,
      })
    run = await api(`/runs/${run.id}/finish`, 'POST')
    await page
      .getByRole('button', { name: 'Экспорт результата', exact: true })
      .click()
    // Capture the UI's prepared command; the isolated STA helper publishes it,
    // tests real browser/Word paste, stops only this test app, and restores clipboard.
    let payload: any
    pasteBrowser = await chromium.launch({
      headless: false,
      args: ['--window-position=-32000,-32000', '--window-size=300,300'],
    })
    const pastePage = await pasteBrowser.newPage()
    await pastePage.setContent(
      '<div id="paste-target" contenteditable="true" style="white-space:pre-wrap"></div>',
    )
    await page.route('**/report/clipboard', async (route) => {
      payload = route.request().postDataJSON()
      await route.fulfill({ json: { copied: true, native: true } })
    })
    for (const include of [true, false]) {
      payload = undefined
      await dialog
        .getByLabel('Включать ожидаемые результаты')
        .setChecked(include)
      await dialog.getByLabel('Формат экспорта').selectOption('rich')
      await dialog
        .getByRole('button', { name: 'Скопировать', exact: true })
        .click()
      await expect.poll(() => payload?.parts?.length || 0).toBeGreaterThan(2)
      expect(
        payload.parts.some(
          (part: any) => part.groupId.startsWith('attempt:') && part.total > 1,
        ),
      ).toBe(true)
      const prefix = resolve(root, '.local/word-' + randomUUID()),
        payloadPath = prefix + '.json',
        ready = prefix + '.ready',
        done = prefix + '.done'
      await writeFile(
        payloadPath,
        JSON.stringify({
          url: base + `/api/v1/runs/${run.id}/report/clipboard`,
          csrf,
          body: payload,
          expectedText: 'Редактируемый русский комментарий',
        }),
      )
      const child = spawn(
        'powershell.exe',
        [
          '-NoProfile',
          '-STA',
          '-ExecutionPolicy',
          'Bypass',
          '-File',
          resolve(root, 'scripts/test-word-paste.ps1'),
          '-PayloadPath',
          payloadPath,
          '-ApplicationPid',
          String(app.pid),
          '-BinaryPath',
          process.env.QAG_E2E_BINARY || resolve(root, '.local/verify/qag.exe'),
          '-ReadyPath',
          ready,
          '-DonePath',
          done,
        ],
        { cwd: root, windowsHide: true },
      )
      let output = ''
      child.stdout.on('data', (v) => {
        output += String(v)
      })
      child.stderr.on('data', (v) => {
        output += String(v)
      })
      const closed = new Promise<number | null>((r) => child.once('close', r))
      let pasted = false
      let pasteError: unknown
      try {
        await eventually(async () => {
          await access(ready)
          return true
        })
        await pastePage.evaluate(() => {
          document.addEventListener(
            'paste',
            (e) => {
              ;(window as any).__pasteMeta = {
                types: [...(e.clipboardData?.types || [])],
                htmlLength: e.clipboardData?.getData('text/html').length || 0,
              }
            },
            { once: true },
          )
          const el = document.getElementById('paste-target')!
          el.innerHTML = ''
          el.focus()
        })
        await pastePage.locator('#paste-target').press('Control+V')
        await expect(pastePage.locator('#paste-target img')).toHaveCount(
          payload.parts.length,
        )
        await expect(pastePage.locator('#paste-target')).toContainText(
          'Редактируемый русский комментарий',
        )
        pasted = await pastePage
          .locator('#paste-target img')
          .evaluateAll((images) =>
            images.every((i) =>
              (i as HTMLImageElement).src.startsWith('data:image/png;base64,'),
            ),
          )
      } catch (e) {
        pasteError = e
        console.log(
          'Clipboard paste metadata:',
          await pastePage.evaluate(() => (window as any).__pasteMeta),
        )
      } finally {
        await writeFile(done, 'done')
        const exit = await closed
        expect(exit, output).toBe(0)
        await startApp()
        await Promise.all(
          [unlink(payloadPath), unlink(ready), unlink(done)].map((p) =>
            p.catch(() => {}),
          ),
        )
      }
      if (pasteError) throw pasteError
      expect(pasted).toBe(true)
      await expect(
        api(`/runs/${run.id}/report/clipboard`, 'POST', payload),
      ).rejects.toThrow('409')
      await dialog
        .getByRole('button', { name: 'Подготовить заново', exact: true })
        .click()
    }
    await api(`/test-cases/${doc.id}`, 'PATCH', { archived: true })
    await api(`/folders/${folder.id}`, 'PATCH', { archived: true })
    const archived = await api(`/runs/${run.id}/report?includeExpected=false`)
    expect(archived.partial).toBe(false)
    expect(JSON.stringify(archived)).not.toContain(expectedMarker)
    expect(calls).toBe(3)
    expect(sources).toBe(1)
  } finally {
    await pasteBrowser?.close()
    await new Promise<void>((r) => target.close(() => r()))
  }
})

test('восстановление очереди после источника и SQL не повторяет сеть', async ({
  page,
}) => {
  const folder = await api('/folders', 'POST', {
    name: 'Очередь восстановления ' + randomUUID(),
  })
  for (const protocol of ['source', 'sql']) {
    const env = await api('/environments', 'POST', {
      name: 'Recovery ' + protocol + ' ' + randomUUID(),
      description: '',
    })
    await api(
      `/environments/${env.id}/resources`,
      'POST',
      protocol === 'source'
        ? {
            name: 'TARGET',
            kind: 'http',
            config: {
              baseUrl: config.http + '/api',
              headers: [],
              tls: { mode: 'plaintext' },
            },
            secrets: { replace: {}, clear: [] },
          }
        : {
            name: 'TARGET',
            kind: 'postgresql',
            config: {
              address: config.target.address,
              database: config.target.database,
              username: config.target.username,
              headers: [],
              tls: { mode: 'plaintext' },
            },
            secrets: {
              replace: { PASSWORD: config.target.password },
              clear: [],
            },
          },
    )
    let doc = await api(`/folders/${folder.id}/test-cases`, 'POST', {
      name: 'Recovery ' + protocol,
    })
    const seq = `"${config.targetSchema}"."effect_sequence"`
    const block = (second = false) =>
      protocol === 'source'
        ? {
            id: randomUUID(),
            kind: 'http',
            resource: 'TARGET',
            timeoutSeconds: 120,
            http: {
              method: 'POST',
              path: '/task',
              query: [],
              headers: second
                ? []
                : [
                    {
                      id: randomUUID(),
                      name: 'Authorization',
                      source: {
                        method: 'GET',
                        url: config.http + '/token?delay=1',
                        query: [],
                        headers: [],
                        bodyMode: 'none',
                        body: '',
                        tls: { mode: 'plaintext' },
                        jsonPointer: '/tokens/0/value',
                        fetchMode: 'per_run',
                      },
                    },
                  ],
              bodyMode: 'none',
              body: '',
            },
            expected: { statusCode: 200, bodyMode: 'none' },
          }
        : {
            id: randomUUID(),
            kind: 'postgresql',
            resource: 'TARGET',
            timeoutSeconds: 120,
            sql: {
              mode: 'rows',
              query: second
                ? `SELECT nextval('${seq}')::text AS effect`
                : `WITH effect AS MATERIALIZED (SELECT nextval('${seq}') AS n) SELECT n::text AS effect, pg_sleep(60)::text AS pause FROM effect`,
              parameters: [],
            },
            expected: { bodyMode: 'rows', columns: [], rows: [] },
          }
    const step = randomUUID()
    doc = await api(`/test-cases/${doc.id}`, 'PUT', {
      name: doc.name,
      revision: doc.revision,
      steps: [
        {
          id: step,
          description: 'Сохранить ручную оценку',
          blocks: [block(), block(true)],
        },
      ],
    })
    const httpBefore = await (await fetch(config.http + '/stats')).json(),
      sqlBefore = await (await fetch(config.sqlEffectURL)).json()
    let run = await api(
      `/test-cases/${doc.id}/runs`,
      'POST',
      { environmentId: env.id, revision: doc.revision, executeAll: true },
      randomUUID(),
    )
    await api(`/runs/${run.id}/steps/${step}`, 'PATCH', {
      status: 'failed',
      comment: 'До остановки очереди',
      revision: 1,
    })
    await eventually(async () =>
      protocol === 'source'
        ? (await (await fetch(config.http + '/stats')).json()).sources ===
          httpBefore.sources + 1
        : (await (await fetch(config.sqlEffectURL)).json()).effects ===
          sqlBefore.effects + 1,
    )
    await page.goto('/')
    await page.getByRole('button', { name: 'Папка ' + folder.name, exact: true }).click()
    await page.getByRole('button', { name: 'Открыть тест-кейс ' + doc.name, exact: true }).click()
    await expect(page.locator('.batch-progress')).toContainText('выполняется')
    const bulk = page.getByRole('button', { name: 'Пометить все пройденным', exact: true })
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    await page.route('**/steps/pass-all', async route => { await gate; await route.continue() }, { times: 1 })
    await bulk.click()
    await expect(bulk).toBeDisabled()
    release()
    await expect(page.getByLabel('Статус шага 1', { exact: true })).toHaveValue('passed')
    await expect(page.getByLabel('Комментарий к шагу 1', { exact: true })).toHaveValue('До остановки очереди')
    await expect(page.getByRole('button', { name: 'Отменить прогон', exact: true })).toBeEnabled()
    await expect(page.getByRole('button', { name: 'Завершить прогон', exact: true })).toBeDisabled()
    run = await api(`/runs/${run.id}`)
    expect(run.status).toBe('running')
    expect(run.attempts).toHaveLength(1)
    await stopApp()
    await startApp()
    run = await api(`/runs/${run.id}`)
    expect(run.status).toBe('interrupted')
    expect(run.batches[0].state).toBe('interrupted')
    expect(
      run.batches[0].items.every((i: any) => i.state === 'interrupted'),
    ).toBe(true)
    expect(run.attempts).toHaveLength(1)
    expect(run.attempts[0].state).toBe('interrupted')
    expect(run.stepStates[0].comment).toBe('До остановки очереди')
    expect(run.retentionReasons).toContain('first_run')
    await api(`/runs/${run.id}/report?includeExpected=false`)
    await pause(300)
    const httpAfter = await (await fetch(config.http + '/stats')).json(),
      sqlAfter = await (await fetch(config.sqlEffectURL)).json()
    expect(httpAfter.calls).toBe(httpBefore.calls)
    expect(httpAfter.sources).toBe(
      httpBefore.sources + (protocol === 'source' ? 1 : 0),
    )
    expect(sqlAfter.effects).toBe(
      sqlBefore.effects + (protocol === 'sql' ? 1 : 0),
    )
    if (protocol === 'source') {
      const later = await api(
        `/test-cases/${doc.id}/runs`,
        'POST',
        { environmentId: env.id, revision: doc.revision, executeAll: true },
        randomUUID(),
      )
      await api(`/runs/${later.id}/cancel`, 'POST')
      await api(`/runs/${later.id}`, 'DELETE')
      await expect(api(`/runs/${later.id}`)).rejects.toThrow('404')
    }
  }
  const env = await api('/environments', 'POST', {
    name: 'Manual-only ' + randomUUID(),
    description: '',
  })
  let manual = await api(`/folders/${folder.id}/test-cases`, 'POST', {
    name: 'Ручной без вызовов',
  })
  manual = await api(`/test-cases/${manual.id}`, 'PUT', {
    name: manual.name,
    revision: manual.revision,
    steps: [{ id: randomUUID(), description: 'Осмотреть вручную', blocks: [] }],
  })
  const run = await api(
    `/test-cases/${manual.id}/runs`,
    'POST',
    { environmentId: env.id, revision: manual.revision, executeAll: true },
    randomUUID(),
  )
  expect(run.batches[0].state).toBe('completed')
  expect(run.batches[0].items).toHaveLength(0)
  expect(run.stepStates[0].status).toBe('not_run')
  await page.goto('/')
  await page
    .getByRole('button', { name: 'Папка ' + folder.name, exact: true })
    .click()
  await page
    .getByRole('button', {
      name: 'Открыть тест-кейс ' + manual.name,
      exact: true,
    })
    .click()
  await expect(page.locator('.batch-progress')).toContainText('Выполнено 0/0')
  await page
    .getByRole('button', { name: 'Отменить прогон', exact: true })
    .click()
  await expect(page.locator('.run-view .page-heading .status')).toHaveText(
    'Отменён',
  )
})
