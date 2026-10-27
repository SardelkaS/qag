import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const icons = ['/favicon.ico', '/qag-32.png', '/qag-256.png']
let publicDir = ''

export default defineConfig({
  plugins: [react(), {
    name: 'qag-icon-versions',
    configResolved(config) { publicDir = config.publicDir },
    transformIndexHtml(html) {
      for (const icon of icons) {
        const bytes = readFileSync(join(publicDir, icon.slice(1)))
        const version = createHash('sha256').update(bytes).digest('hex').slice(0, 12)
        html = html.replaceAll(`href="${icon}"`, `href="${icon}?v=${version}"`)
      }
      return html
    },
  }],
  server: {
    strictPort: true,
    port: 5173,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
  build: { outDir: 'dist', emptyOutDir: true },
})
