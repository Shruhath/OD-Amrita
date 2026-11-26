import { expect, test, type Page } from '@playwright/test'

import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import { installFakeWebNfc } from './support/web-nfc'

type FallbackCredit = {
  limit: number
  consumed: number
  remaining: number
  exhausted: boolean
}

const eventID = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc'
const categoryID = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'
const eventName = 'Onam Celebrations'

async function installCreditRoutes(page: Page, credit: FallbackCredit) {
  const markBodies: unknown[] = []
  await installPortalSession(page, 'coordinator')
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)
    if (url.pathname.endsWith('/auth/me')) {
      return json(route, { user_id: '99999999-9999-4999-8999-999999999999', email: 'coordinator@amrita.edu', role: 'coordinator' })
    }
    if (url.pathname.endsWith('/events')) {
      return json(route, {
        data: [{ id: eventID, name: eventName, event_date: '2026-08-03', status: 'active', branches: [], created_at: '2026-08-03T04:00:00Z', archived_at: null }],
        page: 1,
        page_size: 20,
        total: 1,
      })
    }
    if (url.pathname.endsWith('/categories')) {
      return json(route, { data: [{ id: categoryID, event_id: eventID, name: 'Cultural', created_at: '2026-08-03T04:00:00Z' }] })
    }
    if (url.pathname.endsWith('/mark/status')) {
      return json(route, {
        server_time: '2026-08-03T04:00:00Z',
        slot: { slot: 1, started_at: '2026-08-03T03:30:00Z', ends_at: '2026-08-03T04:20:00Z' },
        counters: { mine: 0, category_total: 0 },
        fallback_credit: credit,
      })
    }
    if (url.pathname.endsWith('/mark') && request.method() === 'POST') {
      markBodies.push(request.postDataJSON())
      return json(route, {
        record_id: '66666666-6666-4666-8666-666666666666',
        student: { student_id: '95000000-0000-4000-8000-000000000001', roll_no: 'CB.SC.U4CSE99001', display_name: 'Aparna Menon', branch: 'CSE', section: 'A', batch: '2024' },
        event_id: eventID,
        category_id: categoryID,
        slot: 1,
        capture_method: 'nfc',
        server_received_at: '2026-08-03T04:00:05Z',
      }, 201)
    }
    return json(route, { code: 'NOT_FOUND', message: `unmocked ${url.pathname}` }, 404)
  })
  return markBodies
}

test.describe('fallback capture credits', () => {
  test('[P0] exhausted fallback credits disable camera/search and leave NFC usable', async ({ page }) => {
    await installFakeWebNfc(page)
    const markBodies = await installCreditRoutes(page, { limit: 5, consumed: 5, remaining: 0, exhausted: true })

    await page.goto('/mark')

    await expect(page.getByRole('button', { name: /camera|scan/i })).toBeDisabled()
    await expect(page.getByRole('button', { name: /search/i })).toBeDisabled()
    await expect(page.getByRole('button', { name: /tap|nfc/i })).toBeEnabled()
    await expect(page.getByText(/use nfc/i)).toBeVisible()
    expect(markBodies).toHaveLength(0)
  })

  test('[P1] low remaining fallback credit is visible before selecting camera/search', async ({ page }) => {
    await installCreditRoutes(page, { limit: 5, consumed: 4, remaining: 1, exhausted: false })

    await page.goto('/mark')

    await expect(page.getByText(/1 fallback credit/i)).toBeVisible()
    await expect(page.getByText(/almost exhausted/i)).toBeVisible()
  })
})
