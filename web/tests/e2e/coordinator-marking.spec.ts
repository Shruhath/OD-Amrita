import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import { installFakeWebNfc } from './support/web-nfc'
import {
  collection,
  markCardPayload,
  markCategoryID,
  markEventID,
  markResult,
  markStatusClosed,
  markStatusOpen,
  markStudent,
  markableCategory,
  markableEvent,
} from '../support/factories/marking'

/**
 * The mocked coordinator browser journey.
 *
 * Owns the coordinator's end-to-end path with deterministic network fakes: the
 * screen loads its scoped selectors, a tap produces a green confirmation
 * carrying the student's name, and a non-coordinator is refused without issuing
 * a single request.
 *
 * The radio is faked (`installFakeWebNfc`) — CI Chromium has no NFC hardware and
 * no `NDEFReader`. Nothing in this file may be read as evidence that a real tap
 * works; that is Task 9 and it is recorded by hand.
 *
 * Routes are installed BEFORE navigation and every fulfilment carries the CORS
 * allow headers, because the dev server (127.0.0.1:4173) calls the API on a
 * different origin and the browser rejects a bare fulfilment before the app ever
 * sees it.
 */

const identity = (role: string) => ({
  user_id: '99999999-9999-4999-8999-999999999999',
  email: `${role}@amrita.edu`,
  name: `E2E ${role}`,
  role,
})

type Recorder = { paths: string[]; markBodies: unknown[] }

/** Installs every route the marking screen touches. Returns a request log. */
async function installMarkingRoutes(
  page: import('@playwright/test').Page,
  options: { role?: string; status?: ReturnType<typeof markStatusOpen>; markResponse?: { status: number; body: unknown } } = {},
): Promise<Recorder> {
  const { role = 'coordinator', status = markStatusOpen(), markResponse } = options
  const recorder: Recorder = { paths: [], markBodies: [] }

  await installPortalSession(page, role as 'coordinator')

  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    recorder.paths.push(url.pathname)

    if (request.method() === 'OPTIONS') return fulfilPreflight(route)

    if (url.pathname.endsWith('/auth/me')) return json(route, identity(role))
    if (url.pathname.endsWith('/mark/status')) return json(route, status)
    if (url.pathname.endsWith('/students/search')) return json(route, collection([markStudent(1)]))
    if (url.pathname.endsWith('/categories')) return json(route, collection([markableCategory()]))
    if (url.pathname.endsWith('/events')) return json(route, collection([markableEvent()]))

    if (url.pathname.endsWith('/mark') && request.method() === 'POST') {
      recorder.markBodies.push(request.postDataJSON())
      if (markResponse) return json(route, markResponse.body, markResponse.status)
      return json(route, markResult(markStudent(1, { display_name: 'Aparna Menon' })), 201)
    }

    return json(route, { code: 'NOT_FOUND', message: `unmocked ${url.pathname}` }, 404)
  })

  return recorder
}

test.describe('coordinator marking', () => {
  test('[P0] a coordinator taps a card and sees a green confirmation with the name', async ({ page }) => {
    await installFakeWebNfc(page)
    const recorder = await installMarkingRoutes(page)

    await page.goto('/mark')

    await expect(page.getByRole('heading', { name: /mark/i })).toBeVisible()
    await expect(page.getByRole('combobox', { name: /event/i })).toBeVisible()

    await page.getByRole('button', { name: /tap|nfc/i }).click()
    await page.evaluate((payload) => {
      ;(window as unknown as { __tapCard: (p: unknown) => void }).__tapCard(payload)
    }, markCardPayload())

    const banner = page.getByRole('status')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText('Aparna Menon')

    // AC 3: the raw tag payload goes to the server untouched.
    expect(recorder.markBodies).toHaveLength(1)
    const body = recorder.markBodies[0] as Record<string, unknown>
    expect(body.capture_method).toBe('nfc')
    expect(body.card).toEqual(markCardPayload())
    expect(body.event_id).toBe(markEventID)
    expect(body.category_id).toBe(markCategoryID)
  })

  test('[P0] a non-coordinator sees an explicit state and issues no marking request', async ({ page }) => {
    const recorder = await installMarkingRoutes(page, { role: 'student' })

    await page.goto('/mark')

    await expect(page.getByRole('heading', { name: /access|not|denied/i })).toBeVisible()
    await expect(page.getByRole('button', { name: /tap|nfc/i })).toHaveCount(0)

    // Give the poll a chance to fire before asserting it never did.
    await page.waitForTimeout(6_000)
    expect(recorder.paths.filter((p) => p.includes('/mark/status'))).toHaveLength(0)
    expect(recorder.markBodies).toHaveLength(0)
  })

  test('[P0] a coordinator landing on an unknown path is sent to /mark, not to the admin portal', async ({ page }) => {
    await installMarkingRoutes(page)

    await page.goto('/some/unknown/path')

    // C3: the catch-all currently redirects everyone to /portal/events, where a
    // coordinator can only ever see "Access denied".
    await expect(page).toHaveURL(/\/mark$/)
    await expect(page.getByRole('heading', { name: /access denied/i })).toHaveCount(0)
  })

  test('[P0] a duplicate renders red with the specific reason and a retry path', async ({ page }) => {
    await installFakeWebNfc(page)
    await installMarkingRoutes(page, {
      markResponse: { status: 409, body: { code: 'SCAN_DUPLICATE', message: 'Already marked for this slot.' } },
    })

    await page.goto('/mark')
    await page.getByRole('button', { name: /tap|nfc/i }).click()
    await page.evaluate((payload) => {
      ;(window as unknown as { __tapCard: (p: unknown) => void }).__tapCard(payload)
    }, markCardPayload())

    const banner = page.getByRole('alert')
    await expect(banner).toBeVisible()
    await expect(banner).toContainText(/already marked|duplicate/i)
    await expect(page.getByRole('status')).toHaveCount(0)
    await expect(page.getByRole('button', { name: /retry|try again/i })).toBeVisible()
  })

  test('[P0] no open slot is stated plainly with no countdown', async ({ page }) => {
    await installMarkingRoutes(page, { status: markStatusClosed() })

    await page.goto('/mark')

    await expect(page.getByText(/no slot is open/i)).toBeVisible()
    await expect(page.getByText('00:00')).toHaveCount(0)
  })

  test('[P0] both counters are visible and refresh without a page reload', async ({ page }) => {
    let poll = 0
    await installPortalSession(page, 'coordinator')
    await page.route('**/api/v1/**', async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      if (request.method() === 'OPTIONS') return fulfilPreflight(route)
      if (url.pathname.endsWith('/auth/me')) return json(route, identity('coordinator'))
      if (url.pathname.endsWith('/mark/status')) {
        poll += 1
        return json(route, markStatusOpen({ counters: { mine: poll, category_total: poll * 3 } }))
      }
      if (url.pathname.endsWith('/categories')) return json(route, collection([markableCategory()]))
      if (url.pathname.endsWith('/events')) return json(route, collection([markableEvent()]))
      return json(route, { code: 'NOT_FOUND', message: 'unmocked' }, 404)
    })

    await page.goto('/mark')

    // Scoped to the named counter rather than matching a bare "1" anywhere on the
    // page: the slot clock renders digits too, and a selector that can match
    // either would pass for the wrong reason.
    const mine = page.getByRole('region', { name: /progress/i }).locator('.mark-meter', { hasText: 'Marked by you' })
    await expect(mine).toContainText('1')
    // FR13: the counter reaches 2 with no interaction at all — the poll, not a
    // click, is what moves it.
    await expect(mine).toContainText('2', { timeout: 15_000 })
  })
})
