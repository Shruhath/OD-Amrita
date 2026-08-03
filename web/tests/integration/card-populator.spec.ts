import { expect, test } from '@playwright/test'
import { installFakeWebNfc, nfcWrites } from '../e2e/support/web-nfc'

const portalAPI = 'http://127.0.0.1:8081'

// The seeded populator section from api/test/portal/main.go. Ananya sorts first
// by canonical roll number even though the harness inserts Madhav first.
const seededSection = { branch: 'CSE', batch: '2024', section: 'A' }
const firstStudent = { rollNo: 'CB.SC.U4CSE24001', name: 'Ananya Rao' }
const secondStudent = { rollNo: 'CB.SC.U4CSE24002', name: 'Madhav Rao' }

async function openPopulator(page: import('@playwright/test').Page) {
  await page.goto('/integration.html?path=%2Fportal%2Fcard-populator')
  await expect(page.getByRole('heading', { name: 'Card populator' })).toBeVisible()
  await page.getByLabel('Branch', { exact: true }).selectOption({ label: `${seededSection.branch} — Computer Science and Engineering` })
  await page.getByLabel('Batch', { exact: true }).fill(seededSection.batch)
  await page.getByLabel('Section', { exact: true }).fill(seededSection.section)
  await page.getByRole('button', { name: 'Load roster' }).click()
}

test('[P0] lists real roster rows, seals one card, and resolves it back through Postgres', async ({ page, request }) => {
  await installFakeWebNfc(page)
  await openPopulator(page)

  // The database decides the order. Both seeded section-A rows are present, and
  // the roll-ordered first row is the one the harness inserted second.
  const roster = page.getByRole('table', { name: 'Students to write' })
  await expect(roster.getByRole('row', { name: new RegExp(firstStudent.rollNo) })).toBeVisible()
  await expect(roster.getByRole('row', { name: new RegExp(secondStudent.rollNo) })).toBeVisible()
  const rollCells = await roster.locator('tbody tr td:first-child').allTextContents()
  expect(rollCells.slice(0, 2)).toEqual([firstStudent.rollNo, secondStudent.rollNo])
  // Section B is a different section and must not appear.
  await expect(roster.getByRole('row', { name: /CB\.SC\.U4CSE24003/ })).toHaveCount(0)

  await page.getByRole('button', { name: new RegExp(`Write card for ${firstStudent.name}`, 'i') }).click()
  await expect(page.getByText(/Written, awaiting verify/i)).toBeVisible()

  await page.getByRole('button', { name: new RegExp(`Verify card for ${firstStudent.name}`, 'i') }).click()
  await expect(page.getByText(new RegExp(`Verified ${firstStudent.name}`, 'i'))).toBeVisible()

  // The real seal endpoint produced the payload, and only version+token reached
  // the card — the HMAC key never left the server (NFR16, AR20).
  const written = await nfcWrites(page)
  expect(written).toHaveLength(1)
  expect(Object.keys(JSON.parse(written[0])).sort()).toEqual(['token', 'version'])
  expect(written[0]).not.toMatch(/roll_no|display_name|student_id|branch|section|batch|email/i)

  // The roster read is admin-only against the real API.
  const denied = await request.get(`${portalAPI}/api/v1/admin/students?page=1&page_size=100`, {
    headers: { Authorization: 'Bearer portal-student-token' },
  })
  expect(denied.status()).toBe(403)
  await expect(denied.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })
})

test('[P0] filters the roster server-side rather than in the browser', async ({ page }) => {
  await installFakeWebNfc(page)
  await page.goto('/integration.html?path=%2Fportal%2Fcard-populator')
  await expect(page.getByRole('heading', { name: 'Card populator' })).toBeVisible()

  await page.getByLabel('Branch', { exact: true }).selectOption({ label: 'CSE — Computer Science and Engineering' })
  await page.getByLabel('Batch', { exact: true }).fill('2024')
  await page.getByLabel('Section', { exact: true }).fill('B')
  await page.getByRole('button', { name: 'Load roster' }).click()

  const roster = page.getByRole('table', { name: 'Students to write' })
  await expect(roster.getByRole('row', { name: /CB\.SC\.U4CSE24003/ })).toBeVisible()
  await expect(roster.getByRole('row', { name: new RegExp(firstStudent.rollNo) })).toHaveCount(0)
})

test('[P0] the real seal and resolve endpoints reject a non-admin caller', async ({ request }) => {
  const seal = await request.post(
    `${portalAPI}/api/v1/admin/students/95000000-0000-4000-8000-000000000001/card-seal`,
    { headers: { Authorization: 'Bearer portal-student-token' } },
  )
  expect(seal.status()).toBe(403)

  const resolve = await request.post(`${portalAPI}/api/v1/admin/card-tokens/resolve`, {
    headers: { Authorization: 'Bearer portal-student-token' },
    data: { version: 'v1', token: 'A'.repeat(43) },
  })
  expect(resolve.status()).toBe(403)
})
