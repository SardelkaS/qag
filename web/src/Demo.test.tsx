import { expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import CaseEditor from './CaseEditor'
import { newBlock, type TestCase, type Resource } from './api'

it('разделяет запрет редактирования и разрешение пробного вызова', () => {
  const block = newBlock('http')
  block.resource = 'DEMO_HTTP'
  const doc = {
    id: 'case',
    name: 'Пример',
    revision: 1,
    readOnly: true,
    steps: [{ id: 'step', description: 'Проверить', blocks: [block] }],
  } as TestCase
  const resource = { id: 'r', name: 'DEMO_HTTP', kind: 'http', readOnly: true } as Resource
  const launch = vi.fn()
  render(
    <CaseEditor
      document={doc}
      resources={[resource]}
      fields={{}}
      disabled
      canExecute
      busy={false}
      environmentId="env"
      attempts={{}}
      change={vi.fn()}
      launch={launch}
      cancel={vi.fn()}
    />,
  )
  expect(screen.getByLabelText('Название тест-кейса')).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Запустить вызов' })).toBeEnabled()
  expect(screen.getByRole('button', { name: '+ Добавить шаг' })).toBeDisabled()
})
