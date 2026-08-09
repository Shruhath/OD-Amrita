import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import {
  publicAttendanceRoute,
  publicDay,
  publicDayAttendance,
  publicEmptyDayAttendance,
  publicEmptyRecordCollection,
  publicError,
  publicRecord,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
  publicStudentRoll,
} from '../support/factories/public-attendance'

/**
 * Mocked browser journeys for the Story 0.10 public attendance dashboard.
 *
 * The one thing this file adds over the component tests is a REAL browser at a
 * REAL URL: the component tests run on a memory router, so they cannot prove
 * that `/attendance` is reachable as a browser location, that a deep link works,
 * or that a reload keeps the selected student. web/vercel.json rewrites
 * `/(.*)` -> `/index.html`, so the deep link is expected to work without config.
 *
 * `installPortalSession(page, null)` clears localStorage before the app boots,
 * so every journey below is a genuinely signed-out visitor rather than one who
 * merely has no role.
 */

type PublicRoutesOptions = {
  recordsStatus?: number
  searchStatus?: number
  records?: unknown
  board?: unknown
}

async function installPublicRoutes(page: import('@playwright/test').Page, options: PublicRoutesOptions = {}) {
  const seen: { authorization: (string | null)[]; searches: string[] } = { authorization: [], searches: [] }

  await installPortalSession(page, null)
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)

    seen.authorization.push(request.headers()['authorization'] ?? null)

    if (url.pathname.endsWith('/public/od-records')) {
      return json(route, options.board ?? publicEmptyDayAttendance())
    }
    if (url.pathname.endsWith('/public/students/search')) {
      seen.searches.push(url.searchParams.get('q') ?? '')
      if (options.searchStatus && options.searchStatus !== 200) {
        return json(route, publicError('RATE_LIMITED', 'Too many searches.'), options.searchStatus)
      }
      return json(route, publicSearchResults())
    }
    if (url.pathname.includes('/public/students/') && url.pathname.endsWith('/od-records')) {
      if (options.recordsStatus && options.recordsStatus !== 200) {
        return json(route, publicError('STUDENT_NOT_FOUND', 'No such student.'), options.recordsStatus)
      }
      return json(route, options.records ?? publicRecordCollection())
    }

    // Anything else is a defect: this page must call only the two public reads.
    // Failing loudly here is what catches a stray /auth/me on a public route.
    return json(route, publicError('INTERNAL', `unexpected public-page call to ${url.pathname}`), 500)
  })

  return seen
}

test.describe('public attendance dashboard', () => {
  // 0.10-E2E-001 — /attendance is a real, deep-linkable browser URL that renders
  // for an anonymous visitor at phone width (AC 9).
  test('[P0] opens at phone width for a signed-out visitor with no app install and no sign-in', async ({ page }) => {
    await installPublicRoutes(page)
    await page.setViewportSize({ width: 390, height: 844 })

    await page.goto(publicAttendanceRoute)

    await expect(page.getByRole('searchbox', { name: /roll number|name|student/i })).toBeVisible()
    await expect(page.getByRole('button', { name: /sign in|continue with microsoft/i })).toHaveCount(0)
    await expect(page.getByText(/access denied/i)).toHaveCount(0)

    // The page must not scroll sideways on a phone. A horizontal overflow on the
    // one screen every student opens is the most visible possible defect.
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })

  // 0.10-E2E-002 — the search debounces, gates at 3 characters, and selecting a
  // result loads the records and writes ?student= into the real browser URL
  // (AC 10).
  test('[P0] searching and selecting a student updates the browser URL', async ({ page }) => {
    const seen = await installPublicRoutes(page)

    await page.goto(publicAttendanceRoute)

    const searchbox = page.getByRole('searchbox', { name: /roll number|name|student/i })
    await searchbox.fill('CB')
    await expect(page.getByText(/at least 3|3 characters|keep typing/i)).toBeVisible()
    expect(seen.searches).toHaveLength(0)

    await searchbox.fill(publicStudentRoll)
    await page.getByRole('option', { name: new RegExp(publicStudentRoll.replace(/\./g, '\\.'), 'i') }).click()

    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`\\?student=${publicStudentID}$`))

    // Debounced, not per-keystroke: typing a 16-character roll number must not
    // be 16 requests against a rate-limited public endpoint.
    expect(seen.searches.length).toBeLessThanOrEqual(3)
  })

  // 0.10-E2E-003 — a reload keeps the same student, which is what makes a shared
  // link work (AC 10).
  test('[P0] reloading a ?student= link reopens the same records', async ({ page }) => {
    await installPublicRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()

    await page.reload()
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
    await expect(page.getByText(new RegExp(publicStudentRoll.replace(/\./g, '\\.'), 'i'))).toBeVisible()
  })

  // BOARD-E2E-001 — the board is on screen at phone width with no search, and
  // the page still does not scroll sideways.
  //
  // A table of roll numbers, names and slot chips is the widest thing this page
  // has ever rendered, and horizontal overflow on the one screen every student
  // opens is the most visible possible defect. The table is allowed to scroll
  // inside its own box; the PAGE is not.
  test('[P0] the day board loads without searching and does not widen the page', async ({ page }) => {
    await installPublicRoutes(page, { board: publicDayAttendance() });
    await page.setViewportSize({ width: 390, height: 844 })

    await page.goto(publicAttendanceRoute)

    const table = page.getByRole('table', { name: /on-duty students/i })
    await expect(table).toBeVisible()
    const row = table.getByRole('row', { name: /CB\.SC\.U4CSE24101/ })
    await expect(row).toContainText('Ananya Rao')
    await expect(row).toContainText('Procession')

    // Nothing was typed. The board is the page's first answer.
    await expect(page.getByRole('searchbox', { name: /roll number|name|student/i })).toHaveValue('')

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, 'the board makes the page scroll sideways on a phone').toBeLessThanOrEqual(1)
  })

  // 0.10-E2E-004 — the day shows the marked slots and NOTHING ELSE (AC 11).
  //
  // The fixture day is PRD Journey 3: slots 1, 2 and 4 present, slot 3 missing.
  // The gap is what the student came for, and it is now conveyed by the three
  // rows that exist rather than by nine rows of "No record" — so slot 3 having
  // no row at all is the assertion, and it is scoped to a row count rather than
  // to page-wide text that some other slot could satisfy.
  test('[P0] only the marked slots are rendered, and the gap is simply absent', async ({ page }) => {
    await installPublicRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()

    const slotOne = page.getByRole('listitem').filter({ hasText: /slot\s*1\b/i }).first()
    await expect(slotOne).toContainText(/Onam Celebrations/i)
    await expect(slotOne).toContainText(/Portal Coordinator/i)

    await expect(page.getByRole('listitem').filter({ hasText: /slot\s*3\b/i })).toHaveCount(0)
    await expect(page.getByText(/no record/i)).toHaveCount(0)
    // The three that WERE marked are all there — "no rows at all" would satisfy
    // the assertion above and is a different bug.
    await expect(page.getByRole('listitem').filter({ hasText: /slot\s*\d+:/i })).toHaveCount(3)

    // Epic 0 has no async path; inventing an amber state would tell a student to
    // wait for a sync that does not exist (T14).
    await expect(page.getByText(/pending|syncing|not yet synced|queued/i)).toHaveCount(0)
  })

  // 0.10-E2E-004b — the day filter reaches a day the dashboard did not open on.
  //
  // One day is on screen at a time, so the filter is the ONLY way to the rest of
  // a student's history. If it does not work, those days are not merely awkward
  // to reach — they are gone.
  test('[P0] the day filter steps back to an earlier day of records', async ({ page }) => {
    await installPublicRoutes(page, {
      records: publicRecordCollection({
        days: [
          publicDay({ od_date: '2026-07-28', records: [publicRecord(1, { od_date: '2026-07-28' })] }),
          publicDay({
            od_date: '2026-07-20',
            records: [publicRecord(5, {
              od_date: '2026-07-20',
              record_id: '66000000-0000-4000-8000-000000000720',
              event_name: 'Sports Day Relay',
            })],
          }),
        ],
      }),
    })

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)

    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
    await expect(page.getByText(/Sports Day Relay/i)).toHaveCount(0)

    await page.getByRole('button', { name: /previous day/i }).click()

    await expect(page.getByText(/Sports Day Relay/i).first()).toBeVisible()
    await expect(page.getByText(/Onam Celebrations/i)).toHaveCount(0)
  })

  // 0.10-E2E-005 — every failure names its reason and offers a retry, and none
  // of them look like "you were not marked" (AC 12).
  test('[P0] a 429 explains itself and never reads as an empty attendance record', async ({ page }) => {
    await installPublicRoutes(page, { searchStatus: 429 })

    await page.goto(publicAttendanceRoute)
    await page.getByRole('searchbox', { name: /roll number|name|student/i }).fill(publicStudentRoll)

    const alert = page.getByRole('alert')
    await expect(alert).toContainText(/too many|try again|slow down/i)
    await expect(alert).not.toContainText(/not marked|no OD records/i)
    await expect(page.getByRole('button', { name: /retry|try again/i })).toBeVisible()
  })

  // 0.10-E2E-006 — a successful empty read says "no OD records", which is a
  // DIFFERENT statement from any error (AC 12).
  test('[P0] a successful empty read is not rendered as a failure', async ({ page }) => {
    await installPublicRoutes(page, { records: publicEmptyRecordCollection() })

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)

    await expect(page.getByText(/no OD records/i)).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  // 0.10-E2E-007 — nothing is sent and nothing is stored (AC 13).
  test('[P0] sends no Authorization header and persists nothing in the browser', async ({ page }) => {
    const seen = await installPublicRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()

    expect(seen.authorization.length).toBeGreaterThan(0)
    expect(seen.authorization.every((value) => value === null || value === undefined)).toBe(true)

    // indexedDB.databases() is Chromium-only, which is the only browser this
    // project runs — so IndexedDB gets a real assertion rather than being named
    // in AC 13 and then quietly skipped.
    const stored = await page.evaluate(async () => ({
      local: Object.keys(localStorage),
      session: Object.keys(sessionStorage),
      databases: (await indexedDB.databases()).map((db) => db.name ?? ''),
    }))
    expect(stored.local).toEqual([])
    expect(stored.session).toEqual([])
    expect(stored.databases).toEqual([])
  })
})
