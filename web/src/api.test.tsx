import { afterEach, expect, it, vi } from 'vitest'
import { command } from './api'

afterEach(() => {
  vi.unstubAllGlobals()
  sessionStorage.clear()
})
it('сохраняет один ключ команды после потери ответа и обновления CSRF-сессии', async () => {
  const keys: string[] = []
  let attempt = 0
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path.endsWith('/session'))
        return new Response(JSON.stringify({ csrfToken: 'fresh-token' }), { status: 200 })
      keys.push((init!.headers as Record<string, string>)['Idempotency-Key'])
      attempt++
      if (attempt === 1) throw new TypeError('connection lost')
      if (attempt === 2)
        return new Response(
          JSON.stringify({ code: 'invalid_session', message: 'Обновите сессию' }),
          { status: 403 },
        )
      return new Response(JSON.stringify({ id: 'same-attempt' }), { status: 200 })
    }),
  )
  await expect(command('/test-command')).rejects.toThrow()
  expect(sessionStorage.length).toBe(1)
  await expect(command('/test-command')).rejects.toThrow()
  expect(sessionStorage.length).toBe(1)
  expect(await command('/test-command')).toEqual({ id: 'same-attempt' })
  expect(new Set(keys).size).toBe(1)
  expect(sessionStorage.length).toBe(0)
})
