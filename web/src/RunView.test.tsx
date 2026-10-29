import { useState } from 'react'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import RunView from './RunView'
import * as api from './api'
import type { Run } from './api'

const initial = () => ({
  id: 'run', status: 'running', startedAt: '2026-10-28T12:00:00Z', environmentName: 'test', retentionReasons: [], resources: [], attempts: [], variants: [], batches: [],
  snapshot: { name: 'Manual case', revision: 1, steps: [1, 2].map(i => ({id: `step-${i}`, description: `Action ${i}`, blocks: []})) },
  stepStates: [1, 2].map(i => ({ stepId: `step-${i}`, status: i === 1 ? 'failed' : 'not_run', comment: '', revision: 1 })),
}) as unknown as Run

function Harness() {
  const [run, setRun] = useState(initial)
  return <RunView run={run} update={setRun} onRefresh={() => {}} />
}
afterEach(() => vi.restoreAllMocks())

describe('массовая ручная отметка', () => {
  it('сохраняет draft и позволяет записать его с revision собственной массовой команды', async () => {
    let run = initial()
    const request = vi.spyOn(api, 'request').mockImplementation(async (path, method, body) => {
      if (path.endsWith('/steps/pass-all')) run = { ...run, stepStates: run.stepStates.map(s => ({...s, status: 'passed', revision: 2})) }
      else if (path.endsWith('/steps/step-1') && method === 'PATCH') {
        expect(body).toMatchObject({revision: 2, status: 'passed', comment: 'Unsaved note'})
        run = {...run, stepStates: run.stepStates.map(s => s.stepId === 'step-1' ? {...s, comment: 'Unsaved note', revision: 3} : s)}
      } else throw new Error('Unexpected command')
      return run
    })
    render(<Harness />)
    fireEvent.change(screen.getByLabelText('Комментарий к шагу 1'), {target: {value: 'Unsaved note'}})
    fireEvent.click(screen.getByRole('button', {name: 'Пометить все пройденным'}))
    await waitFor(() => expect(screen.getByLabelText('Статус шага 1')).toHaveValue('passed'))
    expect(screen.getByLabelText('Комментарий к шагу 1')).toHaveValue('Unsaved note')
    expect(screen.getByRole('button', {name: 'Завершить прогон'})).toBeDisabled()
    fireEvent.click(screen.getByRole('button', {name: 'Сохранить комментарий'}))
    await waitFor(() => expect(screen.queryByRole('button', {name: 'Сохранить комментарий'})).toBeNull())
    expect(request).toHaveBeenCalledTimes(2)
    expect(request.mock.calls[0][2]).toEqual({steps: [{stepId:'step-1',revision:1},{stepId:'step-2',revision:1}]})
  })

  it('после конфликта сохраняет введённый текст и не подставляет чужую revision для комментария', async () => {
    const newer = initial()
    newer.stepStates[0] = {...newer.stepStates[0], comment: 'Other tab', revision: 2}
    const request = vi.spyOn(api, 'request').mockImplementation(async (path, method) => {
      if (method === 'GET' || !method) return newer
      if (path.endsWith('/steps/step-1')) throw new Error('Comment conflict')
      throw new Error('Step conflict')
    })
    render(<Harness />)
    fireEvent.change(screen.getByLabelText('Комментарий к шагу 1'), {target: {value: 'My draft'}})
    fireEvent.click(screen.getByRole('button', {name: 'Пометить все пройденным'}))
    await screen.findByText('Step conflict')
    expect(screen.getByLabelText('Комментарий к шагу 1')).toHaveValue('My draft')
    fireEvent.click(screen.getByRole('button', {name: 'Сохранить комментарий'}))
    await screen.findByText('Comment conflict')
    expect(request.mock.calls.at(-1)?.[2]).toMatchObject({revision: 1, comment:'My draft'})
    expect(screen.getByLabelText('Комментарий к шагу 1')).toHaveValue('My draft')
  })
})
