import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 120_000,
  expect: { timeout: 10_000 },
  use: {
    actionTimeout: 10_000,
    baseURL: 'http://127.0.0.1:18080',
    viewport: { width: 1440, height: 1000 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  reporter: 'list',
})
