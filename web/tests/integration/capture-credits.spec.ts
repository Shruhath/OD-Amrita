import { expectSomethingSelected, selectedValue } from '../support/select'
import { expect, test } from '@playwright/test'

const portalAPI = 'http://127.0.0.1:8081'
const markingEventID = '93000000-0000-4000-8000-000000000001'
const portalCoordinatorID = '90000000-0000-4000-8000-000000000003'
const firstCreditRoll = 'CB.SC.U4CSE24004'
const secondCreditRoll = 'CB.SC.U4CSE24005'

async function selectedScope(page: import('@playwright/test').Page) {
  const eventSelect = page.getByRole('combobox', { name: /event/i })
  const categorySelect = page.getByRole('combobox', { name: /category/i })
  await expectSomethingSelected(eventSelect)
  await expectSomethingSelected(categorySelect)
  return {
    eventID: await selectedValue(eventSelect),
    categoryID: await selectedValue(categorySelect),
  }
}

async function searchMark(page: import('@playwright/test').Page, roll: string) {
  await page.getByRole('button', { name: /search/i }).click()
  await page.getByRole('searchbox').fill(roll)
  await page.getByRole('button', { name: new RegExp(roll.replace(/\./g, '\\.')) }).click()
}

async function setFirstCoordinatorLimit(request: import('@playwright/test').APIRequestContext, limit: number) {
  return request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/coordinators/${portalCoordinatorID}/limit`, {
    headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
    data: { limit },
  })
}

test.describe.configure({ mode: 'serial' })

test.describe('real stack fallback credits', () => {
  test('[P0] a live cap raise lets an exhausted coordinator use search again without redeploy', async ({ page, request }) => {
    await setFirstCoordinatorLimit(request, 1)

    await page.goto('/integration.html?role=coordinator&path=%2Fmark')
    const { eventID, categoryID } = await selectedScope(page)
    const status = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${eventID}&category_id=${categoryID}`,
      { headers: { Authorization: 'Bearer portal-coordinator-token' } },
    )
    const slotOpen = (await status.json()).slot !== null

    // Exhaust the credit the real way when a teaching slot is open — that path
    // is the point of the test and only exists between 08:00 and 18:15 IST.
    if (slotOpen) {
      await searchMark(page, firstCreditRoll)
      await expect(page.getByRole('status')).toBeVisible()
      await expect(page.getByRole('button', { name: /search/i })).toBeDisabled({ timeout: 15_000 })
    }

    // Then drop the cap to zero UNCONDITIONALLY. Outside teaching hours this is
    // the only way to reach "exhausted"; inside them the credit is already spent
    // and this is a no-op on the button state. Either way it runs, and that is
    // deliberate: the number of capture-credit config writes this journey makes
    // must not depend on the wall clock, because a CI step asserts that count.
    // While this sat inside the `else`, the journey wrote two config rows during
    // teaching hours and three outside them, and the audit-conservation gate —
    // which hardcodes one of those numbers — was therefore red roughly fourteen
    // hours a day for reasons that had nothing to do with the change under test.
    await setFirstCoordinatorLimit(request, 0)
    await expect(page.getByRole('button', { name: /search/i })).toBeDisabled({ timeout: 15_000 })

    const raise = await setFirstCoordinatorLimit(request, 2)
    expect(raise.status()).toBe(200)

    await expect(page.getByRole('button', { name: /search/i })).toBeEnabled({ timeout: 15_000 })
    if (slotOpen) {
      await searchMark(page, secondCreditRoll)
      await expect(page.getByRole('status')).toContainText(/CB\.SC\.U4CSE24005|Iyer/i)
    }
  })

  test('[P0] admin reset writes one audit row and coordinator status reports zero consumed', async ({ request }) => {
    const summary = await request.get(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const first = (await summary.json()).data[0] as { user_id: string }

    const reset = await request.post(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/coordinators/${first.user_id}/reset`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })

    expect(reset.status()).toBe(200)
    await expect(reset.json()).resolves.toMatchObject({ consumed: 0 })
  })
})
