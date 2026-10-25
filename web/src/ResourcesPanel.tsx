import { useState } from 'react'
import type { Environment, KeyValue, Resource, ResourceConfig, SecretChanges } from './api'
import { fieldErrors, message, protocolLabel, request } from './api'
import { Field, Modal, Notice, Pairs } from './ui'

type SecretRow = {
  name: string
  mode: 'keep' | 'replace' | 'clear'
  value: string
  exists: boolean
}
const labels: Record<string, string> = {
  TOKEN: 'Токен',
  PASSWORD: 'Пароль PostgreSQL',
  PRIVATE_KEY: 'Приватный ключ клиента',
}
function initialSecrets(names: string[], presets: string[] = []): SecretRow[] {
  return [...new Set([...presets, ...names])].map((name) => ({
    name,
    mode: 'keep',
    value: '',
    exists: names.includes(name),
  }))
}
function secretChanges(rows: SecretRow[]): SecretChanges {
  const replace: Record<string, string> = {},
    clear: string[] = []
  const names = new Set<string>()
  for (const row of rows) {
    if (!row.name || names.has(row.name))
      throw new Error('Имена секретов должны быть непустыми и уникальными')
    names.add(row.name)
    if (row.mode === 'replace') replace[row.name] = row.value
    if (row.mode === 'clear') clear.push(row.name)
  }
  return { replace, clear }
}
function Secrets({
  rows,
  change,
  presets = [],
}: {
  rows: SecretRow[]
  change: (v: SecretRow[]) => void
  presets?: string[]
}) {
  const update = (i: number, patch: Partial<SecretRow>) =>
    change(rows.map((r, n) => (n === i ? { ...r, ...patch } : r)))
  return (
    <fieldset className="secret-editor">
      <legend>Секреты</legend>
      <p className="muted small">
        Сохранённые значения не показываются. Для заголовков и метаданных используйте ${'{TOKEN}'}.
      </p>
      {rows.map((row, i) => (
        <div className="secret-row" key={i}>
          <div className="form-grid">
            <Field
              label={labels[row.name] || 'Имя секрета'}
              hint={row.exists ? 'Секрет задан' : 'Не задан'}
            >
              <input
                value={row.name}
                readOnly={presets.includes(row.name)}
                onChange={(e) => update(i, { name: e.target.value.toUpperCase() })}
                placeholder="TOKEN_NAME"
              />
            </Field>
            <Field label={`Действие: ${row.name}`}>
              <select
                value={row.mode}
                onChange={(e) =>
                  update(i, { mode: e.target.value as SecretRow['mode'], value: '' })
                }
              >
                <option value="keep">{row.exists ? 'Оставить сохранённое' : 'Не задавать'}</option>
                <option value="replace">Задать новое значение</option>
                <option value="clear">Удалить значение</option>
              </select>
            </Field>
          </div>
          {row.mode === 'replace' && (
            <Field
              label={`Новое значение: ${row.name}`}
              hint="Пустое значение тоже можно сохранить явно."
            >
              {row.name === 'PRIVATE_KEY' ? (
                <textarea
                  className="mono"
                  rows={4}
                  autoComplete="off"
                  value={row.value}
                  onChange={(e) => update(i, { value: e.target.value })}
                />
              ) : (
                <input
                  type="password"
                  autoComplete="new-password"
                  value={row.value}
                  onChange={(e) => update(i, { value: e.target.value })}
                />
              )}
            </Field>
          )}
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        onClick={() => change([...rows, { name: '', value: '', mode: 'replace', exists: false }])}
      >
        + Секретная переменная
      </button>
    </fieldset>
  )
}

export default function ResourcesPanel({
  environments,
  environmentId,
  resources,
  reload,
  selectEnvironment,
}: {
  environments: Environment[]
  environmentId: string
  resources: Resource[]
  reload: () => Promise<void>
  selectEnvironment: (id: string) => void
}) {
  const [envDialog, setEnvDialog] = useState<'new' | Environment | null>(null)
  const [editing, setEditing] = useState<Resource | 'new' | null>(null)
  const environment = environments.find((e) => e.id === environmentId)
  return (
    <section className="resources-page">
      <div className="page-heading">
        <div>
          <p className="section-label">ПОДКЛЮЧЕНИЯ И ПЕРЕМЕННЫЕ</p>
          <h1>Окружения и ресурсы</h1>
          <p className="muted">
            Адреса и сертификаты задаются здесь, сценарии используют имена ресурсов.
          </p>
        </div>
        <button className="button secondary" onClick={() => setEnvDialog('new')}>
          + Окружение
        </button>
      </div>
      {!environment ? (
        <div className="empty-card">
          <h2>Создайте первое окружение</h2>
          <p className="muted">
            Например, dev или stage. Затем добавьте HTTP, gRPC или PostgreSQL-ресурс.
          </p>
          <button className="button" onClick={() => setEnvDialog('new')}>
            Создать окружение
          </button>
        </div>
      ) : (
        <>
          <div className="environment-banner">
            <div>
              <strong>{environment.name}</strong>
              <span className="muted small">
                {Object.keys(environment.variables).length} открытых переменных ·{' '}
                {environment.secretNames.length} секретных
              </span>
            </div>
            <button className="button secondary" onClick={() => setEnvDialog(environment)}>
              Настроить окружение
            </button>
          </div>
          <div className="resources-grid">
            <aside className="resource-list">
              <div className="row-heading">
                <h3>Ресурсы</h3>
                <button className="text-button" onClick={() => setEditing('new')}>
                  + Добавить
                </button>
              </div>
              {resources.length === 0 && <p className="muted small">Ресурсов пока нет.</p>}
              {resources.map((r) => (
                <button
                  key={r.id}
                  className={`resource-item ${editing !== 'new' && editing?.id === r.id ? 'selected' : ''}`}
                  onClick={() => setEditing(r)}
                >
                  <span className={`protocol ${r.kind}`}>{protocolLabel[r.kind]}</span>
                  <strong>{r.name}</strong>
                  <small>{r.config.baseUrl || r.config.address}</small>
                </button>
              ))}
            </aside>
            <div>
              {editing ? (
                <ResourceForm
                  key={editing === 'new' ? 'new' : `${editing.id}:${editing.revision}`}
                  initial={editing === 'new' ? undefined : editing}
                  environmentId={environmentId}
                  saved={async (r) => {
                    setEditing(r)
                    await reload()
                  }}
                  cancel={() => setEditing(null)}
                />
              ) : (
                <div className="empty-card compact">
                  <h3>Выберите ресурс</h3>
                  <p className="muted">
                    Именованный ресурс объединяет адрес, параметры подключения и сертификаты.
                  </p>
                  <button className="button" onClick={() => setEditing('new')}>
                    Добавить ресурс
                  </button>
                </div>
              )}
            </div>
          </div>
        </>
      )}
      {envDialog && (
        <EnvironmentForm
          initial={envDialog === 'new' ? undefined : envDialog}
          close={() => setEnvDialog(null)}
          saved={async (env) => {
            await reload()
            selectEnvironment(env.id)
            setEditing(null)
            setEnvDialog(null)
          }}
        />
      )}
    </section>
  )
}

function EnvironmentForm({
  initial,
  close,
  saved,
}: {
  initial?: Environment
  close: () => void
  saved: (env: Environment) => Promise<void>
}) {
  const [name, setName] = useState(initial?.name ?? ''),
    [variables, setVariables] = useState<KeyValue[]>(
      Object.entries(initial?.variables ?? {}).map(([name, value]) => ({ name, value })),
    )
  const [secrets, setSecrets] = useState(initialSecrets(initial?.secretNames ?? [])),
    [error, setError] = useState(''),
    [fields, setFields] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false)
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    setFields({})
    try {
      if (new Set(variables.map((v) => v.name)).size !== variables.length)
        throw new Error('Имена переменных должны быть уникальными')
      const env = await request<Environment>(
        initial ? `/environments/${initial.id}` : '/environments',
        initial ? 'PUT' : 'POST',
        {
          name,
          revision: initial?.revision ?? 0,
          variables: Object.fromEntries(variables.map((v) => [v.name, v.value])),
          secrets: secretChanges(secrets),
        },
      )
      await saved(env)
    } catch (e) {
      setError(message(e))
      setFields(fieldErrors(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal title={initial ? 'Настройки окружения' : 'Новое окружение'} onClose={close}>
      <form onSubmit={(e) => void submit(e)}>
        <fieldset disabled={busy} className="plain-fieldset">
          {error && <Notice>{error}</Notice>}
          <Field label="Имя окружения" error={fields.name}>
            <input
              autoFocus
              required
              maxLength={200}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="dev"
            />
          </Field>
          <Pairs label="Открытые переменные" value={variables} onChange={setVariables} />
          {fields.variables && <p className="field-error">{fields.variables}</p>}
          <Secrets rows={secrets} change={setSecrets} />
          <div className="modal-actions">
            <button type="button" className="button secondary" onClick={close}>
              Отмена
            </button>
            <button className="button" type="submit">
              {busy ? 'Сохранение…' : 'Сохранить окружение'}
            </button>
          </div>
        </fieldset>
      </form>
    </Modal>
  )
}

function ResourceForm({
  initial,
  environmentId,
  saved,
  cancel,
}: {
  initial?: Resource
  environmentId: string
  saved: (r: Resource) => Promise<void>
  cancel: () => void
}) {
  const [name, setName] = useState(initial?.name ?? ''),
    [kind, setKind] = useState<Resource['kind']>(initial?.kind ?? 'http')
  const [config, setConfig] = useState<ResourceConfig>(
    initial?.config ?? {
      baseUrl: '',
      address: '',
      database: '',
      username: '',
      headers: [],
      tls: { mode: 'tls' },
      protoFiles: {},
    },
  )
  const presets = kind === 'postgresql' ? ['PASSWORD', 'PRIVATE_KEY'] : ['TOKEN', 'PRIVATE_KEY']
  const [secrets, setSecrets] = useState(initialSecrets(initial?.secretNames ?? [], presets)),
    [error, setError] = useState(''),
    [fields, setFields] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false)
  const change = (patch: Partial<ResourceConfig>) => setConfig((old) => ({ ...old, ...patch }))
  const tls = (patch: Partial<ResourceConfig['tls']>) =>
    change({ tls: { ...config.tls, ...patch } })
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    setFields({})
    try {
      const result = await request<Resource>(
        initial ? `/resources/${initial.id}` : `/environments/${environmentId}/resources`,
        initial ? 'PUT' : 'POST',
        { name, kind, revision: initial?.revision ?? 0, config, secrets: secretChanges(secrets) },
      )
      await saved(result)
    } catch (e) {
      setError(message(e))
      setFields(fieldErrors(e))
    } finally {
      setBusy(false)
    }
  }
  const upload = async (list: FileList | null) => {
    if (!list) return
    try {
      const files = Array.from(list)
      if (files.reduce((n, f) => n + f.size, 0) > 5 * 1024 * 1024 || files.length > 100)
        throw new Error('Не больше 100 файлов и 5 MiB')
      const values = await Promise.all(
        files.map(
          async (file) =>
            [
              file.webkitRelativePath
                ? file.webkitRelativePath.split('/').slice(1).join('/')
                : file.name,
              await file.text(),
            ] as const,
        ),
      )
      change({ protoFiles: { ...config.protoFiles, ...Object.fromEntries(values) } })
    } catch (e) {
      setError(message(e))
    }
  }
  return (
    <form className="resource-form" onSubmit={(e) => void submit(e)}>
      <fieldset disabled={busy} className="plain-fieldset">
        <div className="row-heading">
          <h2>{initial ? initial.name : 'Новый ресурс'}</h2>
          <button type="button" className="text-button" onClick={cancel}>
            Закрыть
          </button>
        </div>
        {error && <Notice>{error}</Notice>}
        <div className="form-grid">
          <Field label="Имя ресурса" error={fields.name} hint="Имя для ссылок в запросах.">
            <input
              required
              value={name}
              maxLength={200}
              placeholder="AI_GW_SERVISE"
              onChange={(e) => setName(e.target.value.toUpperCase())}
            />
          </Field>
          <Field label="Тип ресурса" error={fields.kind}>
            <select
              value={kind}
              disabled={!!initial}
              onChange={(e) => {
                const next = e.target.value as Resource['kind']
                setKind(next)
                setSecrets(
                  initialSecrets(
                    [],
                    next === 'postgresql' ? ['PASSWORD', 'PRIVATE_KEY'] : ['TOKEN', 'PRIVATE_KEY'],
                  ),
                )
              }}
            >
              {Object.entries(protocolLabel).map(([key, value]) => (
                <option key={key} value={key}>
                  {value}
                </option>
              ))}
            </select>
          </Field>
        </div>
        {kind === 'http' ? (
          <Field
            label="Базовый URL"
            error={fields['config.baseUrl']}
            hint="Путь /api сохранится при запросе RESOURCE/task."
          >
            <input
              required
              className="mono"
              placeholder="https://gateway.local/api"
              value={config.baseUrl ?? ''}
              onChange={(e) => change({ baseUrl: e.target.value })}
            />
          </Field>
        ) : (
          <Field label="Адрес сервера" error={fields['config.address']}>
            <input
              required
              className="mono"
              placeholder="localhost:50051"
              value={config.address ?? ''}
              onChange={(e) => change({ address: e.target.value })}
            />
          </Field>
        )}
        {kind === 'postgresql' && (
          <div className="form-grid">
            <Field label="База данных" error={fields['config.database']}>
              <input
                required
                value={config.database ?? ''}
                onChange={(e) => change({ database: e.target.value })}
              />
            </Field>
            <Field label="Пользователь PostgreSQL">
              <input
                required
                value={config.username ?? ''}
                onChange={(e) => change({ username: e.target.value })}
              />
            </Field>
          </div>
        )}
        {kind !== 'postgresql' && (
          <Pairs
            label={kind === 'http' ? 'Общие заголовки' : 'Общие метаданные'}
            value={config.headers}
            onChange={(v) => change({ headers: v })}
          />
        )}
        <Field label="Защита подключения" error={fields['config.tls.mode']}>
          <select
            value={config.tls.mode}
            onChange={(e) => tls({ mode: e.target.value as 'tls' | 'plaintext' })}
          >
            <option value="tls">TLS — с проверкой сертификата</option>
            <option value="plaintext">Без TLS — plaintext</option>
          </select>
        </Field>
        {config.tls.mode === 'tls' && (
          <details className="tls-settings">
            <summary>CA и клиентский сертификат</summary>
            <Field label="Имя сервера TLS">
              <input
                className="mono"
                value={config.tls.serverName ?? ''}
                placeholder="По умолчанию — имя хоста"
                onChange={(e) => tls({ serverName: e.target.value })}
              />
            </Field>
            <Field label="CA-сертификат PEM" error={fields['config.tls.ca']}>
              <textarea
                className="mono"
                rows={3}
                value={config.tls.ca ?? ''}
                placeholder="Необязательно, системные CA используются автоматически"
                onChange={(e) => tls({ ca: e.target.value })}
              />
            </Field>
            <Field
              label="Клиентский сертификат PEM"
              error={fields['config.tls.certificate']}
              hint="Для mTLS задайте также приватный ключ в секретах."
            >
              <textarea
                className="mono"
                rows={3}
                value={config.tls.certificate ?? ''}
                onChange={(e) => tls({ certificate: e.target.value })}
              />
            </Field>
          </details>
        )}
        <Secrets rows={secrets} change={setSecrets} presets={presets} />
        {kind === 'grpc' && (
          <fieldset className="proto-files">
            <legend>Описание gRPC</legend>
            <p className="muted small">
              Без файлов используется server reflection. Если его нет, загрузите .proto вместе с
              импортами.
            </p>
            <div className="inline">
              <label className="file-button">
                Добавить .proto
                <input
                  type="file"
                  multiple
                  accept=".proto"
                  onChange={(e) => void upload(e.target.files)}
                />
              </label>
              <label className="file-button">
                Папка с импортами
                <input
                  type="file"
                  multiple
                  {...{ webkitdirectory: '' }}
                  onChange={(e) => void upload(e.target.files)}
                />
              </label>
            </div>
            {Object.keys(config.protoFiles ?? {})
              .sort()
              .map((file) => (
                <div className="row-heading proto-file" key={file}>
                  <code>{file}</code>
                  <button
                    type="button"
                    className="text-button danger"
                    onClick={() => {
                      const files = { ...config.protoFiles }
                      delete files[file]
                      change({ protoFiles: files })
                    }}
                  >
                    Удалить
                  </button>
                </div>
              ))}
            {fields['config.protoFiles'] && (
              <p className="field-error">{fields['config.protoFiles']}</p>
            )}
          </fieldset>
        )}
        <div className="action-bar">
          <span className="muted small">Изменения применятся к новым прогонам.</span>
          <button className="button" type="submit">
            {busy ? 'Сохранение…' : 'Сохранить ресурс'}
          </button>
        </div>
      </fieldset>
    </form>
  )
}
