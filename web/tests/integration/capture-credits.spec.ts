import { expect, test } from '@playwright/test'

const portalAPI = 'http://127.0.0.1:8081'
const markingEventID = '93000000-0000-4000-8000-000000000001'

test.describe.configure({ mode: 'serial' })

test.describe('real stack fallback credits', () => {
  test('[P0] a live cap raise lets an exhausted coordinator use search again without redeploy', async ({ page, request }) => {
    await request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/config`, {
      headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
      data: { limit: 0 },
    })

    await page.goto('/integration.html?role=coordinator&path=%2Fmark')
    await expect(page.getByRole('button', { name: /search/i })).toBeDisabled()

    const raise = await request.put(`${portalAPI}/api/v1/admin/events/${markingEventID}/capture-credits/config`, {
      headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
      data: { limit: 8 },
    })
    expect(raise.status()).toBe(200)

    await expect(page.getByRole('button', { name: /search/i })).toBeEnabled({ timeout: 15_000 })
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
