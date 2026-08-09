import { selectValue } from '../support/select'
/**
 * Mocked browser journey for the multi-day event (AC 3, 11, 12, 15, 16).
 *
 * Everything here is fulfilled from route handlers, so this suite asserts the
 * SCREEN's behaviour given a server that already speaks the new shapes. What it
 * cannot see — that the server actually produces those shapes, that the day
 * filter narrows a real query, that the unique index permits day two — lives in
 * the Go suites and in tests/integration/multi-day-events.spec.ts.
 *
 * NOT skipped, not gated. These fail today because the surfaces do not exist.
 */
import { expect, test, type Page, type Route } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json, portalOrigin } from './support/portal-session'

const FESTIVAL_ID = '93000000-0000-4000-8000-000000000004'
const CATEGORY_ID = '94000000-0000-4000-8000-000000000001'
const STUDENT_ID = '95000000-0000-4000-8000-000000000101'
const FESTIVAL_START = '2026-08-10'
const FESTIVAL_END = '2026-09-08'
/** 10 Aug -> 8 Sep inclusive. A picker showing 1, 29 or 31 is a different bug each time. */
const FESTIVAL_DAY_COUNT = 30

const festivalEvent = {
  id: FESTIVAL_ID,
  name: 'Amritotsavam',
  event_date: FESTIVAL_START,
  end_date: FESTIVAL_END,
  status: 'active' as const,
  branches: [{ id: '10000000-0000-4000-8000-000000000001', code: 'ATDD-CSE', name: 'Computer Science Test Fixture' }],
  created_at: '2026-07-20T03:00:00Z',
  archived_at: null,
}

const singleDayEvent = {
  ...festivalEvent,
  id: '93000000-0000-4000-8000-000000000005',
  name: 'Innovation Day',
  event_date: '2026-08-15',
  end_date: '2026-08-15',
}

function festivalRecord(slot: number, odDate: string) {
  return {
    record_id: `67000000-0000-4000-8000-${odDate.replace(/-/g, '')}${String(slot).padStart(2, '0')}`,
    slot,
    event_id: FESTIVAL_ID,
    event_name: 'Amritotsavam',
    // The EVENT's first day, on every record, whatever day it belongs to. This
    // is the shape that fails if anything groups on event_date.
    event_date: FESTIVAL_START,
    od_date: odDate,
    category_id: CATEGORY_ID,
    category_name: 'Procession',
    marked_by_name: 'Portal Coordinator',
    marked_by_role: 'coordinator' as const,
    marked_at: `${odDate}T04:05:00Z`,
  }
}

const publicHistory = {
  student: {
    student_id: STUDENT_ID,
    roll_no: 'CB.SC.U4CSE24101',
    display_name: 'Ananya Rao',
    branch: 'CSE',
    section: 'A',
    batch: '2024',
  },
  days: [
    { od_date: '2026-08-19', records: [festivalRecord(1, '2026-08-19'), festivalRecord(2, '2026-08-19')] },
    { od_date: '2026-08-12', records: [festivalRecord(1, '2026-08-12')] },
    { od_date: FESTIVAL_START, records: [festivalRecord(4, FESTIVAL_START)] },
  ],
  generated_at: '2026-08-19T16:30:00Z',
}

// The board is not what these journeys are about; an empty day keeps it quiet
// without letting it render a failure alert that their locators would match.
const publicBoard = {
  od_date: FESTIVAL_START,
  students: [],
  categories: [],
  truncated: false,
  generated_at: '2026-08-19T16:30:00Z',
}

const publicEvents = {
  data: [
    {
      event_id: FESTIVAL_ID,
      name: 'Amritotsavam',
      event_date: FESTIVAL_START,
      end_date: FESTIVAL_END,
      categories: [{ id: CATEGORY_ID, name: 'Procession' }],
    },
  ],
}

/**
 * The identity `/api/v1/auth/me` must answer with, or the app renders the
 * sign-in screen and every assertion below fails on "Authentication could not be
 * verified" — a red that has nothing to do with this story.
 *
 * `installPortalSession` seeds the Supabase session and mocks `**\/auth/v1/**`;
 * it does NOT mock the backend's own identity read, and the existing specs each
 * route it themselves inside a `**\/api/v1/**` catch-all.
 */
function identity(role: 'admin' | 'coordinator') {
  return { subject: `e2e-${role}-subject`, email: `${role}@amrita.edu`, name: `E2E ${role}`, role }
}

async function installPublicBackend(page: Page) {
  await page.route('**/api/v1/public/**', async (route: Route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    const url = route.request().url()
    // The BOARD (…/public/od-records) and one student's records
    // (…/public/students/<id>/od-records) both end in the same segment, so the
    // board has to be matched FIRST or every board request is answered with one
    // student's history.
    if (url.includes('/public/od-records')) return json(route, publicBoard)
    if (url.includes('/od-records')) return json(route, publicHistory)
    if (url.includes('/events')) return json(route, publicEvents)
    if (url.includes('/search')) return json(route, { data: [publicHistory.student] })
    return json(route, { code: 'INTERNAL', message: 'unrouted public call' }, 500)
  })
}

test.describe('the public dashboard across a thirty-day festival', () => {
  test.beforeEach(async ({ page }) => {
    await installPublicBackend(page)
  })

  // 0.12a-E2E-001 — a festival's days are reached ONE AT A TIME through the
  // filter, each keyed on its own od_date.
  //
  // Every record in the fixture carries the same event_date and a different
  // od_date, so a screen that keyed on the event would show one day and call it
  // all three. Stacking all thirty down the page was the previous answer to
  // that; the filter is the current one, and either way no day may be lost.
  test('0.12a-E2E-001 the day filter reaches each day of a festival', async ({ page }) => {
    await page.goto(`${portalOrigin}/attendance?student=${STUDENT_ID}`)

    const filter = page.getByRole('combobox', { name: /^day$/i })
    await expect(filter).toBeVisible()

    // The PUBLIC screen has its own formatter rendering "Wednesday, 19 August
    // 2026". The PORTAL uses the short "19 Aug 2026" from
    // routes/portal/events/format.ts. Asserting the wrong one fails on wording
    // rather than on the grouping.
    for (const [day, heading] of [
      ['2026-08-19', '19 August 2026'],
      ['2026-08-12', '12 August 2026'],
      [FESTIVAL_START, '10 August 2026'],
    ]) {
      await filter.click()
      await page.getByRole('listbox').locator(`[data-value="${day}"]`).click()

      // Exactly one day on screen, and it is the one that was chosen.
      const headings = page.getByRole('heading', { name: /\d{1,2} \w+ 2026/ })
      await expect(headings).toHaveCount(1)
      await expect(headings.first()).toContainText(heading)
    }
  })

  // 0.12a-E2E-002 — the dispute day picker spans the whole range and stays a
  // bounded select.
  //
  // An unbounded <input type="date"> on this page is an ANONYMOUS write path
  // accepting any day in history (T23 / 0.11 T23).
  test('0.12a-E2E-002 the day picker offers thirty bounded options', async ({ page }) => {
    await page.goto(`${portalOrigin}/attendance?student=${STUDENT_ID}`)

    await page.getByRole('button', { name: /report a missed slot/i }).click()

    // Scoped to the FORM. The dashboard carries a day filter of its own, and the
    // two are bounded by different things on purpose — the filter by the days
    // the student has records on, this one by the event's range.
    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    const dayControl = form.getByRole('combobox', { name: /day|date/i })
    await expect(dayControl).toBeVisible()

    // The app draws its own dropdown, so the options exist once it is opened —
    // which is also when a visitor can see them. What this test protects is
    // unchanged: the day is chosen from an ENUMERATED list, never typed. A free
    // date input on this anonymous write path accepts any day in history.
    await dayControl.click()
    const options = page.getByRole('listbox').getByRole('option')
    await expect(options).toHaveCount(FESTIVAL_DAY_COUNT)
    await expect(options.first()).toHaveAttribute('data-value', FESTIVAL_START)
    await expect(options.last()).toHaveAttribute('data-value', FESTIVAL_END)
    await expect(page.locator('input[type="date"]')).toHaveCount(0)
  })

  // 0.12a-E2E-003 — the report is filed against the day the student is LOOKING
  // AT.
  //
  // 0.11's review found the form filing against the wrong day when a student had
  // records on two dates. Across thirty days that stops being an edge case. The
  // rule now attaches to the day filter rather than to a clicked row, but it is
  // the same rule.
  test('0.12a-E2E-003 reporting from the first day of the festival pre-selects that day', async ({ page }) => {
    await page.goto(`${portalOrigin}/attendance?student=${STUDENT_ID}`)

    await page.getByRole('combobox', { name: /^day$/i }).click()
    await page.getByRole('listbox').locator(`[data-value="${FESTIVAL_START}"]`).click()
    await expect(page.locator('section.public-day')).toContainText('10 August 2026')

    await page.getByRole('button', { name: /report a missed slot/i }).click()

    // The chosen day is what the control DISPLAYS; its label carries the iso
    // date alongside the long form, so this still pins the exact day.
    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await expect(form.getByRole('combobox', { name: /day|date/i })).toContainText(FESTIVAL_START)
  })
})

test.describe('the admin surfaces for a multi-day event', () => {
  const attendanceQueries: string[] = []

  test.beforeEach(async ({ page }) => {
    attendanceQueries.length = 0
    await installPortalSession(page, 'admin')
    await page.route('**/api/v1/**', (route: Route) => {
      const request = route.request()
      if (request.method() === 'OPTIONS') return fulfilPreflight(route)
      const path = new URL(request.url()).pathname

      if (path.endsWith('/auth/me')) return json(route, identity('admin'))
      if (path.endsWith('/admin/branches')) return json(route, { data: festivalEvent.branches })
      if (path.endsWith('/attendance')) {
        attendanceQueries.push(request.url())
        return json(route, { data: [], page: 1, page_size: 20, total: 0 })
      }
      if (path.endsWith(`/admin/events/${FESTIVAL_ID}`)) return json(route, festivalEvent)
      if (path.endsWith('/admin/events')) {
        return json(route, { data: [festivalEvent, singleDayEvent], page: 1, page_size: 20, total: 2 })
      }
      return json(route, { code: 'NOT_FOUND', message: `unmocked ${path}` }, 404)
    })
  })

  // 0.12a-E2E-004 — the events list renders a range for the festival and a single
  // date for the one-day event (AC 3).
  test('0.12a-E2E-004 the list distinguishes a range from a single date', async ({ page }) => {
    await page.goto(`${portalOrigin}/portal/events`)

    const festivalRow = page.getByRole('row', { name: /Amritotsavam/ })
    await expect(festivalRow).toContainText('10 Aug 2026')
    // Intl's en-GB short month for September is "Sept"; a plain 'Sep' substring
    // does not appear in "08 Sept 2026".
    await expect(festivalRow).toContainText(/08 Sept? 2026/)

    const singleRow = page.getByRole('row', { name: /Innovation Day/ })
    await expect(singleRow).toContainText('15 Aug 2026')
    // Not "15 Aug 2026 – 15 Aug 2026": formatDateRange must collapse the equal
    // case, or every ordinary event gains a visible regression.
    await expect(singleRow).not.toContainText(/15 Aug 2026.*15 Aug 2026/)
  })

  // 0.12a-E2E-005 — the attendance section offers a DAY filter and sends it.
  //
  // T22. The assertion is on the outgoing REQUEST as well as the screen: a
  // control that renders but never reaches the query looks entirely correct.
  test('0.12a-E2E-005 the attendance day filter reaches the server', async ({ page }) => {
    await page.goto(`${portalOrigin}/portal/events/${FESTIVAL_ID}`)

    const dayFilter = page.getByRole('combobox', { name: /day|date/i })
    await expect(dayFilter).toBeVisible()
    await selectValue(page, dayFilter, '2026-08-19')

    await expect
      .poll(() => attendanceQueries.some((url) => url.includes('od_date=2026-08-19')))
      .toBe(true)
  })
})

test.describe('the marking screen outside the range', () => {
  // 0.12a-E2E-006 — a day outside the range reads as "nothing scheduled", never
  // as an error (AC 11).
  //
  // The server answers with no open slot, which is the SAME state as "inside the
  // range, between slots". The screen must not acquire a second, scarier branch
  // for it: a coordinator who opens the app the day before a festival should see
  // that nothing is on, not a red failure they will try to debug at the venue.
  test('0.12a-E2E-006 a day outside the range says nothing is scheduled, not an error', async ({ page }) => {
    await installPortalSession(page, 'coordinator')
    await page.route('**/api/v1/**', (route: Route) => {
      const request = route.request()
      if (request.method() === 'OPTIONS') return fulfilPreflight(route)
      const path = new URL(request.url()).pathname

      if (path.endsWith('/auth/me')) return json(route, identity('coordinator'))
      if (path.endsWith('/mark/status')) {
        // The server's answer for a day OUTSIDE the range is the SAME answer it
        // gives between slots on a day inside it: `slot: null`. That is the
        // point — the screen must not acquire a second, scarier branch for it.
        return json(route, {
          server_time: '2026-08-09T04:00:00Z',
          slot: null,
          counters: { mine: 0, category_total: 0 },
          fallback_credit: { limit: 5, consumed: 0, remaining: 5, exhausted: false, warning_threshold: 1 },
        })
      }
      if (path.endsWith('/categories')) return json(route, { data: [{ id: CATEGORY_ID, name: 'Procession' }] })
      if (path.endsWith('/events')) {
        return json(route, { data: [{ ...festivalEvent, categories: [{ id: CATEGORY_ID, name: 'Procession' }] }] })
      }
      return json(route, { code: 'NOT_FOUND', message: `unmocked ${path}` }, 404)
    })

    await page.goto(`${portalOrigin}/mark`)

    await expect(page.getByText(/no slot is open|nothing (is )?scheduled/i)).toBeVisible()

    // No FAILURE alert. Scoped rather than `getByRole('alert')).toHaveCount(0)`:
    // desktop chromium always raises "This browser cannot use NFC. Use Chrome on
    // Android", which is a true statement about the browser and has nothing to
    // do with the range. A blanket count would fail on it forever and the
    // guard would be quietly deleted the first time someone hit it.
    await expect(
      page.getByRole('alert').filter({ hasText: /could not|failed|error|unavailable|try again/i }),
    ).toHaveCount(0)
  })
})
