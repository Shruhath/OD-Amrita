import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests/api',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.PORTAL_API_URL ?? 'http://127.0.0.1:8081',
    trace: 'retain-on-failure',
  },
})
