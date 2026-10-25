import { useEffect, useState } from 'react'
import type { ExpectedImage, ImageRef } from './api'
import { message, request } from './api'
import { Modal, Notice } from './ui'

export function ImageGallery({
  caseId,
  images = [],
  change,
  disabled = false,
  onBusy,
  label = 'Изображения ожидаемого результата',
}: {
  caseId: string
  images?: ImageRef[]
  change?: (images: ImageRef[]) => void
  disabled?: boolean
  onBusy?: (busy: boolean) => void
  label?: string
}) {
  const [error, setError] = useState(''),
    [busy, setBusy] = useState(false),
    [open, setOpen] = useState<ImageRef | null>(null),
    [failed, setFailed] = useState<Record<string, boolean>>({})
  useEffect(() => {
    if (!open) return
    const close = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(null)
    }
    window.addEventListener('keydown', close)
    return () => window.removeEventListener('keydown', close)
  }, [open])
  const url = (id: string) => `/api/v1/test-cases/${caseId}/images/${id}`
  const upload = async (files: FileList | null) => {
    if (!files || !change) return
    setError('')
    setBusy(true)
    onBusy?.(true)
    try {
      if (images.length + files.length > 10)
        throw new Error('Не больше 10 изображений в одном ожидании')
      let next = [...images]
      for (const file of Array.from(files)) {
        if (file.size > 10 * 1024 * 1024)
          throw new Error('Изображение должно быть не больше 10 MiB')
        const data = await new Promise<string>((resolve, reject) => {
          const reader = new FileReader()
          reader.onload = () => resolve(String(reader.result).split(',')[1])
          reader.onerror = () => reject(new Error('Не удалось прочитать файл'))
          reader.readAsDataURL(file)
        })
        const image = await request<ExpectedImage>(
          `/test-cases/${caseId}/images`,
          'POST',
          { filename: file.name, dataBase64: data },
        )
        next = [...next, { imageId: image.id, caption: '' }]
        change(next)
      }
    } catch (e) {
      setError(message(e))
    } finally {
      setBusy(false)
      onBusy?.(false)
    }
  }
  const move = (i: number, delta: number) => {
    const next = [...images]
    ;[next[i], next[i + delta]] = [next[i + delta], next[i]]
    change?.(next)
  }
  if ((!change || disabled) && !images.length && !busy) return null
  return (
    <section className="image-gallery" aria-label={label}>
      <h5>{label}</h5>
      {error && <Notice onClose={() => setError('')}>{error}</Notice>}
      {busy && (
        <p className="muted small" role="status">
          Загрузка изображения…
        </p>
      )}
      <div className="image-grid">
        {images.map((ref, i) => (
          <figure className="image-reference" key={`${ref.imageId}:${i}`}>
            <button
              type="button"
              className="image-preview"
              aria-label={`Открыть изображение ${i + 1}`}
              onClick={() => setOpen(ref)}
            >
              {failed[ref.imageId] ? (
                <span role="alert">Не удалось загрузить изображение</span>
              ) : (
                <img
                  src={url(ref.imageId)}
                  alt={ref.caption || `Эталон ${i + 1}`}
                  loading="lazy"
                  onError={() =>
                    setFailed((v) => ({ ...v, [ref.imageId]: true }))
                  }
                />
              )}
            </button>
            {change && !disabled ? (
              <>
                <input
                  aria-label={`Подпись изображения ${i + 1}`}
                  value={ref.caption ?? ''}
                  placeholder="Подпись — необязательно"
                  disabled={busy}
                  maxLength={1000}
                  onChange={(e) =>
                    change(
                      images.map((v, n) =>
                        n === i ? { ...v, caption: e.target.value } : v,
                      ),
                    )
                  }
                />
                <div className="inline">
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Изображение ${i + 1} выше`}
                    disabled={busy || i === 0}
                    onClick={() => move(i, -1)}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`Изображение ${i + 1} ниже`}
                    disabled={busy || i === images.length - 1}
                    onClick={() => move(i, 1)}
                  >
                    ↓
                  </button>
                  <button
                    type="button"
                    className="text-button danger"
                    disabled={busy}
                    onClick={() => change(images.filter((_, n) => i !== n))}
                  >
                    Удалить изображение
                  </button>
                </div>
              </>
            ) : (
              ref.caption && <figcaption>{ref.caption}</figcaption>
            )}
          </figure>
        ))}
      </div>
      {change && !disabled && (
        <label className="file-button">
          {busy ? 'Загрузка…' : '+ Изображения'}
          <input
            type="file"
            aria-label="Добавить изображения"
            multiple
            accept="image/png,image/jpeg,image/webp"
            disabled={busy || images.length >= 10}
            onChange={(e) => {
              void upload(e.target.files)
              e.target.value = ''
            }}
          />
        </label>
      )}
      {change && !disabled && (
        <p className="muted small">
          PNG, JPEG, WebP · до 10 MiB и 20 млн пикселей · ручной эталон
        </p>
      )}
      {open && (
        <Modal
          title="Изображение ожидаемого результата"
          onClose={() => setOpen(null)}
        >
          <img
            className="image-expanded"
            src={url(open.imageId)}
            alt={open.caption || 'Эталон'}
          />
          {open.caption && <p>{open.caption}</p>}
        </Modal>
      )}
    </section>
  )
}
