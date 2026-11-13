import { useRef, useState } from 'react'
import type {
  Environment,
  Resource,
  ResourceConfig,
  SecretChanges,
} from './api'
import { all, fieldErrors, message, protocolLabel, request } from './api'
import { Field, Modal, Notice } from './ui'
import { HeaderEditor, TLSFields } from './HeaderEditor'
import { PemUpload } from './PemUpload'
import { ResourceVariables, environmentVariableWrite, variableMap, type VariableRow } from './ResourceVariables'
import { FileReadContext, useFileRead, useFileReadScope } from './FileReads'
import { ReadOnlyNotice } from './ReadOnlyNotice'
import { FormErrorBanner, FormFeedbackContext } from './FormFeedback'

type SecretRow = {
  name: string
  mode: 'keep' | 'replace' | 'clear'
  value: string
  originalName?: string
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
    originalName: names.includes(name) ? name : undefined,
  }))
}
function secretChanges(rows: SecretRow[]): SecretChanges {
  const replace: Record<string, string> = {},
    clear: string[] = []
  const names = new Set<string>()
  for (const row of rows) {
    if (!row.name.trim()) continue
    if (names.has(row.name))
      throw new Error('Имена секретов должны быть уникальными')
    names.add(row.name)
    if (row.originalName && row.originalName !== row.name) {
      if (row.mode !== 'replace')
        throw new Error('Для переименования секрета задайте новое значение')
      clear.push(row.originalName)
    }
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
    <fieldset className="secret-editor" data-error-section="secrets">
      <legend>Секреты</legend>
      <p className="muted small">
        Сохранённые значения не показываются. Для заголовков и метаданных
        используйте ${'{TOKEN}'}.
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
                onChange={(e) =>
                  update(i, { name: e.target.value.toUpperCase() })
                }
                placeholder="TOKEN_NAME"
              />
            </Field>
            <Field label={`Действие: ${row.name}`}>
              <select
                value={row.mode}
                onChange={(e) =>
                  update(i, {
                    mode: e.target.value as SecretRow['mode'],
                    value: '',
                  })
                }
              >
                <option value="keep">
                  {row.exists ? 'Оставить сохранённое' : 'Не задавать'}
                </option>
                <option value="replace">Задать новое значение</option>
                <option value="clear">Удалить значение</option>
              </select>
            </Field>
          </div>
          {row.name === 'PRIVATE_KEY' && <PemUpload label="Загрузить приватный ключ из файла" kind="key" change={(value) => update(i, { mode: 'replace', value })} />}
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
        onClick={() =>
          change([
            ...rows,
            { name: '', value: '', mode: 'replace', exists: false },
          ])
        }
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
  const [variablesOpen, setVariablesOpen] = useState(false)
  const environment = environments.find((e) => e.id === environmentId)
  return (
    <section className="resources-page">
      <div className="page-heading">
        <div>
          <p className="section-label">ПОДКЛЮЧЕНИЯ И ПЕРЕМЕННЫЕ</p>
          <h1>Окружения и ресурсы</h1>
          <p className="muted">
            Адреса и сертификаты задаются здесь, сценарии используют имена
            ресурсов.
          </p>
        </div>
        <button
          className="button secondary"
          onClick={() => setEnvDialog('new')}
        >
          + Окружение
        </button>
      </div>
      {!environment ? (
        <div className="empty-card">
          <h2>Создайте первое окружение</h2>
          <p className="muted">
            Например, dev или stage. Затем добавьте HTTP, gRPC или
            PostgreSQL-ресурс.
          </p>
          <button className="button" onClick={() => setEnvDialog('new')}>
            Создать окружение
          </button>
        </div>
      ) : (
        <>
          {environment.readOnly && <ReadOnlyNotice environment />}
          <div className="environment-banner">
            <div>
              <strong>{environment.name}</strong>
              <p className="muted">{environment.description}</p>
              <span className="muted small">
                {Object.keys(environment.variables).length} открытых переменных
                · {Object.keys(environment.httpVariables ?? {}).length} HTTP-источников
                · {environment.secretNames.length} секретных
              </span>
            </div>
            <button
              className="button secondary"
              disabled={environment.readOnly}
              onClick={() => setEnvDialog(environment)}
            >
              Настроить окружение
            </button>
          </div>
          <section className="environment-variables">
            <div className="row-heading">
              <h3>Переменные</h3>
              <button
                className="text-button"
                disabled={environment.readOnly}
                onClick={() => setVariablesOpen(true)}
              >
                Настроить переменные
              </button>
            </div>
            {!Object.keys(environment.variables).length &&
              !Object.keys(environment.httpVariables ?? {}).length &&
              !environment.secretNames.length && (
                <p className="muted small">Переменных пока нет.</p>
              )}
            <div className="variable-list">
              {Object.entries(environment.variables)
                .sort(([a], [b]) => a.localeCompare(b))
                .map(([name, value]) => (
                  <div key={name}>
                    <code>{name}</code>
                    <span className="badge">Открытая</span>
                    <span>{value || 'Пустая строка'}</span>
                  </div>
                ))}
              {Object.entries(environment.httpVariables ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, source]) => (
                <div key={name}>
                  <code>{name}</code><span className="badge">Из HTTP-запроса</span>
                  <span className="muted">{source.method} {source.url} · {source.fetchMode === 'per_run' ? 'Один раз на прогон' : 'Перед каждым вызовом'}</span>
                </div>
              ))}
              {environment.secretNames.map((name) => (
                <div key={name}>
                  <code>{name}</code>
                  <span className="badge">Секретная</span>
                  <span className="muted">Значение задано</span>
                </div>
              ))}
            </div>
            {variablesOpen && (
              <VariablesForm
                initial={environment}
                close={() => setVariablesOpen(false)}
                saved={async () => {
                  await reload()
                  setVariablesOpen(false)
                }}
              />
            )}
          </section>
          <div className="resources-grid">
            <aside className="resource-list">
              <div className="row-heading">
                <h3>Ресурсы</h3>
                <button
                  className="text-button"
                  disabled={environment.readOnly}
                  onClick={() => setEditing('new')}
                >
                  + Добавить
                </button>
              </div>
              {resources.length === 0 && (
                <p className="muted small">Ресурсов пока нет.</p>
              )}
              {resources.map((r) => (
                <button
                  key={r.id}
                  className={`resource-item ${editing !== 'new' && editing?.id === r.id ? 'selected' : ''}`}
                  onClick={() => setEditing(r)}
                >
                  <span className={`protocol ${r.kind}`}>
                    {protocolLabel[r.kind]}
                  </span>
                  <strong>{r.name}</strong>
                  <small>{r.config.baseUrl || r.config.address}</small>
                </button>
              ))}
            </aside>
            <div>
              {editing ? (
                <ResourceForm
                  key={
                    editing === 'new'
                      ? 'new'
                      : `${editing.id}:${editing.revision}`
                  }
                  initial={editing === 'new' ? undefined : editing}
                  environmentId={environmentId}
                  environment={environment}
                  environmentsSecretNames={environment.secretNames}
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
                    Именованный ресурс объединяет адрес, параметры подключения и
                    сертификаты.
                  </p>
                  <button
                    className="button"
                    disabled={environment.readOnly}
                    onClick={() => setEditing('new')}
                  >
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
    [description, setDescription] = useState(initial?.description ?? ''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const env = await request<Environment>(
        initial ? '/environments/' + initial.id : '/environments',
        initial ? 'PUT' : 'POST',
        { name, description, revision: initial?.revision ?? 0 },
      )
      await saved(env)
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      title={initial ? 'Настройки окружения' : 'Новое окружение'}
      onClose={close}
    >
      <form onSubmit={(e) => void submit(e)}>
        <fieldset className="plain-fieldset" disabled={busy}>
          {error && <Notice>{error}</Notice>}
          <Field label="Имя окружения">
            <input
              autoFocus
              required
              maxLength={200}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="Описание окружения" hint="Необязательно">
            <textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
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

export function VariablesForm({
  initial,
  close,
  saved,
}: {
  initial?: Environment
  close: () => void
  saved: (env: Environment) => Promise<void>
}) {
  const form = useRef<HTMLFormElement>(null)
  const [variables, setVariables] = useState<VariableRow[]>([
    ...Object.entries(initial?.variables ?? {}).map(([name, value]) => ({
      id: crypto.randomUUID(),
      mode: 'literal' as const,
      name,
      value,
    })),
    ...Object.entries(initial?.httpVariables ?? {}).map(([name, source]) => ({ id: crypto.randomUUID(), mode: 'http' as const, name, source })),
  ])
  const files = useFileReadScope()
  const [secrets, setSecrets] = useState(
      initialSecrets(initial?.secretNames ?? []),
    ),
    [error, setError] = useState(''),
    [fields, setFields] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false)
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (files.pending || busy) return
    setBusy(true)
    setError('')
    setFields({})
    try {
      const definitions = environmentVariableWrite(variables)
      const env = await request<Environment>(
        `/environments/${initial!.id}/variables`,
        'PUT',
        {
          revision: initial?.revision ?? 0,
          ...definitions,
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
    <FormFeedbackContext.Provider value={setError}>
    <FileReadContext.Provider value={files.begin}>
    <Modal title="Переменные окружения" onClose={close} className="form-modal">
      <FormErrorBanner error={error} fields={fields} form={form} close={() => setError('')} />
      <form ref={form} onSubmit={(e) => void submit(e)}>
        <div className="modal-form-scroll">
        <fieldset disabled={busy} className="plain-fieldset">
          <ResourceVariables scope="environment" rows={variables} change={setVariables} secrets={secrets.map(row => row.name).filter(Boolean)} probe={{
            disabled: busy || files.pending > 0 || !!initial?.readOnly,
            version: JSON.stringify({ variables, secrets, revision: initial?.revision }),
            failed: text => { setError(text); setFields({ source: text }) },
            build: async () => ({ scope: 'environment', environmentId: initial!.id, environmentRevision: initial!.revision, variables: environmentVariableWrite(variables).variables, secrets: secretChanges(secrets) }),
          }} />
          {fields.variables && (
            <p className="field-error">{fields.variables}</p>
          )}
          <Secrets rows={secrets} change={setSecrets} />
        </fieldset>
        </div>
          <div className="modal-actions">
            <button type="button" className="button secondary" disabled={busy} onClick={close}>
              Отмена
            </button>
            <button className="button" type="submit" disabled={busy || files.pending > 0}>
              {busy ? 'Сохранение…' : 'Сохранить переменные'}
            </button>
          </div>
      </form>
    </Modal>
    </FileReadContext.Provider>
    </FormFeedbackContext.Provider>
  )
}

export function ResourceForm({
  initial,
  environmentId,
  environment,
  environmentsSecretNames,
  saved,
  cancel,
}: {
  initial?: Resource
  environmentId: string
  environment?: Environment
  environmentsSecretNames?: string[]
  saved: (r: Resource) => Promise<void>
  cancel: () => void
}) {
  const form = useRef<HTMLFormElement>(null)
  const [name, setName] = useState(initial?.name ?? ''),
    [kind, setKind] = useState<Resource['kind']>(initial?.kind ?? 'http')
  const [config, setConfig] = useState<ResourceConfig>(
    initial?.config
      ? {
          ...initial.config,
          tls: {
            ...initial.config.tls,
            mode:
              initial.config.tls.mode === 'tls' &&
              initial.config.tls.certificate
                ? 'mtls'
                : initial.config.tls.mode,
          },
        }
      : {
          baseUrl: '',
          address: '',
          database: '',
          username: '',
          headers: [],
          tls: { mode: 'tls' },
          protoFiles: {},
        },
  )
  const [resourceVariables, setResourceVariables] = useState<VariableRow[]>(Object.entries(initial?.config.variables ?? {}).map(([name, definition]) => ({ ...definition, name, id: crypto.randomUUID() })))
  const files = useFileReadScope()
  const presets =
    kind === 'postgresql'
      ? ['PASSWORD', 'PRIVATE_KEY']
      : ['TOKEN', 'PRIVATE_KEY']
  const [secrets, setSecrets] = useState(
      initialSecrets(initial?.secretNames ?? [], presets),
    ),
    [error, setError] = useState(''),
    [fields, setFields] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false)
  const change = (patch: Partial<ResourceConfig>) =>
    setConfig((old) => ({ ...old, ...patch }))
  const tls = (patch: Partial<ResourceConfig['tls']>) =>
    change({ tls: { ...config.tls, ...patch } })
  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (files.pending || busy || initial?.readOnly) return
    setBusy(true)
    setError('')
    setFields({})
    try {
      const result = await request<Resource>(
        initial
          ? `/resources/${initial.id}`
          : `/environments/${environmentId}/resources`,
        initial ? 'PUT' : 'POST',
        {
          name,
          kind,
          revision: initial?.revision ?? 0,
          config: { ...config, variables: variableMap(resourceVariables) },
          secrets: secretChanges(secrets),
        },
      )
      await saved(result)
    } catch (e) {
      setError(message(e))
      setFields(fieldErrors(e))
    } finally {
      setBusy(false)
    }
  }
  const readProto = useFileRead<Record<string, string>>(values => setConfig(old => ({ ...old, protoFiles: { ...old.protoFiles, ...values } })), setError, files.begin)
  const upload = async (list: FileList | null) => {
    if (!list) return
    const selected = Array.from(list)
    await readProto(async () => {
      const files = selected
      if (
        files.reduce((n, f) => n + f.size, 0) > 5 * 1024 * 1024 ||
        files.length > 100
      )
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
      return Object.fromEntries(values)
    })
  }
  return (
    <FormFeedbackContext.Provider value={setError}>
    <FileReadContext.Provider value={files.begin}>
    <form ref={form} className="resource-form" onSubmit={(e) => void submit(e)}>
      <FormErrorBanner error={error} fields={fields} form={form} close={() => setError('')} />
      {initial?.config.unavailableReason === 'demo_sql_unavailable' && (
        <Notice>
          В YAML-режиме демонстрационная PostgreSQL не настроена. SQL-шаг
          доступен для запуска и вернёт ошибку. Пользовательские SQL-ресурсы
          могут обращаться к внешней БД.
        </Notice>
      )}
      <fieldset disabled={busy || initial?.readOnly} className="plain-fieldset">
        <div className="row-heading">
          <h2>{initial ? initial.name : 'Новый ресурс'}</h2>
          <button type="button" className="text-button" onClick={cancel}>
            Закрыть
          </button>
        </div>
        <div className="form-grid">
          <Field
            label="Имя ресурса"
            error={fields.name}
            hint="Имя для ссылок в запросах."
          >
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
              disabled={!!initial || files.pending > 0}
              onChange={(e) => {
                const next = e.target.value as Resource['kind']
                setKind(next)
                setSecrets(
                  initialSecrets(
                    [],
                    next === 'postgresql'
                      ? ['PASSWORD', 'PRIVATE_KEY']
                      : ['TOKEN', 'PRIVATE_KEY'],
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
        {kind !== 'postgresql' && <p className="muted small">Общие заголовки или метаданные добавляются ко всем запросам этого ресурса. Значение в отдельном вызове перекрывает общее имя без учёта регистра.</p>}
        {kind !== 'postgresql' && (
          <div data-error-section="headers">
          <HeaderEditor
            secrets={[
              ...(environmentsSecretNames ?? []),
              ...secrets.map((s) => s.name),
            ]}
            label={kind === 'http' ? 'Общие заголовки' : 'Общие метаданные'}
            value={config.headers}
            onChange={(v) => change({ headers: v })}
          />
          </div>
        )}
        <ResourceVariables rows={resourceVariables} change={setResourceVariables} secrets={[...(environmentsSecretNames ?? []), ...secrets.map(s => s.name).filter(Boolean)]} probe={{
          disabled: busy || files.pending > 0 || !!initial?.readOnly,
          version: JSON.stringify({ resourceVariables, secrets, config, revision: initial?.revision, environmentRevision: environment?.revision }),
          failed: text => { setError(text); setFields({ source: text }) },
          build: async () => {
            const env = environment ?? (await all<Environment>('/environments')).find(value => value.id === environmentId)
            if (!env) throw new Error('Выберите окружение для проверки')
            return { scope: 'resource', environmentId, environmentRevision: env.revision, resourceId: initial?.id, resourceRevision: initial?.revision, resourceVariables: variableMap(resourceVariables), secrets: secretChanges(secrets) }
          },
        }} />
        <div data-error-section="tls"><TLSFields config={config.tls} change={tls} /></div>
        <Secrets rows={secrets} change={setSecrets} presets={presets} />
        {kind === 'grpc' && (
          <fieldset className="proto-files" data-error-section="proto">
            <legend>Описание gRPC</legend>
            <p className="muted small">
              Без файлов используется server reflection. Если его нет, загрузите
              .proto вместе с импортами.
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
          <span className="muted small">
            Изменения применятся к новым прогонам.
          </span>
          <button className="button" type="submit" disabled={files.pending > 0}>
            {initial?.readOnly
              ? 'Системное подключение · только просмотр'
              : busy
                ? 'Сохранение…'
                : files.pending > 0 ? 'Чтение файлов…' : 'Сохранить ресурс'}
          </button>
        </div>
      </fieldset>
    </form>
    </FileReadContext.Provider>
    </FormFeedbackContext.Provider>
  )
}
