import { useLayoutEffect, useRef } from 'react'

// The control stays in its own card; only the space occupied by the page chrome
// is shared. Updating a CSS property preserves focus and unsaved form state.
export function useRunStickyTop(active = false) {
  const ref = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const root = ref.current
    if (!root || root.closest('.modal')) return
    const workspace = root.closest('.workspace')
    const panels = [
      ...(workspace?.querySelectorAll<HTMLElement>('.toolbar, .document-header') ?? []),
    ]
    const commands = active ? root.querySelector<HTMLElement>('.run-toolbar') : null
    const header = panels.find(panel => panel.classList.contains('document-header'))
    const update = () => {
      const scale = root.offsetWidth ? root.getBoundingClientRect().width / root.offsetWidth : 1
      // A wrapped title must leave room for commands and the step control.
      header?.classList.remove('run-header-scrolls')
      let bottom = 0
      for (const panel of panels) {
        const style = getComputedStyle(panel)
        if (style.position === 'sticky' || style.position === 'fixed') {
          const height = panel.getBoundingClientRect().height / scale
          bottom = Math.max(bottom, height + (parseFloat(style.top) || 0))
        }
      }
      const commandHeight = commands ? commands.getBoundingClientRect().height / scale : 0
      if (header && bottom + commandHeight + 144 > innerHeight / scale) {
        header.classList.add('run-header-scrolls')
        bottom = 0
        for (const panel of panels) {
          const style = getComputedStyle(panel)
          if (style.position === 'sticky' || style.position === 'fixed')
            bottom = Math.max(bottom, panel.getBoundingClientRect().height / scale + (parseFloat(style.top) || 0))
        }
      }
      root.style.setProperty('--run-toolbar-top', `${bottom}px`)
      root.style.setProperty('--run-sticky-top', `${bottom + commandHeight + 12}px`)
    }
    const observer = typeof ResizeObserver === 'undefined'
      ? null
      : new ResizeObserver(update)
    for (const panel of panels) observer?.observe(panel)
    if (commands) observer?.observe(commands)
    window.addEventListener('resize', update)
    update()
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', update)
      header?.classList.remove('run-header-scrolls')
    }
  }, [active])
  return ref
}
