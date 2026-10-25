import type { Attempt, Block, Resource, TestCase } from './api'
import { newBlock, protocolLabel } from './api'
import { BlockEditor } from './BlockEditor'
import { Results } from './Results'
import { Field } from './ui'
import { ImageGallery } from './ImageGallery'

type Props = {
  dirty?: boolean
  onBusy?: (busy: boolean) => void
  secrets?: string[]
  document: TestCase
  resources: Resource[]
  fields: Record<string, string>
  disabled: boolean
  canExecute: boolean
  busy: boolean
  environmentId: string
  attempts: Record<string, Attempt>
  change: (c: TestCase) => void
  launch: (id: string) => void
  cancel: (id: string) => void
}
export default function CaseEditor({
  document: c,
  dirty = false,
  onBusy,
  secrets,
  resources,
  fields,
  disabled,
  canExecute,
  busy,
  environmentId,
  attempts,
  change,
  launch,
  cancel,
}: Props) {
  const updateStep = (
    index: number,
    patch: Partial<TestCase['steps'][number]>,
  ) =>
    change({
      ...c,
      steps: c.steps.map((s, n) => (n === index ? { ...s, ...patch } : s)),
    })
  const updateBlock = (step: number, index: number, block: Block) =>
    updateStep(step, {
      blocks: c.steps[step].blocks.map((b, n) => (n === index ? block : b)),
    })
  const moveStep = (index: number, direction: number) => {
    const steps = [...c.steps]
    ;[steps[index], steps[index + direction]] = [
      steps[index + direction],
      steps[index],
    ]
    change({ ...c, steps })
  }
  const moveBlock = (step: number, index: number, direction: number) => {
    const blocks = [...c.steps[step].blocks]
    ;[blocks[index], blocks[index + direction]] = [
      blocks[index + direction],
      blocks[index],
    ]
    updateStep(step, { blocks })
  }
  return (
    <section className="editor">
      <Field label="Название тест-кейса" error={fields.name}>
        <input
          className="case-name-input"
          maxLength={200}
          disabled={disabled}
          value={c.name}
          onChange={(e) => change({ ...c, name: e.target.value })}
        />
      </Field>
      {!c.steps.length && (
        <div className="empty-card">
          <h2>Первый шаг сценария</h2>
          <p className="muted">
            Опишите действие. Заголовок необязателен; блоки вызовов можно
            добавить позже.
          </p>
        </div>
      )}
      {c.steps.map((step, i) => (
        <article className="step-card" key={step.id}>
          <div className="step-heading">
            <span className="step-number">{i + 1}</span>
            <h3>{step.title || `Шаг ${i + 1}`}</h3>
            <div className="inline">
              <button
                className="icon-button"
                aria-label={`Переместить шаг ${i + 1} вверх`}
                disabled={disabled || i === 0}
                onClick={() => moveStep(i, -1)}
              >
                ↑
              </button>
              <button
                className="icon-button"
                aria-label={`Переместить шаг ${i + 1} вниз`}
                disabled={disabled || i === c.steps.length - 1}
                onClick={() => moveStep(i, 1)}
              >
                ↓
              </button>
              <button
                className="text-button danger"
                disabled={disabled}
                onClick={() =>
                  change({ ...c, steps: c.steps.filter((_, n) => n !== i) })
                }
              >
                Удалить шаг
              </button>
            </div>
          </div>
          <Field label={`Заголовок шага ${i + 1}`} hint="Необязательно">
            <input
              disabled={disabled}
              value={step.title ?? ''}
              onChange={(e) => updateStep(i, { title: e.target.value })}
            />
          </Field>
          <Field
            label={`Описание шага ${i + 1}`}
            error={fields[`steps.${i}.description`]}
          >
            <textarea
              disabled={disabled}
              rows={3}
              value={step.description}
              placeholder="Что нужно сделать и проверить вручную?"
              onChange={(e) => updateStep(i, { description: e.target.value })}
            />
          </Field>
          {(!disabled || step.expected?.text) && (
            <Field
              label={`Ожидаемый результат шага ${i + 1}`}
              hint="Необязательно"
            >
              <textarea
                disabled={disabled}
                rows={2}
                value={step.expected?.text ?? ''}
                onChange={(e) =>
                  updateStep(i, {
                    expected: { ...step.expected, text: e.target.value },
                  })
                }
              />
            </Field>
          )}
          <ImageGallery
            caseId={c.id}
            images={step.expected?.images}
            change={(images) =>
              updateStep(i, { expected: { ...step.expected, images } })
            }
            disabled={disabled}
            onBusy={onBusy}
          />
          {step.blocks.map((block, j) => (
            <div className="call-block" key={block.id}>
              <div className="row-heading">
                <strong>
                  <span className={`protocol ${block.kind}`}>
                    {protocolLabel[block.kind]}
                  </span>{' '}
                  Вызов {j + 1}
                </strong>
                <div className="inline">
                  <button
                    className="icon-button"
                    aria-label={`Переместить вызов ${j + 1} вверх в шаге ${i + 1}`}
                    disabled={disabled || j === 0}
                    onClick={() => moveBlock(i, j, -1)}
                  >
                    ↑
                  </button>
                  <button
                    className="icon-button"
                    aria-label={`Переместить вызов ${j + 1} вниз в шаге ${i + 1}`}
                    disabled={disabled || j === step.blocks.length - 1}
                    onClick={() => moveBlock(i, j, 1)}
                  >
                    ↓
                  </button>
                  <button
                    className="text-button danger"
                    disabled={disabled}
                    onClick={() =>
                      updateStep(i, {
                        blocks: step.blocks.filter((_, n) => n !== j),
                      })
                    }
                  >
                    Удалить вызов
                  </button>
                </div>
              </div>
              <BlockEditor
                block={block}
                caseId={c.id}
                onBusy={onBusy}
                secrets={secrets}
                resources={resources}
                disabled={disabled}
                fields={fields}
                prefix={`steps.${i}.blocks.${j}`}
                onChange={(b) => updateBlock(i, j, b)}
              />
              <div className="block-actions">
                <span className="muted small">
                  {attempts[block.id] &&
                  attempts[block.id].revision !== c.revision
                    ? `Показан ответ версии ${attempts[block.id].revision}`
                    : 'Запуск выполняет только этот блок.'}
                </span>
                <button
                  className="button secondary"
                  disabled={
                    !canExecute ||
                    (!dirty &&
                      c.validationIssues?.some((issue) =>
                        issue.path.startsWith(`steps.${i}.blocks.${j}.`),
                      )) ||
                    busy ||
                    !environmentId ||
                    !resources.some(
                      (r) => r.name === block.resource && r.kind === block.kind,
                    ) ||
                    attempts[block.id]?.state === 'queued' ||
                    attempts[block.id]?.state === 'running'
                  }
                  onClick={() => launch(block.id)}
                >
                  Запустить вызов
                </button>
              </div>
              <Results
                block={block}
                attempt={attempts[block.id]}
                cancel={
                  attempts[block.id]
                    ? () => cancel(attempts[block.id].id)
                    : undefined
                }
              />
            </div>
          ))}
          <div className="add-call">
            <span className="muted small">Добавить вызов</span>
            {(['http', 'grpc', 'postgresql'] as const).map((kind) => (
              <button
                key={kind}
                className="text-button"
                disabled={disabled}
                onClick={() =>
                  updateStep(i, { blocks: [...step.blocks, newBlock(kind)] })
                }
              >
                + {protocolLabel[kind]}
              </button>
            ))}
          </div>
        </article>
      ))}
      <button
        className="add-step"
        disabled={disabled}
        onClick={() =>
          change({
            ...c,
            steps: [
              ...c.steps,
              {
                id: crypto.randomUUID(),
                title: '',
                description: '',
                blocks: [],
              },
            ],
          })
        }
      >
        + Добавить шаг
      </button>
    </section>
  )
}
