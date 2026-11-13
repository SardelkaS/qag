import { createContext, useContext, useEffect, useRef, useState, type RefObject } from 'react'
import { Notice } from './ui'
import './diagnostics.css'

export const FormFeedbackContext = createContext<((message: string) => void) | undefined>(undefined)
export const useFormFeedback = () => useContext(FormFeedbackContext)

export function FormErrorBanner({ error, fields = {}, form, close }: { error: string; fields?: Record<string, string>; form?: RefObject<HTMLFormElement | null>; close: () => void }) {
  const [top, setTop] = useState(88)
  const element = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const toolbar = document.querySelector('.toolbar')
    if (!toolbar) return
    const update = () => setTop((toolbar as HTMLElement).offsetHeight + 8)
    update()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(update); observer.observe(toolbar)
    return () => observer.disconnect()
  }, [])
  if (!error) return null
  const jump = () => {
    const root = form?.current
    if (!root) return
    const key = Object.keys(fields)[0] ?? ''
    const section = key.includes('header') ? 'headers' : key.includes('tls') || key.includes('certificate') ? 'tls' : key.includes('proto') ? 'proto' : key.includes('variable') || key.includes('source') ? 'variables' : key.includes('secret') ? 'secrets' : ''
    const target = root.querySelector<HTMLElement>('[aria-invalid="true"]') ?? (section ? root.querySelector<HTMLElement>(`[data-error-section="${section}"] input, [data-error-section="${section}"] select, [data-error-section="${section}"] textarea`) : null)
    target?.scrollIntoView({ block: 'center', behavior: 'smooth' }); target?.focus({ preventScroll: true })
  }
  return <div ref={element} className="form-error-banner" style={{ top }}>
    <Notice onClose={close}><span>{error}</span>{Object.keys(fields).length > 0 && <button type="button" className="text-button" onClick={jump}>Перейти к полю</button>}</Notice>
  </div>
}
