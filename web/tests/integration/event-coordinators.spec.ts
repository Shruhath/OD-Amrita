import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const eventName = `Browser Coordinator Event ${Date.now().toString(36)}`
const portalAPI = 'http://127.0.0.1:8081'
const candidateEmail = 'coordinator-fixture-student@amrita.edu'
const coordinatorCategoryName = 'Coordinator Duty'

test.describe.configure({ mode: 'serial' })

async function openFixtureEvent(page: Page) {
  await page.getByRole('link', { name: eventName }).first().click()
}

async function createCoordinatorCategory(page: Page) {
  await page.getByLabel('Category name').fill(coordinatorCategoryName)
  await page.getByRole('button', { name: 'Add category' }).click()
  await expect(page.getByRole('list', { name: 'Categories' }).getByText(coordinatorCategoryName)).toBeVisible()
}

async function selectCoordinatorCategory(page: Page) {
  await page.getByRole('checkbox', { name: coordinatorCategoryName }).check()
}

test('assigns a real provisioned student and persists the assignment across reloads', async ({ page }) => {
  await page.goto('/integration.html')
  await expect(page.getByRole('heading', { name: 'Events', exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'New event' }).click()
  const createDialog = page.getByRole('dialog', { name: 'New event' })
  await createDialog.getByLabel('Event name').fill(eventName)
  await createDialog.getByLabel('Start date').fill('2026-11-05')
  await createDialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await createDialog.getByRole('button', { name: 'Create event' }).click()

  await openFixtureEvent(page)
  await createCoordinatorCategory(page)
  await page.getByLabel('Coordinator email').fill(candidateEmail)
  await selectCoordinatorCategory(page)
  await page.getByRole('button', { name: 'Assign coordinator' }).click()

  await expect(page.getByRole('list', { name: 'Coordinators' }).getByText(candidateEmail)).toBeVisible()
  await page.reload()
  await expect(page.getByRole('list', { name: 'Coordinators' }).getByText(candidateEmail)).toBeVisible()
})

test('rejects a duplicate assignment from the real database', async ({ page }) => {
  await page.goto('/integration.html')
  await openFixtureEvent(page)

  await page.getByLabel('Coordinator email').fill(candidateEmail)
  await selectCoordinatorCategory(page)
  await page.getByRole('button', { name: 'Assign coordinator' }).click()

  await expect(page.getByRole('alert')).toContainText(/already assigned/i)
  await expect(page.getByRole('list', { name: 'Coordinators' }).getByRole('listitem')).toHaveCount(1)
})

test('rejects an unknown coordinator email from the real database', async ({ page }) => {
  await page.goto('/integration.html')
  await openFixtureEvent(page)

  await page.getByLabel('Coordinator email').fill('never-signed-in-coordinator@amrita.edu')
  await selectCoordinatorCategory(page)
  await page.getByRole('button', { name: 'Assign coordinator' }).click()

  await expect(page.getByRole('alert')).toContainText(/signed in/i)
  await expect(page.getByRole('list', { name: 'Coordinators' }).getByRole('listitem')).toHaveCount(1)
})

test('rejects an ineligible admin role from the real database', async ({ page }) => {
  await page.goto('/integration.html')
  await openFixtureEvent(page)

  await page.getByLabel('Coordinator email').fill('portal-admin@amrita.edu')
  await selectCoordinatorCategory(page)
  await page.getByRole('button', { name: 'Assign coordinator' }).click()

  await expect(page.getByRole('alert')).toContainText(/assigned role/i)
  await expect(page.getByRole('list', { name: 'Coordinators' }).getByRole('listitem')).toHaveCount(1)
})

test('keeps assignments readable after archive and refuses stale writes', async ({ page }) => {
  await page.goto('/integration.html')
  const eventID = await currentEventID(page.request)

  const row = page.getByRole('row').filter({ hasText: eventName })
  await row.getByRole('button', { name: 'Archive' }).click()
  await page
    .getByRole('dialog', { name: `Archive ${eventName}` })
    .getByRole('button', { name: 'Archive event' })
    .click()
  await expect(row).toContainText('Archived')

  await openFixtureEvent(page)
  await expect(page.getByRole('list', { name: 'Coordinators' }).getByText(candidateEmail)).toBeVisible()
  await expect(page.getByLabel('Coordinator email')).toHaveCount(0)

  const response = await page.request.post(`${portalAPI}/api/v1/admin/events/${eventID}/coordinators`, {
    headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
    data: { email: 'late@amrita.edu', category_ids: [await currentCategoryID(page.request)] },
  })
  expect(response.status()).toBe(409)
  await expect(response.json()).resolves.toMatchObject({ code: 'EVENT_ARCHIVED' })
})

test('denies a real student principal on both coordinator assignment routes', async ({ request }) => {
  const eventPath = `${portalAPI}/api/v1/admin/events`
  const listResponse = await request.get(eventPath, {
    headers: { Authorization: 'Bearer portal-admin-token' },
  })
  const events = (await listResponse.json()) as { data: Array<{ id: string; name: string }> }
  const target = events.data.find((event) => event.name === eventName)
  expect(target, `no event named ${eventName}`).toBeDefined()

  for (const method of ['get', 'post'] as const) {
    const response = await request[method](`${eventPath}/${target!.id}/coordinators`, {
      headers: { Authorization: 'Bearer portal-student-token', 'Content-Type': 'application/json' },
      ...(method === 'post' ? { data: { email: 'student-attempt@amrita.edu', category_ids: [await currentCategoryID(request)] } } : {}),
    })
    expect(response.status()).toBe(403)
    await expect(response.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })
  }
})

async function currentEventID(request: APIRequestContext) {
  const response = await request.get(`${portalAPI}/api/v1/admin/events`, {
    headers: { Authorization: 'Bearer portal-admin-token' },
  })
  const events = (await response.json()) as { data: Array<{ id: string; name: string }> }
  const target = events.data.find((event) => event.name === eventName)
  expect(target, `no event named ${eventName}`).toBeDefined()
  return target!.id
}

async function currentCategoryID(request: APIRequestContext) {
  const eventID = await currentEventID(request)
  const response = await request.get(`${portalAPI}/api/v1/admin/events/${eventID}/categories`, {
    headers: { Authorization: 'Bearer portal-admin-token' },
  })
  const categories = (await response.json()) as { data: Array<{ id: string; name: string }> }
  const target = categories.data.find((category) => category.name === coordinatorCategoryName)
  expect(target, `no category named ${coordinatorCategoryName}`).toBeDefined()
  return target!.id
}
