import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import * as api from './api'
import type { Environment, HTTPVariableProbe, ProbeView } from './api'
import { newHTTPSource } from './HeaderEditor'
import { ResourceForm, VariablesForm } from './ResourcesPanel'

afterEach(() => vi.restoreAllMocks())
const source = { ...newHTTPSource(), url: 'http://auth.test/token', jsonPointer: '/token', prefix: 'Bearer ' }
const env: Environment = { id: 'env', name: 'Auth', description: '', revision: 2, readOnly: false, variables: { STATIC: 'old' }, httpVariables: { TOKEN_HTTP: source }, secretNames: ['CLIENT_SECRET'] }
const probe = (): HTTPVariableProbe => ({ id: 'probe', scope: 'environment', environmentId: 'env', source, state: 'queued', startedAt: new Date().toISOString(), durationMs: 0, persistencePending: false })
const result = (): ProbeView => ({ probe: { ...probe(), state: 'completed', finishedAt: new Date().toISOString(), logId: 'log' }, valueAvailable: true, extractedValue: '9007199254740993123.125', formattedValue: 'Bearer 9007199254740993123.125', valueType: 'number' })

test('environment probe uses unsaved values and secrets without submitting; changing the draft clears the result', async () => {
  const request = vi.spyOn(api, 'request').mockImplementation(async (path, method, body) => path === '/http-variable-probes' && method === 'POST' ? probe() : path === '/http-variable-probes/probe' ? result() : Promise.reject(new Error(`Unexpected request ${path} ${JSON.stringify(body)}`)))
  const saved = vi.fn(async () => {})
  render(<VariablesForm initial={env} close={vi.fn()} saved={saved} />)
  fireEvent.change(screen.getByLabelText('Переменная окружения: значение 1'), { target: { value: 'draft-value' } })
  fireEvent.change(screen.getByLabelText('Действие: CLIENT_SECRET'), { target: { value: 'replace' } })
  fireEvent.change(screen.getByLabelText('Новое значение: CLIENT_SECRET'), { target: { value: 'draft-password' } })
  fireEvent.click(screen.getByRole('button', { name: 'Проверить получение' }))
  await screen.findByText('9007199254740993123.125')
  expect(request).toHaveBeenCalledWith('/http-variable-probes', 'POST', expect.objectContaining({ scope: 'environment', environmentRevision: 2, variables: { STATIC: 'draft-value' }, secrets: { replace: { CLIENT_SECRET: 'draft-password' }, clear: [] }, source }), expect.any(String))
  expect(saved).not.toHaveBeenCalled()
  expect(request.mock.calls.every(([, method]) => method !== 'PUT')).toBe(true)
  fireEvent.change(screen.getByLabelText('Источник: URL'), { target: { value: 'http://auth.test/changed' } })
  await waitFor(() => expect(screen.queryByText('9007199254740993123.125')).toBeNull())
})

test('new unnamed resource variable can be checked with an incomplete target connection', async () => {
  const request = vi.spyOn(api, 'request').mockImplementation(async path => path === '/http-variable-probes' ? probe() : result())
  render(<ResourceForm environmentId={env.id} environment={env} saved={vi.fn()} cancel={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: '+ Переменная ресурса' }))
  fireEvent.change(screen.getByLabelText('Переменная ресурса: способ 1'), { target: { value: 'http' } })
  fireEvent.change(screen.getByLabelText('Источник: URL'), { target: { value: source.url } })
  fireEvent.change(screen.getByLabelText('Источник: поле JSON'), { target: { value: '/token' } })
  fireEvent.click(screen.getByRole('button', { name: 'Проверить получение' }))
  await waitFor(() => expect(request).toHaveBeenCalled())
  expect(request.mock.calls[0]).toEqual(['/http-variable-probes', 'POST', expect.objectContaining({ scope: 'resource', resourceId: undefined, resourceVariables: {}, variableName: '' }), expect.any(String)])
  expect(screen.getByLabelText('Имя ресурса')).toHaveValue('')
})

test('probe failure appears outside modal scrolling and preserves the edited source', async () => {
  vi.spyOn(api, 'request').mockImplementation(async path => path === '/http-variable-probes' ? probe() : { ...result(), valueAvailable: false, extractedValue: undefined, formattedValue: undefined, probe: { ...probe(), state: 'error', error: 'Поле JSON отсутствует' } })
  render(<VariablesForm initial={env} close={vi.fn()} saved={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: 'Проверить получение' }))
  const dialog = screen.getByRole('dialog', { name: 'Переменные окружения' })
  await waitFor(() => expect(dialog.querySelector('.form-error-banner')).toHaveTextContent('Поле JSON отсутствует'))
  expect(dialog.querySelector('.modal-form-scroll .form-error-banner')).toBeNull()
  expect(within(dialog).getByLabelText('Источник: URL')).toHaveValue(source.url)
})

test('late start response cannot restore a result after source editing', async () => {
  let finish!: (value: HTTPVariableProbe) => void
  const request = vi.spyOn(api, 'request').mockImplementation(async () => new Promise<HTTPVariableProbe>(resolve => { finish = resolve }))
  render(<VariablesForm initial={env} close={vi.fn()} saved={vi.fn()} />)
  fireEvent.click(screen.getByRole('button', { name: 'Проверить получение' }))
  await waitFor(() => expect(request).toHaveBeenCalledTimes(1))
  fireEvent.change(screen.getByLabelText('Источник: URL'), { target: { value: 'http://auth.test/new' } })
  await act(async () => finish(probe()))
  expect(request).toHaveBeenCalledTimes(1)
  expect(screen.queryByText('9007199254740993123.125')).toBeNull()
})
