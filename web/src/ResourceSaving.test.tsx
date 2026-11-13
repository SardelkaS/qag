import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import * as api from './api'
import type { Environment, Resource } from './api'
import { newHTTPSource } from './HeaderEditor'
import { ResourceForm, VariablesForm } from './ResourcesPanel'
import { PemUpload } from './PemUpload'

afterEach(() => vi.restoreAllMocks())

const environment: Environment = { id: 'env', name: 'Auth', description: '', revision: 1, readOnly: false, variables: { STATIC: 'one' }, secretNames: ['AUTH_SECRET'], httpVariables: { AUTH: { ...newHTTPSource(), url: 'https://auth.example.test/token', jsonPointer: '/token', prefix: 'Bearer ', suffix: '!' } } }
const resource = (): Resource => ({ id: 'resource', environmentId: 'env', name: 'TARGET', kind: 'grpc', revision: 1, versionId: 'version', updatedAt: '' , readOnly: false, secretNames: ['PRIVATE_KEY'], config: { address: 'localhost:50051', tls: { mode: 'plaintext' }, headers: [{ id: 'header', name: 'Authorization', source: { ...newHTTPSource(), url: 'https://auth.example.test/token', jsonPointer: '/token', prefix: 'Bearer ' } }], variables: { LOCAL: { mode: 'literal', value: 'kept' } }, protoFiles: { 'old.proto': 'syntax="proto3";' } } })

test('environment editor restores HTTP definitions and submits a separate typed set', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue(environment)
  const saved = vi.fn(async () => {})
  render(<VariablesForm initial={environment} close={vi.fn()} saved={saved} />)
  expect(screen.getByLabelText('Переменная окружения: способ 2')).toHaveValue('http')
  expect(screen.getByLabelText('Источник: текст перед значением')).toHaveValue('Bearer ')
  fireEvent.change(screen.getByLabelText('Источник: формат тела'), { target: { value: 'form' } })
  const form = within(screen.getByRole('group', { name: 'Источник: поля формы' }))
  fireEvent.click(form.getByRole('button', { name: '+ Добавить' }))
  fireEvent.change(form.getByLabelText('Источник: поля формы: имя 1'), { target: { value: 'client_secret' } })
  fireEvent.change(form.getByLabelText('Источник: поля формы: значение 1'), { target: { value: '${AUTH_SECRET}' } })
  fireEvent.change(screen.getByLabelText('Источник: текст после значения'), { target: { value: '] ' } })
  fireEvent.click(screen.getByRole('button', { name: '+ Переменная окружения' }))
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить переменные' }))
  await waitFor(() => expect(saved).toHaveBeenCalled())
  expect(request).toHaveBeenCalledWith('/environments/env/variables', 'PUT', expect.objectContaining({ revision: 1, variables: { STATIC: 'one' }, httpVariables: { AUTH: expect.objectContaining({ bodyMode: 'form', body: '', form: [{ name: 'client_secret', value: '${AUTH_SECRET}' }], prefix: 'Bearer ', suffix: '] ' }) } }))
})

test('one resource save includes headers, locals, TLS, proto and secrets; failure preserves the draft', async () => {
  const initial = resource()
  const request = vi.spyOn(api, 'request').mockRejectedValueOnce(new Error('Ошибка записи')).mockResolvedValueOnce({ ...initial, revision: 2 })
  const saved = vi.fn(async () => {})
  render(<ResourceForm initial={initial} environmentId="env" saved={saved} cancel={vi.fn()} />)
  fireEvent.change(screen.getByLabelText('Адрес сервера'), { target: { value: 'localhost:50052' } })
  const headers = within(screen.getByRole('group', { name: 'Общие метаданные' }))
  fireEvent.change(headers.getByLabelText('Источник: текст после значения'), { target: { value: ' end' } })
  fireEvent.change(screen.getByLabelText('Переменная ресурса: значение 1'), { target: { value: 'changed' } })
  fireEvent.change(screen.getByLabelText('Действие: PRIVATE_KEY'), { target: { value: 'replace' } })
  fireEvent.change(screen.getByLabelText('Новое значение: PRIVATE_KEY'), { target: { value: 'new-key' } })
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ресурс' }))
  await screen.findByText('Ошибка записи')
  expect(screen.getByLabelText('Адрес сервера')).toHaveValue('localhost:50052')
  expect(headers.getByLabelText('Источник: текст после значения')).toHaveValue(' end')
  expect(saved).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ресурс' }))
  await waitFor(() => expect(saved).toHaveBeenCalledWith(expect.objectContaining({ revision: 2 })))
  const payload = request.mock.calls[1][2] as { config: Resource['config']; secrets: unknown }
  expect(payload).toMatchObject({ config: { address: 'localhost:50052', tls: { mode: 'plaintext' }, headers: [{ source: { prefix: 'Bearer ', suffix: ' end' } }], variables: { LOCAL: { mode: 'literal', value: 'changed' } }, protoFiles: initial.config.protoFiles }, secrets: { replace: { PRIVATE_KEY: 'new-key' }, clear: [] } })
})

test('pending proto reading blocks save and retains concurrent edits', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue({ ...resource(), revision: 2 })
  let finish!: (text: string) => void
  const file = new File([''], 'new.proto')
  Object.defineProperty(file, 'text', { value: () => new Promise<string>(resolve => { finish = resolve }) })
  render(<ResourceForm initial={resource()} environmentId="env" saved={async () => {}} cancel={vi.fn()} />)
  fireEvent.change(screen.getByLabelText('Добавить .proto'), { target: { files: [file] } })
  expect(screen.getByRole('button', { name: 'Чтение файлов…' })).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Адрес сервера'), { target: { value: 'localhost:50053' } })
  expect(request).not.toHaveBeenCalled()
  await act(async () => { finish('syntax="proto3"; message New {}') })
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ресурс' }))
  await waitFor(() => expect(request).toHaveBeenCalled())
  expect(request.mock.calls[0][2]).toMatchObject({ config: { address: 'localhost:50053', protoFiles: { 'old.proto': 'syntax="proto3";', 'new.proto': 'syntax="proto3"; message New {}' } } })
})

function deferredPEM() {
  let finish!: (bytes: ArrayBuffer) => void
  const file = new File(['placeholder'], 'ca.pem')
  Object.defineProperty(file, 'arrayBuffer', { value: () => new Promise<ArrayBuffer>(resolve => { finish = resolve }) })
  const pem = '-----BEGIN CERTIFICATE-----\n' + btoa(String.fromCharCode(0x30, 16, 0x04, 14, ...Array(14).fill(1))) + '\n-----END CERTIFICATE-----'
  return { file, pem, finish: () => finish(new TextEncoder().encode(pem).buffer) }
}

test('a PEM read inside a header source keeps newer source fields', async () => {
  const request = vi.spyOn(api, 'request').mockResolvedValue({ ...resource(), revision: 2 })
  const input = deferredPEM()
  render(<ResourceForm initial={resource()} environmentId="env" saved={async () => {}} cancel={vi.fn()} />)
  fireEvent.change(screen.getByLabelText('Источник: Загрузить CA из файла'), { target: { files: [input.file] } })
  expect(screen.getByRole('button', { name: 'Чтение файлов…' })).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Источник: текст после значения'), { target: { value: ' latest' } })
  await act(async () => input.finish())
  fireEvent.click(screen.getByRole('button', { name: 'Сохранить ресурс' }))
  await waitFor(() => expect(request).toHaveBeenCalled())
  expect(request.mock.calls[0][2]).toMatchObject({ config: { headers: [{ source: { suffix: ' latest', tls: { ca: input.pem } } }] } })
})

test('a removed file input cannot apply its late result', async () => {
  const change = vi.fn()
  const input = deferredPEM()
  const view = render(<PemUpload label="CA" kind="certificate" change={change} />)
  fireEvent.change(screen.getByLabelText('CA'), { target: { files: [input.file] } })
  view.unmount()
  await act(async () => input.finish())
  expect(change).not.toHaveBeenCalled()
})
