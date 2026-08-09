import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import {
  publicAttendanceRoute,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
} from '../support/factories/public-attendance'
import {
  disputeConfirmationCopy,
  disputeEmail,
  disputeError,
  disputeODDate,
  disputeReference,
  identityUnverified,
  publicDisputeReceipt,
  publicEventCollection,
} from '../support/factories/student-disputes'

/**
 * Mocked browser journeys for Story 0.11's public dispute form.
 *
 * What this file adds over the component tests is a REAL browser at a REAL URL,
 * with real layout: whether the form is usable at 390px, whether the page scrolls
 * sideways once a form is added to it, and whether IndexedDB stays empty. None of
 * those exist under jsdom.
 *
 * `installPortalSession(page, null)` clears localStorage before the app boots, so
 * every journey below is a genuinely signed-out visitor rather than one who
 * merely has no role.
 *
 * The catch-all at the bottom of the router handler is deliberate: this page must
 * call the public reads and the public write, and NOTHING else. A stray
 * /auth/me — the thing that appears the moment somebody reuses the authorized
 * client — fails loudly here instead of passing silently.
 */

type DisputeRouteOptions = {
  submitStatus?: number
  submitBody?: unknown
  records?: unknown
}

async function installDisputeRoutes(page: import('@playwright/test').Page, options: DisputeRouteOptions = {}) {
  const seen: { authorization: (string | null)[]; submissions: unknown[] } = { authorization: [], submissions: [] }

  await installPortalSession(page, null)
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)

    seen.authorization.push(request.headers()['authorization'] ?? null)

    if (url.pathname.endsWith('/public/students/search')) return json(route, publicSearchResults())
    if (url.pathname.includes('/public/students/') && url.pathname.endsWith('/od-records')) {
      return json(route, options.records ?? publicRecordCollection())
    }
    if (url.pathname.endsWith('/public/events')) return json(route, publicEventCollection())
    if (url.pathname.endsWith('/public/disputes') && request.method() === 'POST') {
      seen.submissions.push(request.postDataJSON())
      const status = options.submitStatus ?? 201
      if (status !== 201) {
        return json(route, options.submitBody ?? disputeError('INTERNAL', 'Server problem.'), status)
      }
      return json(route, publicDisputeReceipt(), 201)
    }

    return json(route, disputeError('INTERNAL', `unexpected public-page call to ${url.pathname}`), 500)
  })

  return seen
}

/** The slot-3 row — the gap in the 0.10 fixture day, PRD Journey 3 verbatim. */
function slotThreeRow(page: import('@playwright/test').Page) {
  return page.getByRole('listitem').filter({ hasText: /slot\s*3\b/i })
}

test.describe('public dispute form', () => {
  // 0.11-E2E-001 — the affordance is reachable from a "No record" slot, and the
  // form is usable at phone width without the page scrolling sideways (AC 1,
  // AC 15).
  //
  // A horizontal overflow on the one screen every student opens is the most
  // visible possible defect, and adding a form with a wide select is exactly how
  // it appears.
  test('[P0] opens from the no-record slot at phone width with no horizontal overflow', async ({ page }) => {
    await installDisputeRoutes(page)
    await page.setViewportSize({ width: 390, height: 844 })

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()

    const row = slotThreeRow(page)
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(/no record/i)

    await row.getByRole('button', { name: /report|missed|dispute/i }).click()

    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await expect(form).toBeVisible()
    await expect(form.getByRole('textbox', { name: /email/i })).toBeVisible()

    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, 'the page scrolls sideways once the dispute form is open').toBeLessThanOrEqual(1)
  })

  // 0.11-E2E-002 — the whole submission journey, ending in NFR50's exact copy and
  // the reference, within the same interaction (AC 2, AC 15).
  test('[P0] submitting shows the fixed confirmation copy and the reference', async ({ page }) => {
    const seen = await installDisputeRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
    await slotThreeRow(page).getByRole('button', { name: /report|missed|dispute/i }).click()

    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await form.getByRole('textbox', { name: /email/i }).fill(disputeEmail)
    await form.getByRole('button', { name: /submit|send|report/i }).click()

    // Scoped to the receipt panel. A bare getByRole('status') matches 0.10's
    // "As of" live region too — that collision was previously resolved by
    // DELETING 0.10's role, which silently removed an announcement it shipped.
    const status = page.getByRole('region', { name: /dispute receipt/i }).getByRole('status')
    await expect(status).toContainText(disputeConfirmationCopy)
    await expect(status).toContainText(new RegExp(disputeReference, 'i'))

    expect(seen.submissions).toHaveLength(1)
    expect(seen.submissions[0]).toMatchObject({
      submitted_email: disputeEmail,
      student_id: publicStudentID,
      slot: 3,
      // The day the OD belongs to — carried explicitly, never inferred from the
      // event (amended 2026-08-06; 0.12a makes an event span thirty days).
      od_date: disputeODDate,
    })
  })

  // 0.11-E2E-003 — every refusal is named, and none of them is a blank page, a
  // silent success or anything that implies the slot was credited (AC 16).
  //
  // Written as a data-driven loop rather than with a parameterised helper:
  // Playwright has no `test.each`, and a loop over a literal array keeps the four
  // cases side by side so a fifth is one line.
  for (const scenario of [
    { name: 'identity unverified', status: 400, body: identityUnverified, pattern: /could not verify|university address/i },
    { name: 'already reported', status: 409, body: disputeError('DISPUTE_ALREADY_OPEN', `Already reported. Reference ${disputeReference}.`), pattern: /already reported|already open|already submitted/i },
    { name: 'rate limited', status: 429, body: disputeError('RATE_LIMITED', 'Too many requests.'), pattern: /too many|slow down/i },
    { name: 'server problem', status: 500, body: disputeError('INTERNAL', 'Server problem.'), pattern: /our problem|server|try again/i },
  ]) {
    test(`[P0] ${scenario.name} is explained and never reads as a credited slot`, async ({ page }) => {
      await installDisputeRoutes(page, { submitStatus: scenario.status, submitBody: scenario.body })

      await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
      await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
      await slotThreeRow(page).getByRole('button', { name: /report|missed|dispute/i }).click()

      const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
      await form.getByRole('textbox', { name: /email/i }).fill(disputeEmail)
      await form.getByRole('button', { name: /submit|send|report/i }).click()

      const alert = page.getByRole('alert')
      await expect(alert).toContainText(scenario.pattern)
      await expect(alert).not.toContainText(/marked|credited|present/i)
      await expect(page.getByText(disputeConfirmationCopy)).toHaveCount(0)
    })
  }

  // 0.11-E2E-004 — nothing is sent and nothing is stored (AC 15, AC 17, NFR18).
  //
  // indexedDB.databases() is Chromium-only, which is the only browser this
  // project runs, so IndexedDB gets a real assertion rather than being named in
  // an AC and quietly skipped.
  test('[P0] sends no Authorization header, persists nothing and keeps the email out of the URL', async ({ page }) => {
    const seen = await installDisputeRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/Onam Celebrations/i).first()).toBeVisible()
    await slotThreeRow(page).getByRole('button', { name: /report|missed|dispute/i }).click()

    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await form.getByRole('textbox', { name: /email/i }).fill(disputeEmail)
    await form.getByRole('button', { name: /submit|send|report/i }).click()
    await expect(
      page.getByRole('region', { name: /dispute receipt/i }).getByRole('status'),
    ).toContainText(disputeConfirmationCopy)

    expect(seen.authorization.length).toBeGreaterThan(0)
    expect(seen.authorization.every((value) => value === null || value === undefined)).toBe(true)

    const stored = await page.evaluate(async () => ({
      local: Object.keys(localStorage),
      session: Object.keys(sessionStorage),
      databases: (await indexedDB.databases()).map((db) => db.name ?? ''),
      search: window.location.search,
    }))
    expect(stored.local).toEqual([])
    expect(stored.session).toEqual([])
    expect(stored.databases).toEqual([])
    // NFR18: `?student=<uuid>` stays the ONLY query key. An email in the URL ends
    // up in history, proxy logs and every screenshot the student shares.
    expect(stored.search).not.toContain('@')
    expect([...new URLSearchParams(stored.search).keys()]).toEqual(['student'])
  })

  // 0.11-E2E-005 — a never-marked student can still file (AC 7, T7).
  //
  // They have no slot row to click and no event to derive, so both the affordance
  // and the public event read have to work for a completely empty record.
  test('[P0] a student with no records at all can still reach the form and pick an event', async ({ page }) => {
    await installDisputeRoutes(page, { records: publicRecordCollection({ days: [] }) })

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await expect(page.getByText(/no OD records/i)).toBeVisible()

    await page.getByRole('button', { name: /report|missed|dispute/i }).click()
    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await expect(form).toBeVisible()

    const events = form.getByRole('combobox', { name: /event/i })
    await expect(events).toBeVisible()
    await events.click()
    await expect(
      page.getByRole('listbox').getByRole('option', { name: /Sports Day Relay/i }),
      'the archived event is missing from the picker; a student notices a missing slot AFTER the event, which ' +
        'is usually after it has been archived (T8)',
    ).toHaveCount(1)
  })

  // 0.11-E2E-006 — the two 0.10 reads still work with the form on the page
  // (AC 17).
  //
  // This story adds a POST to a page that was read-only. The regression to catch
  // is the dashboard breaking as a side effect: a shared client that starts
  // sending a token, or a query key collision that empties the slot grid.
  test('[P0] the 0.10 dashboard still renders its slots with the dispute form present', async ({ page }) => {
    await installDisputeRoutes(page)

    await page.goto(`${publicAttendanceRoute}?student=${publicStudentID}`)

    const slotOne = page.getByRole('listitem').filter({ hasText: /slot\s*1\b/i }).first()
    await expect(slotOne).toContainText(/Onam Celebrations/i)
    await expect(slotOne).toContainText(/Portal Coordinator/i)
    await expect(slotThreeRow(page)).toContainText(/no record/i)
    await expect(page.getByText(/pending|syncing|queued/i)).toHaveCount(0)
    await expect(page.getByRole('button', { name: /sign in|continue with microsoft/i })).toHaveCount(0)
  })
})
