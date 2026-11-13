import { useEffect, useRef, useState } from 'react'
import type {
  Attempt,
  DemoInfo,
  Environment,
  Folder,
  Resource,
  Run,
  TestCase,
} from './api'
import {
  all,
  command,
  fieldErrors,
  message,
  RequestError,
  request,
} from './api'
import { Field, Modal, Notice } from './ui'
import CaseEditor from './CaseEditor'
import JiraExport from './JiraExport'
import Sidebar from './Sidebar'
import ResourcesPanel from './ResourcesPanel'
import RunView, { mergeRun } from './RunView'
import { ReadOnlyNotice } from './ReadOnlyNotice'
import History from './History'
import { ThemeSwitch } from './ThemeSwitch'
import { readPreference, writePreference } from './preferences'
import CallLogs, { CallLogsProvider } from './CallLogs'

type NameDialog = {
  allowEmpty?: boolean
  title: string
  initial: string
  submit: (name: string) => Promise<void>
}
const clone = <T,>(value: T): T => structuredClone(value)
const content = (c: TestCase | null) =>
  c ? JSON.stringify({ name: c.name, steps: c.steps }) : ''

export default function App() {
  const [jiraOpen, setJiraOpen] = useState(false)
  const [demo, setDemo] = useState<DemoInfo | null>(null)
  const demoRef = useRef<DemoInfo | null>(null),
    userEnvironment = useRef(readPreference('qag.environment') ?? '')
  const [view, setView] = useState<'cases' | 'resources' | 'logs'>('cases'),
    [archive, setArchive] = useState(false)
  const [folders, setFolders] = useState<Folder[]>([]),
    [cases, setCases] = useState<Record<string, TestCase[]>>({}),
    [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [environments, setEnvironments] = useState<Environment[]>([]),
    [environmentId, setEnvironmentId] = useState(
      readPreference('qag.environment') ?? '',
    ),
    [resources, setResources] = useState<Resource[]>([])
  const [document, setDocument] = useState<TestCase | null>(null),
    [saved, setSaved] = useState<TestCase | null>(null),
    [selectedId, setSelectedId] = useState(readPreference('qag.case') ?? '')
  const [tab, setTab] = useState<'editor' | 'run' | 'history'>('editor'),
    [runId, setRunId] = useState(''),
    [run, setRun] = useState<Run | null>(null)
  const [attempts, setAttempts] = useState<Record<string, Attempt>>({}),
    [historyVersion, setHistoryVersion] = useState(0)
  const [error, setError] = useState(''),
    [fields, setFields] = useState<Record<string, string>>({}),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [loadingCase, setLoadingCase] = useState(false)
  const [nameDialog, setNameDialog] = useState<NameDialog | null>(null),
    [pending, setPending] = useState<(() => void) | null>(null)
  const current = useRef<TestCase | null>(null),
    selection = useRef(selectedId),
    environment = useRef(environmentId)
  const dirty = content(document) !== content(saved),
    archived = !!(document?.archived || document?.parentArchived),
    readonly = archived || !!document?.readOnly
  const writeDocument = (value: TestCase | null) => {
    current.current = value
    setDocument(value)
  }
  const chooseEnvironment = (id: string, persist = true) => {
    environment.current = id
    if (persist) {
      writePreference('qag.environment', id)
      userEnvironment.current = id
    }
    setEnvironmentId(id)
  }
  const fail = (e: unknown) => {
    setError(message(e))
    setFields(fieldErrors(e))
  }
  const guard = (action: () => void) => {
    if (busy) return
    if (busy) return
    if (content(current.current) !== content(saved)) setPending(() => action)
    else action()
  }
  const reloadFolders = async () =>
    setFolders(await all<Folder>(`/folders?archived=${archive}`))
  const reloadFolder = async (id: string) => {
    const values = await all<TestCase>(`/folders/${id}/test-cases`)
    setCases((old) => ({ ...old, [id]: values }))
  }
  const reloadResources = async () => {
    const values = await all<Environment>('/environments')
    setEnvironments(values)
    const id = values.some((e) => e.id === environment.current)
      ? environment.current
      : (values.find((e) => !e.readOnly)?.id ?? '')
    if (id !== environment.current) chooseEnvironment(id)
    setResources(id ? await all<Resource>(`/environments/${id}/resources`) : [])
  }
  const accept = (c: TestCase) => {
    if (c.readOnly && demoRef.current)
      chooseEnvironment(demoRef.current.environmentId, false)
    else if (current.current?.readOnly)
      chooseEnvironment(userEnvironment.current, false)
    current.current = clone(c)
    setDocument(clone(c))
    setSaved(c)
    setFields({})
    selection.current = c.id
    setSelectedId(c.id)
    writePreference('qag.case', c.id)
  }
  const loadCase = async (id: string) => {
    if (current.current?.readOnly && id !== demoRef.current?.testCaseId)
      chooseEnvironment(userEnvironment.current, false)
    selection.current = id
    setSelectedId(id)
    writePreference('qag.case', id)
    writeDocument(null)
    setSaved(null)
    setLoadingCase(true)
    setTab('editor')
    setAttempts({})
    setRun(null)
    setRunId(sessionStorage.getItem(`qag.run.${id}`) ?? '')
    setError('')
    try {
      const [c, active] = await Promise.all([
        request<TestCase>(`/test-cases/${id}`),
        request<{ items: Run[] }>(`/test-cases/${id}/runs?active=true`),
      ])
      if (selection.current === id) {
        accept(c)
        const liveRun = active.items[0]
        if (liveRun) {
          setRun(liveRun)
          setRunId(liveRun.id)
          sessionStorage.setItem(`qag.run.${id}`, liveRun.id)
          setTab('run')
        }
      }
    } catch (e) {
      if (selection.current === id) fail(e)
    } finally {
      if (selection.current === id) setLoadingCase(false)
    }
  }
  const selectCase = (id: string) =>
    guard(() => {
      setView('cases')
      void loadCase(id)
    })
  useEffect(() => {
    let live = true
    void Promise.all([
      all<Environment>('/environments'),
      request<DemoInfo>('/demo'),
    ])
      .then(([values, info]) => {
        if (!live) return
        demoRef.current = info
        setDemo(info)
        setEnvironments(values)
        chooseEnvironment(
          values.some((e) => e.id === environment.current)
            ? environment.current
            : (values.find((e) => !e.readOnly)?.id ?? ''),
        )
        if (selection.current) void loadCase(selection.current)
      })
      .catch(fail)
    return () => {
      live = false
    }
  }, [])
  useEffect(() => {
    let live = true
    setLoading(true)
    void all<Folder>(`/folders?archived=${archive}`)
      .then((values) => {
        if (live) setFolders(values)
      })
      .catch(fail)
      .finally(() => {
        if (live) setLoading(false)
      })
    return () => {
      live = false
    }
  }, [archive])
  useEffect(() => {
    if (!environmentId) {
      setResources([])
      return
    }
    let live = true
    void all<Resource>(`/environments/${environmentId}/resources`)
      .then((values) => {
        if (live) setResources(values)
      })
      .catch(fail)
    return () => {
      live = false
    }
  }, [environmentId])
  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [dirty])
  useEffect(() => {
    if (view !== 'cases' || tab !== 'run' || !runId) return
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const next = await request<Run>(`/runs/${runId}`)
        if (!live) return
        setRun((old) => mergeRun(old, next))
        if (next.status === 'running')
          timer = setTimeout(() => void poll(), 800)
      } catch (e) {
        if (!live) return
        fail(e)
        if (e instanceof RequestError && e.status === 404) {
          setRunId('')
          sessionStorage.removeItem(`qag.run.${selection.current}`)
        } else timer = setTimeout(() => void poll(), 2000)
      }
    }
    void poll()
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [view, tab, runId])
  const pendingAttempts = Object.values(attempts)
    .filter((a) => a.state === 'queued' || a.state === 'running')
    .map((a) => a.id)
    .sort()
    .join(',')
  useEffect(() => {
    if (!pendingAttempts) return
    let live = true
    const caseId = selection.current
    const ids = pendingAttempts.split(',')
    const poll = () => {
      void Promise.all(ids.map((id) => request<Attempt>(`/attempts/${id}`)))
        .then((values) => {
          if (live && selection.current === caseId)
            setAttempts((old) => {
              const next = { ...old }
              for (const a of values)
                if (!next[a.blockId]?.finishedAt || a.finishedAt)
                  next[a.blockId] = a
              return next
            })
        })
        .catch(fail)
    }
    const timer = setInterval(poll, 700)
    poll()
    return () => {
      live = false
      clearInterval(timer)
    }
  }, [pendingAttempts, selectedId])
  const save = async () => {
    const c = current.current
    if (!c) throw new Error('Выберите тест-кейс')
    setBusy(true)
    setError('')
    setFields({})
    try {
      const result = await request<TestCase>(`/test-cases/${c.id}`, 'PUT', {
        name: c.name,
        revision: c.revision,
        steps: c.steps,
      })
      if (selection.current === c.id) accept(result)
      void reloadFolder(c.folderId).catch(fail)
      return result
    } catch (e) {
      fail(e)
      throw e
    } finally {
      setBusy(false)
    }
  }
  const mutate = async (action: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await action()
    } catch (e) {
      fail(e)
    } finally {
      setBusy(false)
    }
  }
  const createFolder = () =>
    guard(() =>
      setNameDialog({
        title: 'Новая папка',
        initial: '',
        submit: async (name) => {
          const f = await request<Folder>('/folders', 'POST', { name })
          setArchive(false)
          setExpanded((old) => new Set(old).add(f.id))
          setCases((old) => ({ ...old, [f.id]: [] }))
          setFolders(await all<Folder>('/folders?archived=false'))
        },
      }),
    )
  const createCase = (f: Folder) =>
    guard(() =>
      setNameDialog({
        title: 'Новый тест-кейс',
        allowEmpty: true,
        initial: '',
        submit: async (name) => {
          const c = await request<TestCase>(
            `/folders/${f.id}/test-cases`,
            'POST',
            { name },
          )
          setExpanded((old) => new Set(old).add(f.id))
          setView('cases')
          setTab('editor')
          setAttempts({})
          setRun(null)
          setRunId('')
          accept(c)
          void reloadFolder(f.id).catch(fail)
        },
      }),
    )
  const renameFolder = (f: Folder) =>
    guard(() =>
      setNameDialog({
        title: 'Переименовать папку',
        initial: f.name,
        submit: async (name) => {
          await request(`/folders/${f.id}`, 'PATCH', { name })
          await reloadFolders()
        },
      }),
    )
  const patchFolder = (f: Folder) =>
    guard(
      () =>
        void mutate(async () => {
          await request(`/folders/${f.id}`, 'PATCH', { archived: !f.archived })
          await reloadFolders()
          if (current.current?.folderId === f.id)
            accept(await request<TestCase>(`/test-cases/${current.current.id}`))
        }),
    )
  const renameCase = (c: TestCase) =>
    guard(() =>
      setNameDialog({
        title: 'Переименовать тест-кейс',
        allowEmpty: true,
        initial: c.name,
        submit: async (name) => {
          const changed = await request<TestCase>(
            `/test-cases/${c.id}`,
            'PATCH',
            { name },
          )
          await reloadFolder(c.folderId)
          if (selection.current === c.id) accept(changed)
        },
      }),
    )
  const patchCase = (c: TestCase) =>
    guard(
      () =>
        void mutate(async () => {
          const changed = await request<TestCase>(
            `/test-cases/${c.id}`,
            'PATCH',
            {
              archived: !c.archived,
            },
          )
          await reloadFolder(c.folderId)
          if (selection.current === c.id) accept(changed)
        }),
    )
  const toggleFolder = (f: Folder) => {
    const open = !expanded.has(f.id)
    setExpanded((old) => {
      const next = new Set(old)
      if (open) next.add(f.id)
      else next.delete(f.id)
      return next
    })
    if (open && !cases[f.id]) void reloadFolder(f.id).catch(fail)
  }
  const startRun = (executeAll = false) =>
    guard(
      () =>
        void mutate(async () => {
          const c = current.current
          if (!c) return
          const value = await command<Run>(`/test-cases/${c.id}/runs`, {
            environmentId: environment.current,
            revision: c.revision,
            executeAll,
          })
          sessionStorage.setItem(`qag.run.${c.id}`, value.id)
          if (selection.current === c.id) {
            setRun(value)
            setRunId(value.id)
            setTab('run')
            setHistoryVersion((v) => v + 1)
          }
        }),
    )
  const launch = (blockId: string) =>
    guard(
      () =>
        void mutate(async () => {
          const c = current.current
          if (!c) return
          const a = await command<Attempt>(
            `/test-cases/${c.id}/blocks/${blockId}/attempts`,
            {
              environmentId: environment.current,
              revision: c.revision,
            },
          )
          if (selection.current === c.id)
            setAttempts((old) => ({ ...old, [blockId]: a }))
        }),
    )
  const cancelAttempt = (id: string) =>
    void mutate(async () => {
      const a = await request<Attempt>(`/attempts/${id}/cancel`, 'POST')
      if (selection.current === a.testCaseId)
        setAttempts((old) => ({ ...old, [a.blockId]: a }))
    })
  return (
    <CallLogsProvider>
    <div className="app-shell">
      {jiraOpen && saved && <JiraExport saved={saved} dirty={dirty} save={save} close={() => setJiraOpen(false)} />}
      <Sidebar
        view={view}
        navigate={(v) =>
          guard(() => {
            setView(v)
            if (v === 'cases' && current.current?.readOnly && demoRef.current)
              chooseEnvironment(demoRef.current.environmentId, false)
          })
        }
        demoCaseId={demo?.testCaseId}
        archive={archive}
        setArchive={setArchive}
        folders={folders}
        cases={cases}
        expanded={expanded}
        selected={selectedId}
        loading={loading}
        busy={busy}
        toggle={toggleFolder}
        open={selectCase}
        createFolder={createFolder}
        createCase={createCase}
        renameFolder={renameFolder}
        patchFolder={patchFolder}
        renameCase={renameCase}
        patchCase={patchCase}
      />
      <main className="workspace">
        <header className="toolbar">
          <div className="inline">
            <span className="breadcrumb">
              {view === 'resources'
                ? 'Настройки подключений'
                : view === 'logs' ? 'Журнал вызовов'
                : document?.name || 'Тест-кейсы'}
            </span>
            {dirty && view === 'cases' && (
              <span
                className="unsaved-dot"
                title="Есть несохранённые изменения"
              />
            )}
          </div>
          <div className="inline">
            <label className="environment-select">
              <span>Окружение</span>
              <select
                aria-label="Текущее окружение"
                disabled={view === 'cases' && !!document?.readOnly}
                value={environmentId}
                onChange={(e) => chooseEnvironment(e.target.value)}
              >
                <option value="">
                  {environments.length
                    ? 'Выберите окружение'
                    : 'Окружений пока нет'}
                </option>
                {environments.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name}
                  </option>
                ))}
              </select>
            </label>
            <ThemeSwitch />
          </div>
        </header>
        {error && (
          <div className="global-notice">
            <Notice
              onClose={() => {
                setError('')
                setFields({})
              }}
            >
              {error}
            </Notice>
          </div>
        )}
        {view === 'logs' ? <CallLogs /> : view === 'resources' ? (
          <ResourcesPanel
            key={environmentId}
            environments={environments}
            environmentId={environmentId}
            resources={resources}
            reload={reloadResources}
            selectEnvironment={chooseEnvironment}
          />
        ) : loadingCase ? (
          <p className="loading">Загрузка сценария…</p>
        ) : !document ? (
          <section className="welcome">
            <div className="welcome-mark">q.</div>
            <p className="section-label">РУЧНАЯ ПРОВЕРКА. ТОЧНЫЙ РЕЗУЛЬТАТ.</p>
            <h1>
              От сценария
              <br />к результату.
            </h1>
            <p className="muted">
              Создайте папку и тест-кейс. Добавьте ручные шаги и вызовы — ответы
              будут рядом с ожиданиями.
            </p>
            <div className="inline">
              <button className="button" onClick={createFolder}>
                Создать папку
              </button>
              <button
                className="button secondary"
                onClick={() => setView('resources')}
              >
                Настроить ресурсы
              </button>
            </div>
          </section>
        ) : (
          <>
            <div className="document-header">
              <div className="page-heading">
                <div>
                  <p className="section-label">
                    ТЕСТ-КЕЙС · ВЕРСИЯ {document.revision}
                  </p>
                  <h1>{saved?.name}</h1>
                </div>
                <div className="inline">
                  {archived && (
                    <span className="badge">Архив · только просмотр</span>
                  )}
                  {document.readOnly && (
                    <span className="badge">
                      Встроенный пример · только просмотр
                    </span>
                  )}

                  {tab === 'editor' && (
                    <>
                      <button className="button secondary" disabled={busy} onClick={() => setJiraOpen(true)}>Сценарий для Jira</button>
                      <span className="muted small">
                        {dirty ? 'Есть изменения' : 'Сохранено'}
                      </span>
                      <button
                        className="button secondary"
                        disabled={readonly || busy || !dirty}
                        onClick={() => void save().catch(() => undefined)}
                      >
                        {busy ? 'Сохранение…' : 'Сохранить'}
                      </button>
                      <button
                        className="button"
                        disabled={
                          archived ||
                          busy ||
                          !document.steps.length ||
                          (!dirty &&
                            (document.validationIssues ?? []).length > 0) ||
                          !environmentId
                        }
                        title={!environmentId ? 'Выберите окружение' : ''}
                        onClick={() => startRun()}
                      >
                        Начать прогон
                      </button>
                      <button
                        className="button secondary"
                        disabled={
                          archived ||
                          busy ||
                          !document.steps.length ||
                          (!dirty &&
                            (document.validationIssues ?? []).length > 0) ||
                          !environmentId
                        }
                        title={!environmentId ? 'Выберите окружение' : ''}
                        onClick={() => startRun(true)}
                      >
                        Выполнить всё
                      </button>
                    </>
                  )}
                </div>
              </div>
              {tab === 'editor' && document.readOnly && <ReadOnlyNotice />}
              {tab === 'editor' &&
                !dirty &&
                (document.validationIssues ?? []).length > 0 && (
                  <section
                    className="readiness"
                    aria-label="Готовность к запуску"
                  >
                    <strong>Черновик сохранён</strong>
                    <p>
                      Для полного прогона заполните отмеченные параметры.
                      Готовые блоки можно запускать отдельно.
                    </p>
                    <ul>
                      {document.validationIssues.map((issue, i) => (
                        <li key={i}>{issue.message}</li>
                      ))}
                    </ul>
                  </section>
                )}
              <div className="document-tabs">
                <button
                  className={tab === 'editor' ? 'selected' : ''}
                  onClick={() => setTab('editor')}
                >
                  Редактор <span>{document.steps.length}</span>
                </button>
                <button
                  className={tab === 'run' ? 'selected' : ''}
                  onClick={() => setTab('run')}
                >
                  Прогон{' '}
                  {run?.status === 'running' && <i className="live-dot" />}
                </button>
                <button
                  className={tab === 'history' ? 'selected' : ''}
                  onClick={() => setTab('history')}
                >
                  История
                </button>
              </div>
            </div>
            {tab === 'editor' && (
              <CaseEditor
                document={document}
                dirty={dirty}
                onBusy={setBusy}
                secrets={
                  environments.find((e) => e.id === environmentId)
                    ?.secretNames ?? []
                }
                resources={resources}
                fields={fields}
                disabled={readonly || busy}
                canExecute={!archived}
                busy={busy}
                environmentId={environmentId}
                attempts={attempts}
                change={(next) => {
                  if (selection.current === next.id) writeDocument(next)
                }}
                launch={launch}
                cancel={cancelAttempt}
              />
            )}
            {tab === 'run' &&
              (runId ? (
                run ? (
                  <RunView
                    key={run.id}
                    run={run}
                    update={(next) => setRun((old) => mergeRun(old, next))}
                    onRefresh={() => setHistoryVersion((v) => v + 1)}
                  />
                ) : (
                  <p className="loading">Загрузка прогона…</p>
                )
              ) : (
                <section className="empty-card">
                  <h2>Начните ручное прохождение</h2>
                  <p className="muted">
                    Сценарий и настройки ресурсов будут зафиксированы на начало
                    прогона.
                  </p>
                  <button
                    className="button"
                    disabled={
                      archived ||
                      !document.steps.length ||
                      !environmentId ||
                      busy
                    }
                    onClick={() => startRun()}
                  >
                    Начать прогон
                  </button>
                  <button
                    className="button secondary"
                    disabled={
                      archived ||
                      !document.steps.length ||
                      !environmentId ||
                      busy
                    }
                    onClick={() => startRun(true)}
                  >
                    Выполнить всё
                  </button>
                </section>
              ))}
            {tab === 'history' && (
              <History
                key={document.id}
                caseId={document.id}
                version={historyVersion}
              />
            )}
          </>
        )}
      </main>
      {nameDialog && (
        <NameForm dialog={nameDialog} close={() => setNameDialog(null)} />
      )}
      {pending && (
        <Modal
          title="Несохранённые изменения"
          onClose={() => {
            if (!busy) setPending(null)
          }}
        >
          <p>В тест-кейсе есть изменения. Сохранить их перед продолжением?</p>
          <div className="modal-actions">
            <button
              className="button secondary"
              disabled={busy}
              onClick={() => setPending(null)}
            >
              Остаться
            </button>
            <button
              className="button secondary danger"
              disabled={busy}
              onClick={() => {
                const action = pending
                writeDocument(saved ? clone(saved) : null)
                setPending(null)
                action()
              }}
            >
              Отбросить изменения
            </button>
            <button
              className="button"
              disabled={busy}
              onClick={() => {
                const action = pending
                void save()
                  .then(() => {
                    setPending(null)
                    action()
                  })
                  .catch(() => undefined)
              }}
            >
              Сохранить и продолжить
            </button>
          </div>
        </Modal>
      )}
    </div>
    </CallLogsProvider>
  )
}

function NameForm({
  dialog,
  close,
}: {
  dialog: NameDialog
  close: () => void
}) {
  const [name, setName] = useState(dialog.initial),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false)
  return (
    <Modal title={dialog.title} onClose={close}>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setBusy(true)
          setError('')
          void dialog
            .submit(name)
            .then(close)
            .catch((e) => setError(message(e)))
            .finally(() => setBusy(false))
        }}
      >
        <Field label="Название" error={error}>
          <input
            autoFocus
            required={!dialog.allowEmpty}
            maxLength={200}
            value={name}
            disabled={busy}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <div className="modal-actions">
          <button className="button secondary" type="button" onClick={close}>
            Отмена
          </button>
          <button className="button" disabled={busy}>
            {busy ? 'Сохранение…' : 'Сохранить название'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
