import { expect, it } from 'vitest'
import { variableMap, type VariableRow } from './ResourceVariables'

it('пропускает несколько пустых строк до проверки HTTP-формы, но сохраняет ошибку непустого дубликата', () => {
  const rows: VariableRow[] = [
    { id:'a', name:' ', mode:'http' }, { id:'b', name:'', mode:'http' },
    { id:'c', name:'VALUE', mode:'literal', value:'' },
  ]
  expect(variableMap(rows)).toEqual({ VALUE:{ mode:'literal', value:'' } })
  expect(() => variableMap([...rows, { id:'d', name:'VALUE', mode:'http' }])).toThrow('уникальными')
})
