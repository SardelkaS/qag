import { useId } from 'react'
import type { HTTPHeaderSource, RequestHeader, ResourceConfig } from './api'
import { PemUpload } from './PemUpload'
import { Field, Pairs } from './ui'

type TLS = ResourceConfig['tls']
export function TLSFields({
  config,
  change,
  prefix = '',
  keyReference,
  keyChange,
  secrets = [],
}: {
  config: TLS
  change: (patch: Partial<TLS>) => void
  prefix?: string
  keyReference?: string
  keyChange?: (value: string) => void
  secrets?: string[]
}) {
  const keyList = useId()
  return (
    <section className="tls-settings">
      <Field label={`${prefix}Защита подключения`}>
        <select
          value={config.mode}
          onChange={(e) =>
            change({
              mode: e.target.value as TLS['mode'],
              ...(e.target.value === 'plaintext'
                ? { certificate: '', ca: '', serverName: '' }
                : e.target.value === 'tls'
                  ? { certificate: '' }
                  : {}),
            })
          }
        >
          <option value="tls">TLS — проверка сервера</option>
          <option value="mtls">mTLS — сервер и клиентский сертификат</option>
          <option value="plaintext">Без TLS — plaintext</option>
        </select>
      </Field>
      {config.mode !== 'plaintext' && (
        <>
          <Field label={`${prefix}Имя сервера TLS`}>
            <input
              className="mono"
              value={config.serverName ?? ''}
              placeholder="По умолчанию — имя хоста"
              onChange={(e) => change({ serverName: e.target.value })}
            />
          </Field>
          <Field
            label={`${prefix}CA-сертификат PEM`}
            hint="Необязательно, используются также системные CA"
          >
            <textarea
              className="mono"
              rows={3}
              value={config.ca ?? ''}
              onChange={(e) => change({ ca: e.target.value })}
            />
          </Field>
          <PemUpload label={`${prefix}Загрузить CA из файла`} kind="certificate" change={(ca) => change({ ca })} />
        </>
      )}
      {config.mode === 'mtls' && (
        <>
          <Field
            label={`${prefix}Клиентский сертификат PEM`}
            hint={
              keyChange
                ? 'Для ключа выберите имя сохранённой секретной переменной'
                : 'Приватный ключ задаётся в секретах PRIVATE_KEY'
            }
          >
            <textarea
              className="mono"
              rows={3}
              value={config.certificate ?? ''}
              onChange={(e) => change({ certificate: e.target.value })}
            />
          </Field>
          <PemUpload label={`${prefix}Загрузить клиентский сертификат из файла`} kind="certificate" change={(certificate) => change({ certificate })} />
          {keyChange && (
            <Field label={`${prefix}Секрет приватного ключа`}>
              <input
                list={keyList}
                value={keyReference ?? ''}
                placeholder="AUTH_PRIVATE_KEY"
                onChange={(e) => keyChange(e.target.value)}
              />
              <datalist id={keyList}>
                {secrets.map((v) => (
                  <option key={v}>{v}</option>
                ))}
              </datalist>
            </Field>
          )}
        </>
      )}
    </section>
  )
}

export const newHTTPSource = (): HTTPHeaderSource => ({
  method: 'GET',
  url: '',
  query: [],
  headers: [],
  bodyMode: 'none',
  body: '',
  tls: { mode: 'tls' },
  jsonPointer: '',
  prefix: '',
  suffix: '',
  fetchMode: 'per_attempt',
})
export function HeaderEditor({
  label,
  value,
  onChange,
  disabled = false,
  secrets = [],
}: {
  label: string
  value: RequestHeader[]
  onChange: (value: RequestHeader[]) => void
  disabled?: boolean
  secrets?: string[]
}) {
  const update = (i: number, patch: Partial<RequestHeader>) =>
    onChange(value.map((h, n) => (n === i ? { ...h, ...patch } : h)))
  const updateSource = (i: number, patch: Partial<HTTPHeaderSource>) =>
    update(i, { source: { ...value[i].source!, ...patch } })
  return (
    <fieldset className="pair-editor header-editor" disabled={disabled}>
      <legend>{label}</legend>
      {value.map((h, i) => (
        <article className="header-row" key={h.id ?? i}>
          <div className="header-line">
            <input
              aria-label={`${label}: имя ${i + 1}`}
              placeholder="Имя заголовка"
              value={h.name}
              onChange={(e) => update(i, { name: e.target.value })}
            />
            <select
              aria-label={`${label}: способ значения ${i + 1}`}
              value={h.source ? 'http' : 'literal'}
              onChange={(e) =>
                update(
                  i,
                  e.target.value === 'http'
                    ? {
                        id: h.id ?? crypto.randomUUID(),
                        value: undefined,
                        source: newHTTPSource(),
                      }
                    : { source: undefined, value: '' },
                )
              }
            >
              <option value="literal">Значение</option>
              <option value="http">Из HTTP-запроса</option>
            </select>
            <button
              type="button"
              className="icon-button"
              aria-label={`Удалить ${label}: ${i + 1}`}
              onClick={() => onChange(value.filter((_, n) => n !== i))}
            >
              ×
            </button>
          </div>
          {!h.source ? (
            <input
              aria-label={`${label}: значение ${i + 1}`}
              placeholder="Значение или ${TOKEN}"
              value={h.value ?? ''}
              onChange={(e) => update(i, { value: e.target.value })}
            />
          ) : (
            <HTTPSourceFields value={h.source} change={(patch) => updateSource(i, patch)} secrets={secrets} />
          )}
        </article>
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

export function HTTPSourceFields({ value, change, secrets = [] }: { value: HTTPHeaderSource; change: (patch: Partial<HTTPHeaderSource>) => void; secrets?: string[] }) {
  return (            <div className="header-source">
              <h5>HTTP-источник значения</h5>
              <div className="form-grid http-line">
                <Field label="Источник: метод">
                  <select
                    value={value.method}
                    onChange={(e) =>
                      change({ method: e.target.value })
                    }
                  >
                    {[
                      'GET',
                      'POST',
                      'PUT',
                      'PATCH',
                      'DELETE',
                      'HEAD',
                      'OPTIONS',
                    ].map((v) => (
                      <option key={v}>{v}</option>
                    ))}
                  </select>
                </Field>
                <Field
                  label="Источник: URL"
                  hint="Полный URL с параметрами; для http выберите plaintext"
                >
                  <input
                    className="mono"
                    value={value.url}
                    placeholder="https://auth.local/token"
                    onChange={(e) => change({ url: e.target.value })}
                  />
                </Field>
              </div>
              <Pairs
                label="Источник: query-параметры"
                value={value.query}
                onChange={(query) => change({ query })}
              />
              <Pairs
                label="Источник: заголовки"
                value={value.headers}
                onChange={(headers) => change({ headers })}
              />
              <Field label="Источник: формат тела">
                <select
                  value={value.bodyMode}
                  onChange={(e) =>
                    change({
                      bodyMode: e.target.value as HTTPHeaderSource['bodyMode'],
                      body: ['none', 'form'].includes(e.target.value) ? '' : value.body,
                      form: e.target.value === 'form' ? value.form ?? [] : undefined,
                    })
                  }
                >
                  <option value="none">Без тела</option>
                  <option value="json">JSON</option>
                  <option value="text">Текст</option>
                  <option value="form">Форма · application/x-www-form-urlencoded</option>
                </select>
              </Field>
              {value.bodyMode === 'form' && <>
                <p className="muted small">Введите исходные значения, как в curl --data-urlencode. Подстановки и секреты кодируются автоматически; повторные имена и пустые значения сохраняются.</p>
                <Pairs label="Источник: поля формы" value={value.form ?? []} onChange={form => change({ form })} />
              </>}
              {value.bodyMode !== 'none' && value.bodyMode !== 'form' && (
                <Field
                  label="Источник: тело запроса"
                  hint="Секреты указываются ссылками, например ${CLIENT_SECRET}"
                >
                  <textarea
                    className="mono"
                    rows={4}
                    value={value.body}
                    onChange={(e) => change({ body: e.target.value })}
                  />
                </Field>
              )}
              <TLSFields
                config={value.tls}
                change={(patch) =>
                  change({ tls: { ...value.tls, ...patch } })
                }
                prefix="Источник: "
                keyReference={value.privateKeySecret}
                keyChange={(privateKeySecret) =>
                  change({ privateKeySecret })
                }
                secrets={secrets}
              />
              <div className="form-grid">
                <Field
                  label="Источник: поле JSON"
                  hint="JSON Pointer: /access_token или /data/token"
                >
                  <input
                    className="mono"
                    value={value.jsonPointer}
                    placeholder="/access_token"
                    onChange={(e) =>
                      change({ jsonPointer: e.target.value })
                    }
                  />
                </Field>
                <Field
                  label="Источник: текст перед значением"
                  hint="Например Bearer с пробелом в конце"
                >
                  <input
                    value={value.prefix ?? ''}
                    onChange={(e) =>
                      change({ prefix: e.target.value })
                    }
                  />
                </Field>
                <Field label="Источник: текст после значения" hint="Необязательно. Пробелы сохраняются без изменений.">
                  <input value={value.suffix ?? ''} onChange={e => change({ suffix: e.target.value })} />
                </Field>
              </div>
              <Field
                label="Источник: когда получать"
                hint="Пробный вызов редактора всегда получает новое значение"
              >
                <select
                  value={value.fetchMode}
                  onChange={(e) =>
                    change({
                      fetchMode: e.target
                        .value as HTTPHeaderSource['fetchMode'],
                    })
                  }
                >
                  <option value="per_attempt">Перед каждым вызовом</option>
                  <option value="per_run">Один раз на прогон</option>
                </select>
              </Field>
            </div>
)
}
