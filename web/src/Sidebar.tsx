import type { Folder, TestCase } from './api'

type Props = {
  demoCaseId?: string
  view: string
  navigate: (view: 'cases' | 'resources') => void
  archive: boolean
  setArchive: (v: boolean) => void
  folders: Folder[]
  cases: Record<string, TestCase[]>
  expanded: Set<string>
  selected: string
  loading: boolean
  busy: boolean
  toggle: (f: Folder) => void
  open: (id: string) => void
  createFolder: () => void
  createCase: (f: Folder) => void
  renameFolder: (f: Folder) => void
  patchFolder: (f: Folder) => void
  renameCase: (c: TestCase) => void
  patchCase: (c: TestCase) => void
}
export default function Sidebar(p: Props) {
  return (
    <aside className="sidebar">
      <a
        className="brand"
        href="/"
        onClick={(e) => {
          e.preventDefault()
          p.navigate('cases')
        }}
      >
        <img src="/qag-32.png" width="32" height="32" alt="" className="brand-icon" />qag<span> / test workspace</span>
      </a>
      <div className="section-label sidebar-label">РАБОЧЕЕ ПРОСТРАНСТВО</div>
      <button
        className={`nav-item ${p.view === 'cases' ? 'active' : ''}`}
        onClick={() => p.navigate('cases')}
      >
        <span>▤</span> Тест-кейсы
      </button>
      <button
        className={`nav-item ${p.view === 'resources' ? 'active' : ''}`}
        onClick={() => p.navigate('resources')}
      >
        <span>◇</span> Окружения и ресурсы
      </button>
      <div className="catalog-heading">
        <span className="section-label">КАТАЛОГ</span>
        <button
          className="icon-button"
          aria-label="Создать папку"
          title="Создать папку"
          disabled={p.busy}
          onClick={p.createFolder}
        >
          +
        </button>
      </div>
      <div className="catalog-tabs">
        <button className={!p.archive ? 'selected' : ''} onClick={() => p.setArchive(false)}>
          Активные
        </button>
        <button className={p.archive ? 'selected' : ''} onClick={() => p.setArchive(true)}>
          Архив
        </button>
      </div>
      {p.demoCaseId && (
        <button
          className={`builtin-example ${p.selected === p.demoCaseId ? 'selected' : ''}`}
          aria-label="Открыть встроенный пример"
          onClick={() => p.open(p.demoCaseId!)}
        >
          <span>◈</span>
          <div>
            <strong>Встроенный пример</strong>
            <small>HTTP · gRPC · PostgreSQL</small>
          </div>
        </button>
      )}
      <nav className="folder-tree" aria-label="Каталог тест-кейсов">
        {p.loading && <p className="muted small">Загрузка…</p>}
        {!p.loading && !p.folders.length && (
          <p className="muted small">
            {p.archive ? 'Архив пуст.' : 'Папок пока нет. Создайте первую.'}
          </p>
        )}
        {p.folders.map((f) => (
          <div className="folder-node" key={f.id}>
            <div className="folder-row">
              <button
                className="folder-toggle"
                aria-label={`Папка ${f.name}`}
                aria-expanded={p.expanded.has(f.id)}
                onClick={() => p.toggle(f)}
              >
                <span className="chevron">{p.expanded.has(f.id) ? '▾' : '▸'}</span>
                <span className="folder-icon" />
                <span>{f.name}</span>
              </button>
              <div className="tree-actions">
                {!f.archived && (
                  <>
                    <button
                      className="icon-button"
                      aria-label={`Добавить тест-кейс в ${f.name}`}
                      title="Создать тест-кейс"
                      onClick={() => p.createCase(f)}
                    >
                      +
                    </button>
                    <button
                      className="icon-button"
                      aria-label={`Переименовать папку ${f.name}`}
                      title="Переименовать"
                      onClick={() => p.renameFolder(f)}
                    >
                      ✎
                    </button>
                  </>
                )}
                <button
                  className="icon-button"
                  disabled={p.busy}
                  aria-label={`${f.archived ? 'Восстановить' : 'Архивировать'} папку ${f.name}`}
                  title={f.archived ? 'Восстановить' : 'В архив'}
                  onClick={() => p.patchFolder(f)}
                >
                  {f.archived ? '↥' : '↧'}
                </button>
              </div>
            </div>
            {p.expanded.has(f.id) && (
              <div className="case-list">
                {!p.cases[f.id] ? (
                  <p className="muted small">Загрузка…</p>
                ) : p.cases[f.id].length === 0 ? (
                  <div className="empty-folder">
                    <span className="muted small">Нет тест-кейсов</span>
                    {!f.archived && (
                      <button className="text-button" onClick={() => p.createCase(f)}>
                        Создать
                      </button>
                    )}
                  </div>
                ) : (
                  p.cases[f.id].map((c) => (
                    <div
                      key={c.id}
                      className={`case-row ${c.archived || f.archived ? 'archived' : ''} ${p.selected === c.id ? 'selected' : ''}`}
                    >
                      <button
                        className="case-select"
                        aria-label={`Открыть тест-кейс ${c.name}`}
                        onClick={() => p.open(c.id)}
                      >
                        <span className="case-dot" />
                        <span>{c.name}</span>
                        {c.archived && <small>Архив</small>}
                      </button>
                      {!f.archived && (
                        <div className="tree-actions">
                          {!c.archived && (
                            <button
                              className="icon-button"
                              title="Переименовать"
                              aria-label={`Переименовать тест-кейс ${c.name}`}
                              onClick={() => p.renameCase(c)}
                            >
                              ✎
                            </button>
                          )}
                          <button
                            className="icon-button"
                            disabled={p.busy}
                            title={c.archived ? 'Восстановить' : 'В архив'}
                            aria-label={`${c.archived ? 'Восстановить' : 'Архивировать'} тест-кейс ${c.name}`}
                            onClick={() => p.patchCase(c)}
                          >
                            {c.archived ? '↥' : '↧'}
                          </button>
                        </div>
                      )}
                    </div>
                  ))
                )}
              </div>
            )}
          </div>
        ))}
      </nav>
      <footer className="sidebar-footer">
        HTTP <span>·</span> gRPC <span>·</span> PostgreSQL
      </footer>
    </aside>
  )
}
