import type { components } from './generated/api'

export type Folder = components['schemas']['Folder']
export type TestCase = components['schemas']['TestCase']
export type DemoInfo = components['schemas']['DemoInfo']
export type Step = components['schemas']['Step']
export type Block = components['schemas']['Block']
export type KeyValue = components['schemas']['KeyValue']
export type RequestHeader = components['schemas']['RequestHeader']
export type HTTPHeaderSource = components['schemas']['HTTPHeaderSource']
export type ImageRef = components['schemas']['ImageRef']
export type ExpectedImage = components['schemas']['ExpectedImage']
export type TypedValue = components['schemas']['TypedValue']
export type Column = components['schemas']['Column']
export type Environment = components['schemas']['Environment']
export type Resource = components['schemas']['Resource']
export type ResourceConfig = components['schemas']['ResourceConfig']
export type SecretChanges = components['schemas']['SecretChanges']
export type Run = components['schemas']['Run']
export type Attempt = components['schemas']['Attempt']
export type StepState = components['schemas']['StepState']
export type Expectation = components['schemas']['Expectation']
export type APIError = components['schemas']['APIError']
export type Page<T> = { items: T[]; nextCursor?: string }

export class RequestError extends Error {
  constructor(
    public status: number,
    public detail: APIError,
  ) {
    super(detail.message)
  }
}

let session: Promise<string> | undefined
async function token() {
  session ??= fetch('/api/v1/session', {
    cache: 'no-store',
    credentials: 'same-origin',
  })
    .then(async (r) => {
      if (!r.ok) throw new Error('Локальная сессия недоступна')
      return ((await r.json()) as { csrfToken: string }).csrfToken
    })
    .catch((error) => {
      session = undefined
      throw error
    })
  return session
}

export async function request<T>(
  path: string,
  method = 'GET',
  body?: unknown,
  idempotencyKey?: string,
): Promise<T> {
  const headers: Record<string, string> = {}
  if (method !== 'GET') {
    headers['Content-Type'] = 'application/json'
    headers['X-QAG-CSRF'] = await token()
  }
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey
  let response: Response
  try {
    response = await fetch(`/api/v1${path}`, {
      method,
      headers,
      body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
      cache: 'no-store',
      credentials: 'same-origin',
    })
  } catch {
    throw new Error(
      'Ответ приложения не получен. Проверьте его работу и повторите получение результата.',
    )
  }
  if (!response.ok) {
    const detail = (await response.json().catch(() => ({
      code: 'request_failed',
      message: 'Не удалось выполнить действие',
      requestId: '',
    }))) as APIError
    if (response.status === 403) session = undefined
    throw new RequestError(response.status, detail)
  }
  return response.json() as Promise<T>
}

// Preserve a command key across network failures; a retry must retrieve the
// same command, rather than accidentally repeat a request with side effects.
export async function command<T>(path: string, body?: unknown): Promise<T> {
  const fingerprint = `qag.command:${path}:${JSON.stringify(body ?? {})}`
  const id = sessionStorage.getItem(fingerprint) ?? crypto.randomUUID()
  sessionStorage.setItem(fingerprint, id)
  try {
    const result = await request<T>(path, 'POST', body, id)
    sessionStorage.removeItem(fingerprint)
    return result
  } catch (error) {
    if (
      error instanceof RequestError &&
      error.status < 500 &&
      error.detail.code !== 'invalid_session' &&
      error.status !== 408 &&
      error.status !== 429
    )
      sessionStorage.removeItem(fingerprint)
    throw error
  }
}

export async function all<T>(path: string): Promise<T[]> {
  const result: T[] = []
  let cursor = ''
  do {
    const query = new URLSearchParams({ limit: '100' })
    if (cursor) query.set('cursor', cursor)
    const page = await request<Page<T>>(
      `${path}${path.includes('?') ? '&' : '?'}${query}`,
    )
    result.push(...page.items)
    cursor = page.nextCursor ?? ''
  } while (cursor)
  return result
}

export function message(error: unknown) {
  return error instanceof Error
    ? error.message
    : 'Не удалось выполнить действие'
}
export function fieldErrors(error: unknown): Record<string, string> {
  return error instanceof RequestError ? (error.detail.fieldErrors ?? {}) : {}
}
export function newBlock(kind: Block['kind']): Block {
  const base = {
    id: crypto.randomUUID(),
    kind,
    resource: '',
    timeoutSeconds: 30,
  }
  if (kind === 'http')
    return {
      ...base,
      http: {
        method: 'GET',
        path: '/',
        query: [],
        headers: [],
        bodyMode: 'none',
        body: '',
      },
      expected: { statusCode: 200, bodyMode: 'json', body: '{}' },
    }
  if (kind === 'grpc')
    return {
      ...base,
      grpc: { service: '', method: '', body: '{}', metadata: [] },
      expected: { grpcCode: 'OK', bodyMode: 'json', body: '{}' },
    }
  return {
    ...base,
    sql: { query: '', parameters: [], mode: 'rows' },
    expected: { bodyMode: 'rows', columns: [], rows: [] },
  }
}
export const statusLabel: Record<string, string> = {
  queued: 'Подготовка',
  running: 'Выполняется',
  completed: 'Ответ получен',
  error: 'Ошибка',
  timed_out: 'Тайм-аут',
  cancelled: 'Отменён',
  interrupted: 'Прерван',
  not_run: 'Не проверен',
  passed: 'Пройден',
  failed: 'Не пройден',
  blocked: 'Заблокирован',
  skipped: 'Пропущен',
  match: 'Совпадает',
  mismatch: 'Есть различия',
  not_compared: 'Не сравнено',
}
export const reasonLabel: Record<string, string> = {
  first_run: 'Первый прогон',
  first_success: 'Первый успешный',
  manual: 'Сохранён вручную',
}
export const protocolLabel: Record<string, string> = {
  http: 'HTTP',
  grpc: 'gRPC',
  postgresql: 'PostgreSQL',
}
export function date(value: string) {
  return new Intl.DateTimeFormat('ru-RU', {
    dateStyle: 'short',
    timeStyle: 'short',
  }).format(new Date(value))
}
