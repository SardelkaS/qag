import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Code, prettyJSON } from './ui'
import { Results, SQLTable } from './Results'
import { newBlock, type Attempt, type Run } from './api'
import { mergeRun } from './RunView'

describe('точное безопасное представление', () => {
  it('сохраняет большие JSON-числа и выводит HTML как текст', () => {
    const value = '{"n":9007199254740993123,"html":"<img src=x onerror=alert(1)>"}'
    expect(prettyJSON(value)).toContain('9007199254740993123')
    const { container } = render(<Code text={value} json />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('<img src=x')
  })
  it('различает NULL, пустую строку и точный numeric', () => {
    render(
      <SQLTable
        columns={[{ name: 'value', type: 'numeric' }]}
        rows={[
          [{ type: 'null' }],
          [{ type: 'string', value: '' }],
          [{ type: 'decimal', value: '9007199254740993.001' }],
        ]}
      />,
    )
    expect(screen.getByText('NULL')).toBeInTheDocument()
    expect(screen.getByText('""')).toBeInTheDocument()
    expect(screen.getByText('9007199254740993.001')).toBeInTheDocument()
  })
  it('показывает ожидание выполненной версии, HTTP-статус и путь diff', () => {
    const block = newBlock('http')
    block.expected.bodyMode = 'json'
    block.expected.body = '{"n":9007199254740993123}'
    const attempt = {
      state: 'completed',
      request: block,
      revision: 2,
      durationMs: 15,
      environmentName: 'dev',
      result: {
        protocol: 'http',
        statusCode: 404,
        headers: [],
        body: '{"n":9007199254740993124}',
        comparison: {
          status: 'mismatch',
          differences: [
            { path: '$.n', expected: '9007199254740993123', actual: '9007199254740993124' },
          ],
        },
      },
    } as unknown as Attempt
    render(
      <Results
        block={{ ...block, expected: { bodyMode: 'text', body: 'новое ожидание' } }}
        attempt={attempt}
      />,
    )
    expect(screen.getByText('HTTP 404')).toBeInTheDocument()
    expect(screen.getByText('$.n')).toBeInTheDocument()
    expect(screen.queryByText('новое ожидание')).toBeNull()
    expect(screen.getByText(/версия сценария 2/)).toBeInTheDocument()
  })
  it('не теряет новую попытку при позднем polling-ответе', () => {
    const old = {
      id: 'run',
      status: 'running',
      stepStates: [],
      attempts: [
        { id: 'a', state: 'completed', finishedAt: 'now' },
        { id: 'b', state: 'queued' },
      ],
    } as unknown as Run
    const next = { ...old, attempts: [{ id: 'a', state: 'running' }] } as Run
    const merged = mergeRun(old, next)
    expect(merged.attempts).toHaveLength(2)
    expect(merged.attempts[0].state).toBe('completed')
  })
})
