import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import JiraExport from './JiraExport'
import { request, type TestCase } from './api'

vi.mock('./api', async () => ({ ...(await vi.importActual('./api')), request: vi.fn() }))
afterEach(() => { cleanup(); vi.clearAllMocks() })
it('экспортирует выбранную сохранённую версию и честно сообщает об отказе clipboard', async () => {
  const save = vi.fn()
  vi.mocked(request).mockResolvedValue({ text: 'h1. Кириллица\n{noformat}\n9007199254740993123.125\n{noformat}' })
  let finish!: (value: unknown) => void
  vi.mocked(request).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
  const clipboard = { writeText: vi.fn().mockRejectedValue(new Error('denied')) }
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: clipboard })
  render(<JiraExport saved={{ id: 'case', revision: 7 } as TestCase} dirty save={save} close={() => {}} />)
  expect(screen.getByText(/Есть несохранённые изменения/)).toBeInTheDocument()
  fireEvent.click(screen.getByText('Версия 7: предпросмотр'))
  expect(screen.getByLabelText('Включить ожидаемые результаты')).toBeDisabled()
  await act(async () => finish({ text: 'h1. Кириллица\n9007199254740993123.125' }))
  await screen.findByLabelText('Jira wiki markup')
  expect(save).not.toHaveBeenCalled()
  expect(request).toHaveBeenCalledWith('/test-cases/case/export?revision=7&includeExpected=true&format=jira-wiki')
  fireEvent.click(screen.getByText('Скопировать для Jira'))
  await screen.findByText(/Буфер обмена недоступен/)
  expect(screen.queryByText('Сценарий скопирован')).toBeNull()
  clipboard.writeText.mockResolvedValue(undefined)
  fireEvent.click(screen.getByText('Скопировать для Jira'))
  await screen.findByText('Сценарий скопирован')
  fireEvent.click(screen.getByLabelText('Включить ожидаемые результаты'))
  await waitFor(() => expect(screen.queryByLabelText('Jira wiki markup')).toBeNull())
  fireEvent.click(screen.getByText('Версия 7: предпросмотр'))
  await waitFor(() => expect(request).toHaveBeenLastCalledWith('/test-cases/case/export?revision=7&includeExpected=false&format=jira-wiki'))
})
