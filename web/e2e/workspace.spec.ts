import { test, expect, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { readFile, access } from 'node:fs/promises'
import { createWriteStream } from 'node:fs'
import { resolve } from 'node:path'
import { randomUUID } from 'node:crypto'

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
  app = spawn(resolve(root, 'dist/qag.exe'), [], {
    cwd: root,
    windowsHide: true,
    env: { ...process.env, QAG_ADDRESS: '127.0.0.1:18080', QAG_DATABASE_URL: config.databaseUrl },
  })
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
  fixture.stderr?.pipe(createWriteStream(resolve(root, '.local/e2e-fixture.log')))
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
    await Promise.race([new Promise((r) => fixture.once('close', r)), pause(5000)])
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
  await form.getByLabel('Защита подключения', { exact: true }).selectOption('plaintext')
  if (kind === 'http')
    await form.getByLabel('Базовый URL', { exact: false }).fill(config.http + '/api')
  if (kind === 'grpc') await form.getByLabel('Адрес сервера', { exact: true }).fill(config.grpc)
  if (kind === 'postgresql') {
    await form.getByLabel('Адрес сервера', { exact: true }).fill(config.target.address)
    await form.getByLabel('База данных', { exact: true }).fill(config.target.database)
    await form.getByLabel('Пользователь PostgreSQL', { exact: true }).fill(config.target.username)
    await form.getByLabel('Действие: PASSWORD', { exact: true }).selectOption('replace')
    await form.getByLabel('Новое значение: PASSWORD', { exact: false }).fill(config.target.password)
  }
  await form.getByRole('button', { name: 'Сохранить ресурс', exact: true }).click()
  await expect(page.locator('.resource-item').filter({ hasText: name })).toBeVisible()
}
async function executeAll(page: Page) {
  const calls = page.locator('.run-view .call-block')
  for (let i = 0; i < 3; i++) {
    await calls.nth(i).getByRole('button', { name: 'Запустить вызов', exact: true }).click()
    await expect(calls.nth(i).getByText('Совпадает', { exact: true }).first()).toBeVisible()
  }
}

test('каталог → три протокола → история → архив → перезапуск', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  await page.goto('/')
  await page.getByRole('button', { name: 'Окружения и ресурсы', exact: false }).click()
  await page.getByRole('button', { name: 'Создать окружение', exact: true }).click()
  await page.getByLabel('Имя окружения', { exact: true }).fill('dev')
  await page.getByRole('button', { name: 'Сохранить окружение', exact: true }).click()
  for (const [name, kind] of [
    ['AI_GW_SERVISE', 'http'],
    ['HEALTH', 'grpc'],
    ['TARGET', 'postgresql'],
  ])
    await addResource(page, name, kind)
  await page.getByRole('button', { name: 'Тест-кейсы', exact: false }).first().click()
  await page.locator('aside').getByRole('button', { name: 'Создать папку', exact: true }).click()
  await page.getByLabel('Название', { exact: true }).fill('Проверки API')
  await page.getByRole('button', { name: 'Сохранить название', exact: true }).click()
  await page.getByRole('button', { name: 'Добавить тест-кейс в Проверки API', exact: true }).click()
  await page.getByLabel('Название', { exact: true }).fill('Смешанный сценарий')
  await page.getByRole('button', { name: 'Сохранить название', exact: true }).click()
  await page.getByRole('button', { name: '+ Добавить шаг', exact: true }).click()
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
  await blocks.nth(1).getByLabel('Ресурс', { exact: true }).fill('HEALTH')
  await blocks.nth(1).getByLabel('Сервис gRPC', { exact: true }).fill('grpc.health.v1.Health')
  await blocks.nth(1).getByLabel('Метод gRPC', { exact: true }).fill('Check')
  await blocks.nth(1).getByLabel('Ожидаемое тело', { exact: true }).fill('{"status":"SERVING"}')
  const sql = blocks.nth(2)
  await sql.getByLabel('Ресурс', { exact: true }).fill('TARGET')
  await sql.getByLabel('SQL-запрос', { exact: false }).fill('SELECT $1::numeric AS amount')
  await sql.getByRole('button', { name: '+ Параметр', exact: true }).click()
  await sql.getByLabel('Параметр $1: тип', { exact: true }).selectOption('decimal')
  await sql.getByLabel('Параметр $1', { exact: true }).fill('9007199254740993123.125')
  await sql.getByRole('button', { name: '+ Колонка', exact: true }).click()
  await sql.getByLabel('Ожидаемая колонка 1', { exact: true }).fill('amount')
  await sql.getByLabel('PostgreSQL-тип колонки 1', { exact: true }).fill('numeric')
  await sql.getByRole('button', { name: '+ Ожидаемая строка', exact: true }).click()
  await sql.getByLabel('Строка 1, колонка 1: тип', { exact: true }).selectOption('decimal')
  await sql.getByLabel('Строка 1, колонка 1', { exact: true }).fill('9007199254740993123.125')
  await sql.getByLabel('Параметр $1', { exact: true }).fill('wrong')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(sql.locator('.parameter-row [role="alert"]')).toBeVisible()
  await expect(sql.getByLabel('Параметр $1', { exact: true })).toHaveValue('wrong')
  await sql.getByLabel('Параметр $1', { exact: true }).fill('9007199254740993123.125')
  await blocks.nth(0).getByLabel('Ожидаемое тело', { exact: true }).fill('{broken')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(blocks.nth(0).getByLabel('Ожидаемое тело', { exact: true })).toHaveAttribute(
    'aria-invalid',
    'true',
  )
  await blocks
    .nth(0)
    .getByLabel('Ожидаемое тело', { exact: true })
    .fill('{"ok":true,"number":9007199254740993123,"token":""}')
  // Navigating with unsaved input offers a real choice and preserves the draft.
  await page.getByRole('button', { name: 'Окружения и ресурсы', exact: false }).click()
  await expect(page.getByRole('dialog', { name: 'Несохранённые изменения' })).toBeVisible()
  await page.getByRole('button', { name: 'Остаться', exact: true }).click()
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  const folders = await api('/folders'),
    folder = folders.items[0],
    cases = await api(`/folders/${folder.id}/test-cases`),
    c = cases.items[0]
  expect(c.steps[0].blocks.map((b: any) => b.kind)).toEqual(['http', 'grpc', 'postgresql'])
  // Editor trials create attempts without creating a run.
  await blocks.nth(0).getByRole('button', { name: 'Запустить вызов', exact: true }).click()
  await expect(blocks.nth(0).getByText('Совпадает', { exact: true })).toBeVisible()
  expect((await api(`/test-cases/${c.id}/runs`)).items).toHaveLength(0)
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await executeAll(page)
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('failed')
  await page.getByRole('button', { name: 'Завершить прогон', exact: true }).click()
  await expect(page.getByText('Первый прогон', { exact: true })).toBeVisible()
  const first = (await api(`/test-cases/${c.id}/runs`)).items[0]
  expect(first.status).toBe('failed')
  expect(first.retentionReasons).toEqual(['first_run'])
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await page.getByRole('button', { name: 'Начать прогон', exact: true }).click()
  await executeAll(page)
  await page.getByLabel('Комментарий к шагу 1', { exact: true }).fill('Ответы проверены')
  await page.getByRole('button', { name: 'Сохранить комментарий', exact: true }).click()
  await page.getByLabel('Статус шага 1', { exact: true }).selectOption('passed')
  await expect(page.getByLabel('Статус шага 1', { exact: true })).toBeEnabled()
  await page.evaluate(()=>sessionStorage.clear())
  await page.reload()
  await page.getByRole('button', { name: 'Прогон', exact: true }).click()
  await expect(page.getByLabel('Комментарий к шагу 1', { exact: true })).toHaveValue(
    'Ответы проверены',
  )
  await page.getByRole('button', { name: 'Завершить прогон', exact: true }).click()
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
    const a = await api(`/runs/${third.id}/blocks/${bid}/attempts`, 'POST', {}, randomUUID())
    await eventually(
      async () => !['queued', 'running'].includes((await api(`/attempts/${a.id}`)).state),
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
  await expect(page.getByText('Сохранён вручную', { exact: true })).toBeVisible()
  // Current edits cannot change the retained scenario snapshot.
  await page.getByRole('button', { name: 'Редактор 1', exact: true }).click()
  await page.getByLabel('Описание шага 1', { exact: true }).fill('Новое описание')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  expect((await api(`/runs/${first.id}`)).snapshot.steps[0].description).toBe(
    c.steps[0].description,
  )
  const folderToggle = page.getByRole('button', { name: 'Папка Проверки API', exact: true })
  if ((await folderToggle.getAttribute('aria-expanded')) === 'false') await folderToggle.click()
  await page
    .getByRole('button', { name: 'Архивировать тест-кейс Смешанный сценарий', exact: true })
    .click()
  await expect(page.locator('.case-row.archived')).toContainText('Архив')
  await page.getByRole('button', { name: 'Архивировать папку Проверки API', exact: true }).click()
  await page.getByRole('button', { name: 'Архив', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Папка Проверки API', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Восстановить папку Проверки API', exact: true }).click()
  await page.getByRole('button', { name: 'Активные', exact: true }).click()
  await expect(page.locator('.case-row.archived')).toContainText('Смешанный сценарий')
  await stopApp()
  await startApp()
  await page.reload()
  await expect(page.getByText('Архив · только просмотр', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await expect(page.locator('.history-table tbody tr')).toHaveCount(3)
  await page.screenshot({ path: resolve(root, '.local/workspace.png'), fullPage: true })
  expect(errors).toEqual([])
  expect(logs).not.toContain(config.target.password)
})

test('остановка процесса не повторяет внешний эффект и сохраняет ручной шаг', async ({ page }) => {
  let env = (await api('/environments')).items[0]
  if (!env) {
    env = await api('/environments', 'POST', {
      name: 'recovery',
      variables: {},
      secrets: { replace: {}, clear: [] },
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
  let c = await api(`/folders/${folder.id}/test-cases`, 'POST', { name: 'Прерванный вызов' })
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
              path: '/slow',
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
  const before = (await (await fetch(config.http + '/stats')).json()).calls
  const a = await api(`/runs/${run.id}/blocks/${block}/attempts`, 'POST', {}, randomUUID())
  await eventually(
    async () => (await (await fetch(config.http + '/stats')).json()).calls === before + 1,
  )
  await stopApp()
  await startApp()
  const restored = await api(`/runs/${run.id}`)
  expect(restored.status).toBe('interrupted')
  expect(restored.stepStates[0].comment).toBe('До остановки')
  expect(restored.attempts[0].id).toBe(a.id)
  expect(restored.attempts[0].state).toBe('interrupted')
  expect((await (await fetch(config.http + '/stats')).json()).calls).toBe(before + 1)
  await page.goto('/')
  await page.evaluate((id) => localStorage.setItem('qag.case', id), c.id)
  await page.reload()
  await page.getByRole('button', { name: 'История', exact: true }).click()
  await expect(page.getByText('Прерван', { exact: true })).toBeVisible()
})

test('сортировка, конфликт редактора, секреты и повтор потерянной команды', async ({ page }) => {
  const folder = await api('/folders', 'POST', { name: 'Порядок и конфликты' })
  const names = ['Ядро', 'бета', 'Альфа', 'Beta', 'alpha'],
    created = []
  for (const name of names)
    created.push(await api(`/folders/${folder.id}/test-cases`, 'POST', { name }))
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
  const rename = await api(`/test-cases/${created[0].id}`, 'PATCH', { name: 'Авангард' })
  await api(`/test-cases/${rename.id}`, 'PATCH', { archived: true })
  expect((await api(`/folders/${folder.id}/test-cases`)).items.at(-1).id).toBe(rename.id)
  const env = await api('/environments', 'POST', {
    name: 'security',
    variables: {},
    secrets: { replace: {}, clear: [] },
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
    variables: {},
    secrets: { replace: {}, clear: [] },
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
  await page.evaluate(
    ({ id, env }) => {
      localStorage.setItem('qag.case', id)
      localStorage.setItem('qag.environment', env)
    },
    { id: c.id, env: env.id },
  )
  await page.reload()
  // Empty manual titles and author order survive moving and reopening.
  await page.getByRole('button', { name: 'Переместить шаг 2 вверх', exact: true }).click()
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue('Второй ручной шаг')
  c = await api(`/test-cases/${c.id}`)
  await api(`/test-cases/${c.id}`, 'PUT', { name: c.name, revision: c.revision, steps: c.steps })
  await page.getByLabel('Описание шага 1', { exact: true }).fill('Мой несохранённый текст')
  await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
  await expect(page.getByRole('alert').first()).toBeVisible()
  await expect(page.getByLabel('Описание шага 1', { exact: true })).toHaveValue(
    'Мой несохранённый текст',
  )
  await page.reload()
  await page.getByRole('button', { name: 'Запустить вызов', exact: true }).click()
  await expect(page.locator('.results').getByText('Есть различия', { exact: true })).toBeVisible()
  expect(await page.locator('.results').textContent()).not.toContain(token)
  // Lose one response after the server accepted it, then explicitly retry.
  const before = (await (await fetch(config.http + '/stats')).json()).calls
  let accepted = ''
  await page.route(`**/test-cases/${c.id}/blocks/${block}/attempts`, async (route) => {
    const response = await route.fetch()
    accepted = (await response.json()).id
    await route.abort('failed')
    await page.unroute(`**/test-cases/${c.id}/blocks/${block}/attempts`)
  })
  await page.getByRole('button', { name: 'Запустить вызов', exact: true }).click()
  await expect(page.getByRole('alert').first()).toBeVisible()
  await page.getByRole('button', { name: 'Запустить вызов', exact: true }).click()
  await expect(page.locator('.results').getByText('Совпадает', { exact: true })).toBeVisible()
  expect((await (await fetch(config.http + '/stats')).json()).calls).toBe(before + 1)
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
    const a = await api(`/runs/${run.id}/blocks/${block}/attempts`, 'POST', {}, randomUUID())
    await eventually(
      async () => !['queued', 'running'].includes((await api(`/attempts/${a.id}`)).state),
    )
  }
  const finished = await api(`/runs/${run.id}/finish`, 'POST')
  expect(finished.status).toBe('passed')
  expect(finished.retentionReasons.sort()).toEqual(['first_run', 'first_success'])
  expect(finished.attempts.map((a: any) => a.result.comparison.status)).toEqual([
    'mismatch',
    'match',
  ])
  expect(logs).not.toContain(token)
  expect(await page.content()).not.toContain(token)
})
