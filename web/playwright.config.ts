import { defineConfig, devices } from '@playwright/test'

const portalBaseURL = process.env.PORTAL_BASE_URL ?? 'http://127.0.0.1:4173'

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: portalBaseURL,
    // Pinned so date assertions are deterministic. The public dashboard renders
    // dates in the VISITOR's locale (`toLocaleDateString(undefined, …)`), which
    // is correct for the product and machine-dependent for a test: Chromium
    // defaults to en-US and prints "Wednesday, August 19, 2026", while jsdom in
    // the unit suite prints "19 August 2026". Without this, whether a spec
    // passes depends on the runner rather than on the code. en-GB is also the
    // day-first form these users actually see.
    locale: 'en-GB',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      testMatch: /e2e\/.*\.spec\.ts/,
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'pnpm dev --host 127.0.0.1 --port 4173',
    url: portalBaseURL,
    reuseExistingServer: !process.env.CI,
    env: {
      ...process.env,
      VITE_SUPABASE_URL: process.env.VITE_SUPABASE_URL ?? 'http://127.0.0.1:54321',
      VITE_SUPABASE_ANON_KEY: process.env.VITE_SUPABASE_ANON_KEY ?? 'test-anon-key',
      VITE_API_BASE_URL: process.env.VITE_API_BASE_URL ?? 'http://127.0.0.1:8080',
    },
  },
})
