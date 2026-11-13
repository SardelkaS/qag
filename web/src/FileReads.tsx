import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'

export const FileReadContext = createContext<() => () => void>(() => () => {})

export function useFileReadScope() {
  const [pending, setPending] = useState(0)
  const begin = useCallback(() => {
    setPending(n => n + 1)
    let released = false
    return () => { if (!released) { released = true; setPending(n => n - 1) } }
  }, [])
  return { pending, begin }
}

// Use the latest draft callback, and discard results after removal or a newer
// selection. A completed read must not restore stale fields in its parent.
export function useFileRead<T>(apply: (value: T) => void, failed: (error: string) => void, beginOverride?: () => () => void) {
  const contextBegin = useContext(FileReadContext)
  const begin = beginOverride ?? contextBegin
  const latest = useRef({ apply, failed })
  latest.current = { apply, failed }
  const state = useRef({ mounted: true, sequence: 0 })
  useEffect(() => {
    state.current.mounted = true
    return () => { state.current.mounted = false; state.current.sequence++ }
  }, [])
  return async (read: () => Promise<T>) => {
    const sequence = ++state.current.sequence
    const release = begin()
    try {
      const value = await read()
      if (state.current.mounted && state.current.sequence === sequence) latest.current.apply(value)
    } catch (error) {
      if (state.current.mounted && state.current.sequence === sequence)
        latest.current.failed(error instanceof Error ? error.message : 'Не удалось прочитать файл')
    } finally { release() }
  }
}
