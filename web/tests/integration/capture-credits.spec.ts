import { expect, test } from '@playwright/test'

const portalAPI = 'http://127.0.0.1:8081'
const markingEventID = '93000000-0000-4000-8000-000000000001'

async function selectedScope(page: import('@playwright/test').Page) {
  const eventSelect = page.getByRole('combobox', { name: /event/i })
  const categorySelect = page.getByRole('combobox', { name: /category/i })
  await expect(eventSelect).not.toHaveValue('')
  await expect(categorySelect).not.toHaveValue('')
  return {
    eventID: await eventSelect.inputValue(),
    categoryID: await categorySelect.inputValue(),
  }
}

async function searchMark(page: import('@playwright/test').Page, roll: string) {
  await page.getByRole('button', { name: /search/i }).click()
  await page.getByRole('searchbox').fill(roll)
  await page.getByRole('button', { name: new RegExp(roll.replace(/\./g, '\\.')) }).click()
}

test.describe.configure({ mode: 'serial' })

test.describe('real stack fallback credits', () => {
  test('[P0] a live cap raise lets an exhausted coordinator use search again without redeploy', async ({ page, request }) => {
    await request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/config`, {
      headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
      data: { limit: 1 },
    })

    await page.goto('/integration.html?role=coordinator&path=%2Fmark')
    const { eventID, categoryID } = await selectedScope(page)
    const status = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${eventID}&category_id=${categoryID}`,
      { headers: { Authorization: 'Bearer portal-coordinator-token' } },
    )
    const slotOpen = (await status.json()).slot !== null

    if (!slotOpen) {
      await request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/config`, {
        headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
        data: { limit: 0 },
      })
      await expect(page.getByRole('button', { name: /search/i })).toBeDisabled({ timeout: 15_000 })
    } else {
      await searchMark(page, 'CB.SC.U4CSE24001')
      await expect(page.getByRole('status')).toBeVisible()
      await expect(page.getByRole('button', { name: /search/i })).toBeDisabled({ timeout: 15_000 })
    }

    const raise = await request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/config`, {
      headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
      data: { limit: 2 },
    })
    expect(raise.status()).toBe(200)

    await expect(page.getByRole('button', { name: /search/i })).toBeEnabled({ timeout: 15_000 })
    if (slotOpen) {
      await searchMark(page, 'CB.SC.U4CSE24002')
      await expect(page.getByRole('status')).toContainText(/CB\.SC\.U4CSE24002|Rao/i)
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
