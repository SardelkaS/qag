import { useState } from 'react'

// Check DER framing before replacing a draft. Certificate/key semantics and
// matching the pair are validated by the server when saving the resource.
function derTags(bytes: Uint8Array, start = 0, end = bytes.length, depth = 0): number[] {
  if (depth > 32) throw new Error()
  const tags: number[] = []
  while (start < end) {
    const tag = bytes[start++]
    let length = bytes[start++]
    if (length & 0x80) {
      const count = length & 0x7f
      if (!count || count > 4 || start + count > end || bytes[start] === 0) throw new Error()
      length = 0
      for (let i = 0; i < count; i++) length = length * 256 + bytes[start++]
      if (length < 128) throw new Error()
    }
    if (!Number.isFinite(length) || start + length > end) throw new Error()
    if (tag & 0x20) derTags(bytes, start, start + length, depth + 1)
    tags.push(tag)
    start += length
  }
  return tags
}

export async function readPEM(file: File, kind: 'certificate' | 'key'): Promise<string> {
  if (!file.size || file.size > 1024 * 1024)
    throw new Error('Нужен непустой PEM-файл размером до 1 MiB')
  let text: string
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
  } catch {
    throw new Error('PEM-файл должен быть текстом UTF-8')
  }
  const labels = kind === 'certificate' ? 'CERTIFICATE' : '(?:RSA |EC )?PRIVATE KEY'
  const pattern = new RegExp(`-----BEGIN (${labels})-----\\s+([A-Za-z0-9+/=\\s]+?)-----END \\1-----`, 'g')
  const blocks = [...text.matchAll(pattern)]
  if (!blocks.length || kind === 'key' && blocks.length !== 1 || text.replace(pattern, '').trim())
    throw new Error('Нужен PEM без шифрования: сертификат или приватный ключ соответствующего типа')
  try {
    for (const block of blocks) {
      const bytes = atob(block[2].replace(/\s/g, ''))
      const data = Uint8Array.from(bytes, c => c.charCodeAt(0))
      const tags = derTags(data)
      if (bytes.length < 16 || tags.length !== 1 || tags[0] !== 0x30) throw new Error()
    }
  } catch {
    throw new Error('Повреждённый PEM-файл')
  }
  return text
}

export function PemUpload({ label, kind, change }: {
  label: string
  kind: 'certificate' | 'key'
  change: (value: string) => void
}) {
  const [error, setError] = useState('')
  return <div className="pem-upload">
    <label className="file-button">{label}
      <input type="file" accept=".pem,.crt,.cer,.key" aria-label={label} onChange={async (event) => {
        const file = event.currentTarget.files?.[0]
        event.currentTarget.value = ''
        if (!file) return
        setError('')
        try { change(await readPEM(file, kind)) }
        catch (e) { setError(e instanceof Error ? e.message : 'Не удалось прочитать PEM') }
      }} />
    </label>
    {error && <p className="field-error" role="alert">{error}</p>}
  </div>
}
