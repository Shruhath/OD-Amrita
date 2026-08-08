import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/integration',
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  // The JSON report is what lets a CI step assert that a given spec actually
  // RAN. It matters for the read-only public dashboard journey in particular:
  // that journey leaves no database trace by design, so its conservation gate
  // ("it wrote nothing") is satisfied by the seed alone and would pass just as
  // happily if the spec had been filtered out or deleted.
  reporter: process.env.CI
    ? [['github'], ['json', { outputFile: 'playwright-integration-report.json' }]]
    : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4174',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    ...devices['Desktop Chrome'],
    // Pinned for the same reason as playwright.config.ts, and AFTER the device
    // spread so it is not overwritten by it. The public dashboard renders dates
    // in the visitor's locale, so any date assertion here would otherwise depend
    // on the runner rather than on the code — and this is the config that runs
    // the real-stack multi-day journeys, which are the date-heaviest specs in
    // the repo.
    locale: 'en-GB',
  },
  webServer: {
    command: 'pnpm dev --host 127.0.0.1 --port 4174',
    url: 'http://127.0.0.1:4174/integration.html',
    reuseExistingServer: !process.env.CI,
    env: {
      ...process.env,
      VITE_API_BASE_URL: 'http://127.0.0.1:8081',
    },
  },
})
