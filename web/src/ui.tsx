import {
  Children,
  cloneElement,
  isValidElement,
  useId,
  type ReactNode,
  type ReactElement,
} from 'react'
import type { KeyValue, TypedValue } from './api'

export function Field({
  label,
  error,
  children,
  hint,
}: {
  label: string
  error?: string
  hint?: string
  children: ReactNode
}) {
  const id = useId()
  const control = Children.map(children, (child) =>
    isValidElement(child) &&
    typeof child.type === 'string' &&
    ['input', 'textarea', 'select'].includes(child.type)
      ? cloneElement(child as ReactElement<Record<string, unknown>>, {
          'aria-label': label,
          'aria-invalid': !!error,
          'aria-describedby': error || hint ? id : undefined,
        })
      : child,
  )
  return (
    <label className={`field ${error ? 'invalid' : ''}`}>
      <span>{label}</span>
      {control}
      {hint && <small id={error ? undefined : id}>{hint}</small>}
      {error && (
        <small id={id} className="field-error" role="alert">
          {error}
        </small>
      )}
    </label>
  )
}
export function Notice({ children, onClose }: { children: ReactNode; onClose?: () => void }) {
  return (
    <div className="notice" role="alert">
      <div>{children}</div>
      {onClose && (
        <button className="icon-button" aria-label="Закрыть сообщение" onClick={onClose}>
          ×
        </button>
      )}
    </div>
  )
}
export function Modal({
  title,
  children,
  onClose,
}: {
  title: string
  children: ReactNode
  onClose: () => void
}) {
  return (
    <div
      className="modal-backdrop"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <section className="modal" role="dialog" aria-modal="true" aria-label={title}>
        <header>
          <h2>{title}</h2>
          <button className="icon-button" onClick={onClose} aria-label="Закрыть">
            ×
          </button>
        </header>
        {children}
      </section>
    </div>
  )
}
export function Pairs({
  label,
  value,
  onChange,
  disabled = false,
}: {
  label: string
  value: KeyValue[]
  onChange: (v: KeyValue[]) => void
  disabled?: boolean
}) {
  return (
    <fieldset className="pair-editor" disabled={disabled}>
      <legend>{label}</legend>
      {value.map((item, i) => (
        <div className="pair" key={i}>
          <input
            aria-label={`${label}: имя ${i + 1}`}
            placeholder="Имя"
            value={item.name}
            onChange={(e) =>
              onChange(value.map((v, n) => (n === i ? { ...v, name: e.target.value } : v)))
            }
          />
          <input
            aria-label={`${label}: значение ${i + 1}`}
            placeholder="Значение или ${NAME}"
            value={item.value}
            onChange={(e) =>
              onChange(value.map((v, n) => (n === i ? { ...v, value: e.target.value } : v)))
            }
          />
          <button
            type="button"
            className="icon-button"
            aria-label={`Удалить ${label.toLowerCase()} ${i + 1}`}
            onClick={() => onChange(value.filter((_, n) => n !== i))}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        onClick={() => onChange([...value, { name: '', value: '' }])}
      >
        + Добавить
      </button>
    </fieldset>
  )
}
const valueTypes: Array<[TypedValue['type'], string]> = [
  ['string', 'Текст'],
  ['integer', 'Целое'],
  ['decimal', 'Точное число'],
  ['boolean', 'Логическое'],
  ['timestamp', 'Дата / время'],
  ['bytes', 'Base64'],
  ['json', 'JSON'],
  ['null', 'NULL'],
]
export function ValueInput({
  value,
  onChange,
  label,
  disabled,
}: {
  value: TypedValue
  onChange: (v: TypedValue) => void
  label: string
  disabled?: boolean
}) {
  return (
    <div className="typed-value">
      <select
        aria-label={`${label}: тип`}
        disabled={disabled}
        value={value.type}
        onChange={(e) =>
          onChange({
            type: e.target.value as TypedValue['type'],
            value: e.target.value === 'boolean' ? 'false' : '',
          })
        }
      >
        {valueTypes.map(([v, text]) => (
          <option key={v} value={v}>
            {text}
          </option>
        ))}
      </select>
      {value.type === 'null' ? (
        <span className="null-value">NULL</span>
      ) : value.type === 'boolean' ? (
        <select
          aria-label={label}
          value={value.value ?? 'false'}
          disabled={disabled}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
        >
          <option>false</option>
          <option>true</option>
        </select>
      ) : (
        <input
          aria-label={label}
          className="mono"
          disabled={disabled}
          value={value.value ?? ''}
          placeholder={value.type === 'timestamp' ? '2026-10-25T12:00:00Z' : 'Значение'}
          onChange={(e) => onChange({ ...value, value: e.target.value })}
        />
      )}
    </div>
  )
}
export function prettyJSON(text: string): string {
  const expression =
    /"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|true|false|null|[{}\[\],:]/g
  const tokens: string[] = []
  let end = 0
  for (const match of text.matchAll(expression)) {
    if (text.slice(end, match.index).trim()) return text
    tokens.push(match[0])
    end = match.index + match[0].length
  }
  if (!tokens.length || text.slice(end).trim()) return text
  let result = ''
  let level = 0
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i]
    if (token === '{' || token === '[') {
      result += token
      if (tokens[i + 1] === (token === '{' ? '}' : ']')) {
        result += tokens[++i]
      } else {
        level++
        result += '\n' + '  '.repeat(level)
      }
    } else if (token === '}' || token === ']') {
      level = Math.max(0, level - 1)
      result += '\n' + '  '.repeat(level) + token
    } else if (token === ',') result += ',\n' + '  '.repeat(level)
    else if (token === ':') result += ': '
    else result += token
  }
  return result
}
export function Code({ text, json = false }: { text: string; json?: boolean }) {
  return <pre className="code">{json ? prettyJSON(text) : text || '—'}</pre>
}
