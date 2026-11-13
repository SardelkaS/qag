import { useState } from 'react'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import type { RequestHeader, Resource, TestCase } from './api'
import { newBlock } from './api'
import { BlockEditor } from './BlockEditor'
import CaseEditor from './CaseEditor'
import { HeaderEditor } from './HeaderEditor'
import { ThemeSwitch } from './ThemeSwitch'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  localStorage.removeItem('qag.theme')
})

test('resource selector keeps a missing resource and filters compatible options', () => {
  const b = { ...newBlock('http'), resource: 'MISSING' }
  render(
    <BlockEditor
      caseId="case"
      block={b}
      resources={
        [
          { id: 'one', name: 'GW', kind: 'http' },
          { id: 'two', name: 'SQL', kind: 'postgresql' },
        ] as Resource[]
      }
      onChange={vi.fn()}
      disabled={false}
      fields={{}}
      prefix="steps.0.blocks.0"
    />,
  )
  const select = screen.getByLabelText('Ресурс') as HTMLSelectElement
  expect(select.tagName).toBe('SELECT')
  expect(select.value).toBe('MISSING')
  expect(within(select).getByText('MISSING — недоступен')).toBeDisabled()
  expect(within(select).getByText('GW')).toBeInTheDocument()
  expect(within(select).queryByText('SQL')).not.toBeInTheDocument()
})

test('header source form chooses per-header mode and keeps its stable ID', () => {
  const fetch = vi.spyOn(globalThis, 'fetch')
  function Harness() {
    const [value, setValue] = useState<RequestHeader[]>([
      { name: 'Authorization', value: 'static' },
    ])
    return (
      <>
        <HeaderEditor label="Заголовки" value={value} onChange={setValue} />
        <output data-testid="header">{JSON.stringify(value)}</output>
      </>
    )
  }
  render(<Harness />)
  fireEvent.change(screen.getByLabelText('Заголовки: способ значения 1'), {
    target: { value: 'http' },
  })
  const id = JSON.parse(screen.getByTestId('header').textContent!)[0].id
  fireEvent.change(screen.getByLabelText('Источник: метод'), {
    target: { value: 'POST' },
  })
  fireEvent.change(screen.getByLabelText('Источник: когда получать'), {
    target: { value: 'per_run' },
  })
  fireEvent.change(screen.getByLabelText('Источник: поле JSON'), {
    target: { value: '/data/access_token' },
  })
  fireEvent.change(screen.getByLabelText('Источник: текст перед значением'), {
    target: { value: 'Bearer ' },
  })
  const header = JSON.parse(screen.getByTestId('header').textContent!)[0]
  expect(header.id).toBe(id)
  expect(header.source).toMatchObject({
    method: 'POST',
    fetchMode: 'per_run',
    jsonPointer: '/data/access_token',
    prefix: 'Bearer ',
  })
  expect(fetch).not.toHaveBeenCalled()
})

test('read-only case images can be enlarged and closed with Escape', () => {
  const block = newBlock('http')
  block.expected.images = [{ imageId: 'image-id', caption: 'Эталон' }]
  const c: TestCase = {
    id: 'case',
    name: 'Read only',
    folderId: 'folder',
    revision: 1,
    archived: false,
    parentArchived: false,
    createdAt: '2026-10-26T00:00:00Z',
    updatedAt: '2026-10-26T00:00:00Z',
    steps: [{ id: 'step', description: 'Check', blocks: [block] }],
    readOnly: true,
    validationIssues: [],
  }
  render(
    <CaseEditor
      document={c}
      resources={[]}
      fields={{}}
      disabled
      canExecute
      busy={false}
      environmentId="env"
      attempts={{}}
      change={vi.fn()}
      launch={vi.fn()}
      cancel={vi.fn()}
    />,
  )
  fireEvent.click(screen.getByRole('button', { name: 'Открыть изображение 1' }))
  expect(
    screen.getByRole('dialog', { name: 'Изображение ожидаемого результата' }),
  ).toBeVisible()
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(
    screen.queryByLabelText('Добавить изображения'),
  ).not.toBeInTheDocument()
})

test('theme follows system and preserves an explicit preference', () => {
  let listener = () => {}
  const media = {
    matches: false,
    addEventListener: (_name: string, fn: () => void) => {
      listener = fn
    },
    removeEventListener: vi.fn(),
  }
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => media as unknown as MediaQueryList),
  )
  document.documentElement.dataset.themePreference = 'system'
  render(<ThemeSwitch />)
  fireEvent.change(screen.getByLabelText('Цветовая тема'), {
    target: { value: 'dark' },
  })
  expect(document.documentElement.dataset.theme).toBe('dark')
  expect(localStorage.getItem('qag.theme')).toBe('dark')
  fireEvent.change(screen.getByLabelText('Цветовая тема'), {
    target: { value: 'system' },
  })
  act(() => {
    media.matches = true
    listener()
  })
  expect(document.documentElement.dataset.theme).toBe('dark')
})
