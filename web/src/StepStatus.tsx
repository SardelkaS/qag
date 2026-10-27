import type { StepState } from './api'

export const manualLabels = {
  not_run: 'Не проверен',
  passed: 'Пройден',
  failed: 'Не пройден',
}
export function StepStatus({
  state,
  index,
  onChange,
  disabled = false,
}: {
  state: StepState
  index: number
  onChange?: (status: StepState['status']) => void
  disabled?: boolean
}) {
  const status =
    state.status === 'passed' || state.status === 'failed'
      ? state.status
      : 'not_run'
  return (
    <div className="step-status-control">
      <span
        className={`step-status-icon ${status}`}
        role="img"
        aria-label={manualLabels[status]}
        title={manualLabels[status]}
      >
        {status === 'passed' ? '✓' : status === 'failed' ? '✕' : '●'}
      </span>
      {onChange && (
        <select
          aria-label={`Статус шага ${index}`}
          value={status}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value as StepState['status'])}
        >
          {Object.entries(manualLabels).map(([value, label]) => (
            <option key={value} value={value}>
              {label}
            </option>
          ))}
        </select>
      )}
      {state.legacyStatus && (
        <small className="muted" title="Историческая запись сохранена">
          Ранее:{' '}
          {state.legacyStatus === 'blocked' ? 'заблокирован' : 'пропущен'}
        </small>
      )}
    </div>
  )
}
