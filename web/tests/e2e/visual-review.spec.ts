import { test, type Browser, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

/**
 * Not an assertion suite — a capture harness. It renders each surface at a phone
 * and a desktop viewport and writes a PNG, so the restyle can be reviewed as
 * pixels rather than inferred from passing role queries.
 *
 * Run on demand: `pnpm test:e2e visual-review`. Each capture gets its own
 * context because installPortalSession registers an auth route, and installing
 * it twice on one page leaves two handlers racing for the same request.
 */

const branch = { id: '11111111-1111-4111-8111-111111111111', code: 'CSE', name: 'Computer Science and Engineering' }
const ece = { id: '22222222-2222-4222-8222-222222222222', code: 'ECE', name: 'Electronics and Communication' }

const events = [
  {
    id: 'aaaaaaaa-aaaa-4aaa-8aaa-000000000001',
    name: 'Amritotsavam 2026',
    event_date: '2026-07-28',
    status: 'active',
    branches: [branch],
    created_at: '2026-07-01T04:00:00Z',
  },
  {
    id: 'aaaaaaaa-aaaa-4aaa-8aaa-000000000002',
    name: 'Convocation Rehearsal',
    event_date: '2026-06-14',
    status: 'archived',
    branches: [branch, ece],
    created_at: '2026-06-01T04:00:00Z',
  },
]

const teachers = [
  { user_id: 'e1', email: 'anitha.r@amrita.edu', name: 'Anitha Ramesh', branches: [branch], created_at: '2026-07-01T04:00:00Z' },
  { user_id: 'e2', email: 'venkat.s@amrita.edu', name: 'Venkat Subramanian', branches: [branch, ece], created_at: '2026-07-02T04:00:00Z' },
]

async function mockApi(page: Page, role: 'admin' | 'student') {
  await page.route('**/api/v1/**', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    const url = route.request().url()
    if (url.includes('/auth/me')) {
      return json(route, { subject: `review-${role}`, email: `${role}@amrita.edu`, name: 'Shruhath Reddy', role })
    }
    if (url.includes('/branches')) return json(route, { data: [branch, ece], page: 1, page_size: 20, total: 2 })
    if (url.includes('/events')) return json(route, { data: events, page: 1, page_size: 20, total: 2 })
    if (url.includes('/teachers')) return json(route, { data: teachers, page: 1, page_size: 20, total: 2 })
    return json(route, { data: [], page: 1, page_size: 20, total: 0 })
  })
}

const viewports = [
  { tag: 'mobile', width: 390, height: 844 },
  { tag: 'desktop', width: 1440, height: 900 },
]

const surfaces: Array<{ name: string; role: 'admin' | 'student' | null; path: string }> = [
  { name: 'signin', role: null, path: '/' },
  { name: 'denied', role: 'student', path: '/portal/events' },
  { name: 'events', role: 'admin', path: '/portal/events' },
  { name: 'teachers', role: 'admin', path: '/portal/teachers' },
  { name: 'students', role: 'admin', path: '/portal/students' },
  { name: 'populator', role: 'admin', path: '/portal/card-populator' },
]

async function capture(browser: Browser, viewport: typeof viewports[number], surface: typeof surfaces[number]) {
  const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height } })
  const page = await context.newPage()
  await installPortalSession(page, surface.role)
  if (surface.role) await mockApi(page, surface.role)
  await page.goto(surface.path)
  await page.waitForLoadState('networkidle')
  await page.screenshot({ path: `.review/${viewport.tag}-${surface.name}.png`, fullPage: true })
  await context.close()
}

for (const viewport of viewports) {
  test(`capture ${viewport.tag}`, async ({ browser }) => {
    // Opt-in: this file matches the e2e testMatch pattern, and CI minutes on the
    // private repo are capped. It asserts nothing, so there is nothing to gain
    // from running it on every push — set VISUAL_REVIEW=1 to capture locally.
    test.skip(!process.env.VISUAL_REVIEW, 'Set VISUAL_REVIEW=1 to capture screenshots')
    for (const surface of surfaces) await capture(browser, viewport, surface)
  })
}
