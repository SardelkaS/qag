import { useLayoutEffect, useRef } from 'react'

// The control stays in its own card; only the space occupied by the page chrome
// is shared. Updating a CSS property preserves focus and unsaved form state.
export function useRunStickyTop() {
  const ref = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const root = ref.current
    if (!root || root.closest('.modal')) return
    const workspace = root.closest('.workspace')
    const panels = [
      ...(workspace?.querySelectorAll<HTMLElement>('.toolbar, .document-header') ?? []),
    ]
    const update = () => {
      let bottom = 0
      for (const panel of panels) {
        const style = getComputedStyle(panel)
        if (style.position === 'sticky' || style.position === 'fixed') {
          const height = panel.getBoundingClientRect().height
          bottom = Math.max(bottom, height + (parseFloat(style.top) || 0))
        }
      }
      root.style.setProperty('--run-sticky-top', `${bottom + 12}px`)
    }
    const observer = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(update)
    for (const panel of panels) observer?.observe(panel)
    window.addEventListener('resize', update)
    update()
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', update)
    }
  }, [])
  return ref
}
