import { selectValue } from '../support/select'
import { expect, test, type Page, type Request } from '@playwright/test'
import {
  cardPayload,
  cardPopulatorBranch,
  cardPopulatorStudent,
  sealResponse,
  studentCollection,
} from '../support/factories/card-populator'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import { installFakeWebNfc, nfcWrites, removeWebNfc } from './support/web-nfc'

async function mockPopulatorApi(page: Page, role: 'admin' | 'student' = 'admin') {
  // Roll-ordered, as the server returns them.
  const students = [
    cardPopulatorStudent(1, { display_name: 'Ananya Rao', roll_no: 'CB.SC.U4CSE24001' }),
    cardPopulatorStudent(2, { display_name: 'Madhav Rao', roll_no: 'CB.SC.U4CSE24002' }),
  ]
  const cardRequests: Request[] = []
  await installPortalSession(page, role)
  await page.route('**/api/v1/auth/me', (route) =>
    json(route, {
      subject: `populator-${role}`,
      email: `${role}@amrita.edu`,
      name: `Populator ${role}`,
      role,
    }),
  )
  await page.route('**/api/v1/admin/branches', (route) => json(route, { data: [cardPopulatorBranch] }))
  await page.route('**/api/v1/admin/students?**', (route) => json(route, studentCollection(students)))
  await page.route('**/api/v1/admin/students/*/card-seal', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    cardRequests.push(route.request())
    return json(route, sealResponse(students[0], cardPayload()), 201)
  })
  await page.route('**/api/v1/admin/card-tokens/resolve', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    cardRequests.push(route.request())
    return json(route, students[0])
  })
  return { cardRequests, students }
}

async function loadSection(page: Page) {
  await expect(page.getByRole('heading', { name: 'Card populator' })).toBeVisible()
  await selectValue(page, page.getByLabel('Branch', { exact: true }), cardPopulatorBranch.id)
  await page.getByLabel('Batch', { exact: true }).fill('2024')
  // Exact: the nav landmark is labelled "Administration sections", which a
  // substring match on "Section" also resolves to.
  await page.getByLabel('Section', { exact: true }).fill('A')
  await page.getByRole('button', { name: 'Load roster' }).click()
}

test.describe('Card populator journey', () => {
  test('[P0] loads a section roster, writes a card, then verifies the same student', async ({ page }) => {
    const { cardRequests } = await mockPopulatorApi(page)
    await installFakeWebNfc(page)
    await page.goto('/portal/card-populator')

    await loadSection(page)
    await expect(page.getByRole('table', { name: 'Students to write' }).getByRole('row', { name: /Ananya Rao/ })).toBeVisible()

    await page.getByRole('button', { name: /Write card for Ananya Rao/i }).click()
    await expect(page.getByText(/Written, awaiting verify/i)).toBeVisible()
    await page.getByRole('button', { name: /Verify card for Ananya Rao/i }).click()
    await expect(page.getByText(/Verified Ananya Rao/i)).toBeVisible()

    expect(cardRequests.map((request) => request.method())).toEqual(['POST', 'POST'])

    // Exactly the sealed payload reached the radio — no identity fields.
    const written = await nfcWrites(page)
    expect(written).toHaveLength(1)
    expect(JSON.parse(written[0])).toEqual(cardPayload())
    expect(written[0]).not.toMatch(/roll_no|display_name|student_id|branch|section|batch|email/i)
  })

  test('[P0] shows unsupported Web NFC as an explicit error and disables card actions', async ({ page }) => {
    const { cardRequests } = await mockPopulatorApi(page)
    await removeWebNfc(page)
    await page.goto('/portal/card-populator')

    await loadSection(page)

    await expect(page.getByRole('alert')).toContainText(/cannot use NFC/i)
    await expect(page.getByRole('button', { name: /Write card for Ananya Rao/i })).toBeDisabled()
    await expect(page.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeDisabled()
    // Roster loading is fine without NFC; only card-token calls must not happen.
    expect(cardRequests).toHaveLength(0)
  })

  // Verification is decided by the SERVER-resolved student, not by a card being
  // present. A card carrying another student's token must fail loudly.
  test('[P0] refuses to verify when the card resolves to a different student', async ({ page }) => {
    const { students } = await mockPopulatorApi(page)
    await installFakeWebNfc(page)
    await page.route('**/api/v1/admin/card-tokens/resolve', async (route) => {
      if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
      return json(route, students[1])
    })
    await page.goto('/portal/card-populator')

    await loadSection(page)
    await page.getByRole('button', { name: /Write card for Ananya Rao/i }).click()
    await expect(page.getByText(/Written, awaiting verify/i)).toBeVisible()
    await page.getByRole('button', { name: /Verify card for Ananya Rao/i }).click()

    await expect(page.getByText(/This card belongs to Madhav Rao/i)).toBeVisible()
    await expect(page.getByText(/0 verified/i)).toBeVisible()
    // The retry path stays available for the same student.
    await expect(page.getByRole('button', { name: /Rewrite card for Ananya Rao/i })).toBeEnabled()
  })

  test('[P0] shows a blank or foreign card as a card error with a retry path', async ({ page }) => {
    await mockPopulatorApi(page)
    // A blank card: the reader sees a tap but our record is absent.
    await installFakeWebNfc(page, { readPayload: null })
    await page.goto('/portal/card-populator')

    await loadSection(page)
    await page.getByRole('button', { name: /Verify card for Ananya Rao/i }).click()

    await expect(page.getByText(/no OD-Amrita card payload/i)).toBeVisible()
    await expect(page.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeEnabled()
    await expect(page.getByText(/0 verified/i)).toBeVisible()
  })

  test('[P0] denies non-admins before roster or card-token requests', async ({ page }) => {
    const { cardRequests } = await mockPopulatorApi(page, 'student')
    await installFakeWebNfc(page)
    const observedAdminRequests: string[] = []
    page.on('request', (request) => {
      if (request.url().includes('/api/v1/admin/students') || request.url().includes('/api/v1/admin/card-tokens')) {
        observedAdminRequests.push(request.url())
      }
    })

    await page.goto('/portal/card-populator')

    await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
    expect(cardRequests).toHaveLength(0)
    expect(observedAdminRequests).toHaveLength(0)
  })

  // AC 6: progress survives a reload, and storage never holds card material.
  test('[P0] recovers progress across a reload without storing card material', async ({ page }) => {
    await mockPopulatorApi(page)
    await installFakeWebNfc(page)
    await page.goto('/portal/card-populator')

    await loadSection(page)
    await page.getByRole('button', { name: /Write card for Ananya Rao/i }).click()
    await expect(page.getByText(/Written, awaiting verify/i)).toBeVisible()

    const stored = await page.evaluate(() => window.sessionStorage.getItem('od-amrita.card-populator.progress.v1') ?? '')
    expect(stored).toContain('written')
    expect(stored).not.toContain(cardPayload().token)

    await page.reload()
    await loadSection(page)
    await expect(page.getByText(/Written, awaiting verify/i)).toBeVisible()
  })
})
