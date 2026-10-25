import { useEffect, useState } from 'react'
type Theme = 'light' | 'dark' | 'system'
export function ThemeSwitch() {
  const [theme, setTheme] = useState<Theme>(() => {
    const value = document.documentElement.dataset.themePreference
    return value === 'light' || value === 'dark' ? value : 'system'
  })
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => {
      const dark = theme === 'dark' || (theme === 'system' && media.matches)
      document.documentElement.dataset.theme = dark ? 'dark' : 'light'
      document.documentElement.dataset.themePreference = theme
      document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
    }
    apply()
    media.addEventListener('change', apply)
    return () => media.removeEventListener('change', apply)
  }, [theme])
  return (
    <select
      className="theme-select"
      aria-label="Цветовая тема"
      value={theme}
      onChange={(e) => {
        const value = e.target.value as Theme
        setTheme(value)
        try {
          localStorage.setItem('qag.theme', value)
        } catch {}
      }}
    >
      <option value="system">Как в системе</option>
      <option value="light">Светлая тема</option>
      <option value="dark">Тёмная тема</option>
    </select>
  )
}
