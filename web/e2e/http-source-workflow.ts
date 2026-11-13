import { expect, type Locator, type Page } from '@playwright/test'
import { createServer } from 'node:http'
import { randomUUID } from 'node:crypto'

function publicConfig(config: any) {
  const copy = structuredClone(config)
  const omitEmpty = (value: any, keys: string[]) => { for (const key of keys) if (value?.[key] === '') delete value[key] }
  const tls = (value: any) => omitEmpty(value, ['ca', 'certificate', 'serverName'])
  const source = (value: any) => { if (value) { tls(value.tls); omitEmpty(value, ['prefix', 'suffix', 'privateKeySecret']) } }
  omitEmpty(copy, ['baseUrl', 'address', 'database', 'username'])
  tls(copy.tls)
  for (const header of copy.headers ?? []) source(header.source)
  for (const definition of Object.values(copy.variables ?? {}) as any[]) source(definition.source)
  return copy
}

export async function verifyHTTPSourceWorkflow(page: Page, base: string, restart: () => Promise<void>) {
  const secret = 'a+b&c= d% проверка'
  const token = 'private-e2e-token'
  let sources = 0, mains = 0, sourceFailure = ''
  const server = createServer(async (request, response) => {
    if (request.url === '/token') {
      sources++
      const chunks: Buffer[] = []
      for await (const chunk of request) chunks.push(Buffer.from(chunk))
      const form = new URLSearchParams(Buffer.concat(chunks).toString())
      if (request.method !== 'POST' || request.headers['content-type'] !== 'application/x-www-form-urlencoded' || form.get('client_secret') !== secret || form.getAll('scope').join(',') !== 'one,two') sourceFailure = 'incorrect form request'
      response.setHeader('Content-Type', 'application/json')
      response.end(JSON.stringify({ access_token: token }))
    } else {
      mains++
      if (request.headers.authorization !== `Bearer ${token}!`) sourceFailure = 'incorrect authorization'
      response.setHeader('Content-Type', 'application/json')
      response.end(JSON.stringify({ echo: token }))
    }
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const sourceURL = `http://127.0.0.1:${(server.address() as { port: number }).port}`
  const api = async (path: string, method = 'GET', body?: unknown) => {
    const session = await (await fetch(base + '/api/v1/session')).json()
    const response = await fetch(base + '/api/v1' + path, { method, headers: { 'Content-Type': 'application/json', Origin: base, 'X-QAG-CSRF': session.csrfToken, 'Idempotency-Key': randomUUID() }, body: method === 'GET' ? undefined : JSON.stringify(body ?? {}) })
    const result = await response.json()
    if (!response.ok) throw new Error(`HTTP source workflow ${response.status}: ${result.message}`)
    return result
  }
  const fillSource = async (scope: Locator, before = '', after = '') => {
    await scope.getByLabel('Источник: метод', { exact: true }).selectOption('POST')
    await scope.getByLabel('Источник: URL', { exact: true }).fill(sourceURL + '/token')
    await scope.getByLabel('Источник: Защита подключения', { exact: true }).selectOption('plaintext')
    await scope.getByLabel('Источник: формат тела', { exact: true }).selectOption('form')
    const fields = scope.getByRole('group', { name: 'Источник: поля формы', exact: true })
    for (const [i, name, value] of [[1, 'client_secret', '${CLIENT_SECRET}'], [2, 'scope', 'one'], [3, 'scope', 'two']] as const) {
      await fields.getByRole('button', { name: '+ Добавить', exact: true }).click()
      await fields.getByLabel(`Источник: поля формы: имя ${i}`, { exact: true }).fill(name)
      await fields.getByLabel(`Источник: поля формы: значение ${i}`, { exact: true }).fill(value)
    }
    await scope.getByLabel('Источник: поле JSON', { exact: true }).fill('/access_token')
    await scope.getByLabel('Источник: текст перед значением', { exact: true }).fill(before)
    await scope.getByLabel('Источник: текст после значения', { exact: true }).fill(after)
    await scope.getByLabel('Источник: когда получать', { exact: true }).selectOption('per_run')
  }
  try {
    let env = await api('/environments', 'POST', { name: `HTTP Sources ${randomUUID()}`, description: '' })
    env = await api(`/environments/${env.id}/variables`, 'PUT', { revision: env.revision, variables: {}, secrets: { replace: { CLIENT_SECRET: secret } } })
    await page.goto(base)
    await page.getByRole('button', { name: /Окружения и ресурсы/ }).click()
    await page.getByLabel('Текущее окружение', { exact: true }).selectOption(env.id)
    await page.getByRole('button', { name: 'Настроить переменные', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Переменные окружения', exact: true })
    const variables = dialog.getByRole('group', { name: 'Переменные окружения', exact: true })
    await variables.getByRole('button', { name: '+ Переменная окружения', exact: true }).click()
    await variables.getByLabel('Переменная окружения: имя 1', { exact: true }).fill('SHARED')
    await variables.getByLabel('Переменная окружения: способ 1', { exact: true }).selectOption('http')
    await fillSource(variables)
    await dialog.getByRole('button', { name: 'Сохранить переменные', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page.locator('.variable-list')).toContainText('SHARED')
    await expect(page.locator('.variable-list')).toContainText('Из HTTP-запроса')
    await page.locator('.resource-list').getByRole('button', { name: '+ Добавить', exact: true }).click()
    const form = page.locator('.resource-form')
    await form.getByLabel('Имя ресурса', { exact: true }).fill('UI_SOURCE')
    await form.getByLabel('Тип ресурса', { exact: true }).selectOption('grpc')
    await form.getByLabel('Адрес сервера', { exact: true }).fill('127.0.0.1:1')
    await form.getByLabel('Защита подключения', { exact: true }).selectOption('tls')
    await form.getByLabel('Имя сервера TLS', { exact: true }).fill('grpc.example.test')
    const headers = form.getByRole('group', { name: 'Общие метаданные', exact: true })
    await headers.getByRole('button', { name: '+ Добавить', exact: true }).click()
    await headers.getByLabel('Общие метаданные: имя 1', { exact: true }).fill('authorization')
    await headers.getByLabel('Общие метаданные: способ значения 1', { exact: true }).selectOption('http')
    await fillSource(headers, 'Token [', ']')
    const locals = form.getByRole('group', { name: 'Переменные ресурса', exact: true })
    await locals.getByRole('button', { name: '+ Переменная ресурса', exact: true }).click()
    await locals.getByLabel('Переменная ресурса: имя 1', { exact: true }).fill('LOCAL')
    await locals.getByLabel('Переменная ресурса: значение 1', { exact: true }).fill('kept')
    await locals.getByRole('button', { name: '+ Переменная ресурса', exact: true }).click()
    await locals.getByLabel('Переменная ресурса: имя 2', { exact: true }).fill('OWN_HTTP')
    await locals.getByLabel('Переменная ресурса: способ 2', { exact: true }).selectOption('http')
    await fillSource(locals, '<', '>')
    await form.getByLabel('Действие: TOKEN', { exact: true }).selectOption('replace')
    await form.getByLabel('Новое значение: TOKEN', { exact: true }).fill('resource-kept-secret')
    await form.getByLabel('Добавить .proto', { exact: true }).setInputFiles({ name: 'test.proto', mimeType: 'text/plain', buffer: Buffer.from('syntax="proto3"; package check; message Check {}') })
    await expect(form.getByText('test.proto', { exact: true })).toBeVisible()
    const submitted = page.waitForRequest(request => request.method() === 'POST' && request.url().endsWith(`/environments/${env.id}/resources`))
    await form.getByRole('button', { name: 'Сохранить ресурс', exact: true }).click()
    const payload = (await submitted).postDataJSON()
    await expect(form.getByRole('heading', { name: 'UI_SOURCE', exact: true })).toBeVisible()
    const saved = (await api(`/environments/${env.id}/resources`)).items.find((item: any) => item.name === 'UI_SOURCE')
    expect(saved.config).toEqual(publicConfig(payload.config))
    expect(saved.secretNames).toContain('TOKEN')
    expect(saved.config.headers[0].source).toMatchObject({ bodyMode: 'form', prefix: 'Token [', suffix: ']', form: payload.config.headers[0].source.form })
    expect(sources).toBe(0)
    await form.getByRole('button', { name: 'Закрыть', exact: true }).click()
    await page.locator('.resource-list').getByRole('button', { name: /UI_SOURCE/ }).click()
    await expect(headers.getByLabel('Источник: текст после значения', { exact: true })).toHaveValue(']')
    await headers.getByLabel('Источник: текст после значения', { exact: true }).fill('] changed')
    await locals.getByLabel('Переменная ресурса: значение 1', { exact: true }).fill('updated')
    await form.getByRole('button', { name: 'Сохранить ресурс', exact: true }).click()
    await expect.poll(async () => (await api(`/environments/${env.id}/resources`)).items.find((item: any) => item.id === saved.id)?.revision).toBe(saved.revision + 1)
    await restart()
    await page.goto(base)
    await page.getByRole('button', { name: /Окружения и ресурсы/ }).click()
    await page.getByLabel('Текущее окружение', { exact: true }).selectOption(env.id)
    await page.locator('.resource-list').getByRole('button', { name: /UI_SOURCE/ }).click()
    await expect(headers.getByLabel('Источник: текст после значения', { exact: true })).toHaveValue('] changed')
    await expect(locals.getByLabel('Переменная ресурса: значение 1', { exact: true })).toHaveValue('updated')
    await expect(form.getByLabel('Имя сервера TLS', { exact: true })).toHaveValue('grpc.example.test')
    await expect(form.getByText('test.proto', { exact: true })).toBeVisible()
    env = (await api('/environments')).items.find((item: any) => item.id === env.id)
    expect(env.httpVariables.SHARED.form).toHaveLength(3)
    expect(sources).toBe(0)
    for (const name of ['HTTP_ONE', 'HTTP_TWO']) await api(`/environments/${env.id}/resources`, 'POST', { name, kind: 'http', config: { baseUrl: sourceURL, tls: { mode: 'plaintext' }, headers: [{ name: 'Authorization', value: 'Bearer ${SHARED}!' }] }, secrets: {} })
    const folder = await api('/folders', 'POST', { name: `HTTP Source ${randomUUID()}` })
    let testCase = await api(`/folders/${folder.id}/test-cases`, 'POST', { name: 'Общий токен' })
    testCase = await api(`/test-cases/${testCase.id}`, 'PUT', { revision: testCase.revision, name: testCase.name, steps: [{ id: randomUUID(), description: 'HTTP-источник из формы', blocks: ['HTTP_ONE', 'HTTP_TWO'].map(resource => ({ id: randomUUID(), kind: 'http', resource, http: { method: 'GET', path: '/target', bodyMode: 'none', body: '', query: [], headers: [] }, expected: { statusCode: 200, bodyMode: 'none', body: '', headers: [] } })) }] })
    let run = await api(`/test-cases/${testCase.id}/runs`, 'POST', { revision: testCase.revision, environmentId: env.id, executeAll: true, historyPolicy: 'unretained' })
    await expect.poll(async () => { run = await api(`/runs/${run.id}`); return run.batches[0]?.state }).toBe('completed')
    expect(sourceFailure).toBe('')
    expect(sources).toBe(1)
    expect(mains).toBe(2)
    expect(run.attempts.every((attempt: any) => attempt.state === 'completed' && attempt.result.redacted)).toBe(true)
    expect(JSON.stringify(run)).not.toContain(token)
    await api(`/runs/${run.id}/cancel`, 'POST')
  } finally { await new Promise<void>(resolve => server.close(() => resolve())) }
}
