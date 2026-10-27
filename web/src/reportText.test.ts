import { describe, expect, it } from 'vitest'
import type { Report } from './api'
import { markdown, plainText, tableLines, richHTML } from './reportText'
import { wrapText } from './reportImages'

describe('public report renderers', () => {
  it('preserves precise SQL values and keeps SQL NULL, JSON null and empty text distinct', () => {
    const lines = tableLines({
      id: 'table',
      kind: 'table',
      text: '',
      columns: [
        { name: 'amount', type: 'numeric' },
        { name: 'empty', type: 'text' },
        { name: 'missing', type: 'text' },
        { name: 'json', type: 'jsonb' },
      ],
      rows: [
        [
          { type: 'decimal', value: '9007199254740993123.125' },
          { type: 'string', value: '' },
          { type: 'null' },
          { type: 'json', value: 'null' },
        ],
      ],
    })
    expect(lines[1]).toBe('9007199254740993123.125 | "" | NULL | null')
  })
  it('escapes markup and chooses a fence that user backticks cannot close', () => {
    const report = {
      document: [
        {
          id: 'title',
          kind: 'heading',
          level: 1,
          text: '<script>unsafe</script>',
        },
        {
          id: 'code',
          kind: 'code',
          text: 'Русский ``` literal\n9007199254740993123.125',
        },
      ],
      images: [],
      privateField: 'never export',
    } as unknown as Report
    const md = markdown(report),
      html = richHTML(report, [])
    expect(md).toContain(
      '````\nРусский ``` literal\n9007199254740993123.125\n````',
    )
    expect(html).toContain('&lt;script&gt;unsafe&lt;/script&gt;')
    expect(html).not.toContain('<script>')
    expect(plainText(report)).not.toContain('never export')
  })
  it('wraps long Unicode lines without losing characters or empty paragraphs', () => {
    const text = 'Длинная строка 🧪 9007199254740993123.125 '.repeat(200)
    const lines = wrapText(text, 30, (s) => [...s].length)
    expect(lines.length).toBeGreaterThan(100)
    expect(lines.join('')).toBe(text)
    expect(lines.every((s) => [...s].length <= 30)).toBe(true)
    expect(wrapText('one\n\ntwo', 10, (s) => s.length)).toEqual([
      'one',
      '',
      'two',
    ])
  })
})
