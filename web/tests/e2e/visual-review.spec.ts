import { test, type Browser, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json, type PortalRole } from './support/portal-session'

/** Both themes, because a glass panel is only as good as its worst ground. */
const themes = ['light', 'dark'] as const
type Theme = typeof themes[number]

/**
 * Not an assertion suite — a capture harness. It renders each surface at a phone
 * and a desktop viewport and writes a PNG, so the restyle can be reviewed as
 * pixels rather than inferred from passing role queries.
 *
 * Every surface the app can show is in here, including the ones that only exist
 * after an interaction (an open dialog, a loaded roster, the search capture
 * mode). A glass panel can only be judged against what is behind it, so a
 * screen that is never captured is a screen whose contrast nobody has checked.
 *
 * Run on demand: `VISUAL_REVIEW=1 pnpm test:e2e visual-review`. Each capture
 * gets its own context because installPortalSession registers an auth route, and
 * installing it twice on one page leaves two handlers racing for the same
 * request.
 */

const branch = { id: '11111111-1111-4111-8111-111111111111', code: 'CSE', name: 'Computer Science and Engineering' }
const ece = { id: '22222222-2222-4222-8222-222222222222', code: 'ECE', name: 'Electronics and Communication' }

const eventID = 'aaaaaaaa-aaaa-4aaa-8aaa-000000000001'

const events = [
  {
    id: eventID,
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

const categories = [
  { id: 'cccccccc-cccc-4ccc-8ccc-000000000001', event_id: eventID, name: 'Cultural Programme', created_at: '2026-07-01T04:00:00Z' },
  { id: 'cccccccc-cccc-4ccc-8ccc-000000000002', event_id: eventID, name: 'Sports Meet', created_at: '2026-07-01T04:05:00Z' },
]

const teachers = [
  { user_id: 'e1', email: 'anitha.r@amrita.edu', name: 'Anitha Ramesh', branches: [branch], created_at: '2026-07-01T04:00:00Z' },
  { user_id: 'e2', email: 'venkat.s@amrita.edu', name: 'Venkat Subramanian', branches: [branch, ece], created_at: '2026-07-02T04:00:00Z' },
]

// `id` and `created_at` are both read by the list (as the React key and as the
// rendered timestamp); without created_at the row renders the literal string
// "Invalid Date", which is how the gap showed up in the first capture.
const coordinators = [
  { id: 'd1', user_id: 'c1', email: 'rahul.k@amrita.edu', name: 'Rahul Krishnan', category_id: categories[0].id, category_name: 'Cultural Programme', created_at: '2026-07-10T09:30:00Z' },
  { id: 'd2', user_id: 'c2', email: 'sneha.p@amrita.edu', name: 'Sneha Prakash', category_id: categories[1].id, category_name: 'Sports Meet', created_at: '2026-07-12T11:05:00Z' },
]

const students = [
  { student_id: 's1', roll_no: 'CB.SC.U4CSE24124', display_name: 'Aparna Menon', branch_code: 'CSE', batch: '2024', section: 'A', card_state: 'pending' },
  { student_id: 's2', roll_no: 'CB.SC.U4CSE24125', display_name: 'Karthik Nair', branch_code: 'CSE', batch: '2024', section: 'A', card_state: 'pending' },
  { student_id: 's3', roll_no: 'CB.SC.U4CSE24126', display_name: 'Divya Raghavan', branch_code: 'CSE', batch: '2024', section: 'A', card_state: 'pending' },
]

const page1 = (data: unknown[]) => ({ data, page: 1, page_size: 20, total: data.length })

/**
 * Order matters: the longer paths are tested first, because `/events` is a
 * prefix of every per-event route and would otherwise swallow all of them.
 */
async function mockApi(page: Page, role: PortalRole) {
  await page.route('**/api/v1/**', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    const url = new URL(route.request().url())
    const path = url.pathname

    if (path.endsWith('/auth/me')) {
      return json(route, { subject: `review-${role}`, email: `${role}@amrita.edu`, name: 'Shruhath Reddy', role })
    }
    if (path.endsWith('/branches')) return json(route, page1([branch, ece]))
    if (path.endsWith('/capture-credits')) {
      return json(route, {
        config: { limit: 25 },
        coordinators: coordinators.map((c, index) => ({
          user_id: c.user_id, email: c.email, name: c.name,
          limit: 25, consumed: index === 0 ? 21 : 4, remaining: index === 0 ? 4 : 21, exhausted: false,
        })),
      })
    }
    if (path.endsWith('/coordinators')) return json(route, page1(coordinators))
    if (path.endsWith('/categories')) return json(route, page1(categories))
    if (path.endsWith('/mark/status')) {
      return json(route, {
        server_time: new Date().toISOString(),
        slot: { index: 3, starts_at: new Date(Date.now() - 5 * 60_000).toISOString(), ends_at: new Date(Date.now() + 7 * 60_000).toISOString() },
        counters: { mine: 42, category_total: 168 },
        fallback_credit: { limit: 25, consumed: 21, remaining: 4, exhausted: false },
      })
    }
    if (path.endsWith('/students/search')) return json(route, page1(students.slice(0, 2)))
    if (path.endsWith('/admin/students')) return json(route, page1(students))
    if (path.endsWith('/teachers')) return json(route, page1(teachers))
    // The single-event route, which the events LIST route would otherwise catch.
    if (/\/events\/[^/]+$/.test(path)) return json(route, events[0])
    if (path.endsWith('/events')) return json(route, page1(events))
    return json(route, page1([]))
  })
}

const viewports = [
  { tag: 'mobile', width: 390, height: 844 },
  { tag: 'desktop', width: 1440, height: 900 },
]

type Surface = {
  name: string
  role: PortalRole | null
  path: string
  /** Drives the screen into a state that only exists after an interaction. */
  prepare?: (page: Page) => Promise<void>
}

const surfaces: Surface[] = [
  { name: 'signin', role: null, path: '/' },
  { name: 'denied', role: 'student', path: '/portal/events' },
  { name: 'events', role: 'admin', path: '/portal/events' },
  {
    name: 'events-dialog',
    role: 'admin',
    path: '/portal/events',
    prepare: async (page) => {
      await page.getByRole('button', { name: 'New event' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  { name: 'event-detail', role: 'admin', path: `/portal/events/${eventID}` },
  { name: 'teachers', role: 'admin', path: '/portal/teachers' },
  { name: 'students', role: 'admin', path: '/portal/students' },
  {
    name: 'populator',
    role: 'admin',
    path: '/portal/card-populator',
    prepare: async (page) => {
      // By role, not by label: "Section" also matches the nav landmark named
      // "Administration sections".
      await page.getByRole('combobox', { name: 'Branch' }).selectOption(branch.id)
      await page.getByRole('textbox', { name: 'Batch' }).fill('2024')
      await page.getByRole('textbox', { name: 'Section' }).fill('A')
      await page.getByRole('button', { name: 'Load roster' }).click()
      await page.getByRole('heading', { name: 'Section progress' }).waitFor()
    },
  },
  { name: 'mark', role: 'coordinator', path: '/mark' },
  {
    name: 'mark-search',
    role: 'coordinator',
    path: '/mark',
    prepare: async (page) => {
      await page.getByRole('button', { name: 'Search' }).click()
      await page.getByLabel('Find a student').fill('apar')
      await page.getByRole('button', { name: /Aparna/ }).waitFor()
    },
  },
]

async function capture(
  browser: Browser,
  viewport: typeof viewports[number],
  surface: Surface,
  theme: Theme,
) {
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    colorScheme: theme,
  })
  const page = await context.newPage()
  await installPortalSession(page, surface.role)
  // Registered AFTER installPortalSession, whose init script clears storage —
  // otherwise the preference is wiped before the app ever reads it. Pinned
  // explicitly rather than left to colorScheme so the capture also exercises
  // the stored-preference path the boot script takes.
  await page.addInitScript((value) => {
    localStorage.setItem('od-amrita-theme', value)
  }, theme)
  if (surface.role) await mockApi(page, surface.role)
  await page.goto(surface.path)
  await page.waitForLoadState('networkidle')
  if (surface.prepare) {
    // A prepare step that cannot run is a real finding about the surface, but it
    // must not cost the other nine captures — record it and carry on.
    try {
      await surface.prepare(page)
    } catch (cause) {
      console.error(`prepare failed for ${viewport.tag}-${theme}-${surface.name}:`, cause)
    }
  }
  // The backdrop image and the panel blurs land a frame after load; without this
  // the capture can catch an unfrosted panel.
  await page.waitForTimeout(400)
  await page.screenshot({ path: `.review/${viewport.tag}-${theme}-${surface.name}.png`, fullPage: true })
  await context.close()
}

for (const viewport of viewports) {
  test(`capture ${viewport.tag}`, async ({ browser }) => {
    // Opt-in: this file matches the e2e testMatch pattern, and CI minutes on the
    // private repo are capped. It asserts nothing, so there is nothing to gain
    // from running it on every push — set VISUAL_REVIEW=1 to capture locally.
    test.skip(!process.env.VISUAL_REVIEW, 'Set VISUAL_REVIEW=1 to capture screenshots')
    test.setTimeout(360_000)
    for (const theme of themes) {
      for (const surface of surfaces) await capture(browser, viewport, surface, theme)
    }
  })
}
