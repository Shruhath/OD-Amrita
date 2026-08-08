import { expect, test, type Page } from '@playwright/test'

// Story 2.2 against the REAL stack — the deterministic portal API in
// api/test/portal and a real Postgres.
//
// Invoked by: web-deploy -> portal-integration job
//   (`pnpm --filter od-amrita-web test:e2e:integration`).
//
// Nothing here is mocked. That is the point: the mocked journey in
// tests/e2e/event-categories.spec.ts proves the UI reacts correctly to a
// well-behaved server, and only this file proves the server actually behaves
// that way and that the rows survive in Postgres.
//
// The event name is deliberately DIFFERENT from event-lifecycle.spec.ts's
// fixture. Both specs run against one database in one worker, and two events
// sharing a name would make every `getByRole('row').filter({hasText})` locator
// ambiguous and would double the row count the CI audit assertions check.

// Suffixed per run. Events carry no unique name constraint and this suite
// CREATES its event through the UI, so a fixed name means a second run against a
// long-lived harness leaves two identical rows — ambiguous locators, and audit
// counts that read as a conservation failure. The portal harness's seed() clears
// anything with this prefix, so old runs never accumulate.
const eventName = `Browser Category Event ${Date.now().toString(36)}`

/**
 * Opens the fixture event from the list.
 *
 * `.first()` matters: events carry no unique name constraint and this suite
 * CREATES its event through the UI, so a re-run against an already-seeded
 * database leaves two identically-named rows and a bare role locator fails with
 * a strict-mode violation that looks like a UI defect rather than stale fixture
 * state. The rows are ordered newest-first, so the first match is this run's.
 */
async function openFixtureEvent(page: Page) {
  await page.getByRole('link', { name: eventName }).first().click()
}
const portalAPI = 'http://127.0.0.1:8081'

test('persists categories through the real API and Postgres across reloads', async ({ page }) => {
  await page.goto('/integration.html')
  await expect(page.getByRole('heading', { name: 'Events', exact: true })).toBeVisible()

  // Reuse Story 2.1's create path so this test owns its own event and does not
  // depend on another spec having run first.
  await page.getByRole('button', { name: 'New event' }).click()
  const createDialog = page.getByRole('dialog', { name: 'New event' })
  await createDialog.getByLabel('Event name').fill(eventName)
  await createDialog.getByLabel('Start date').fill('2026-11-05')
  await createDialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await createDialog.getByRole('button', { name: 'Create event' }).click()

  await openFixtureEvent(page)
  await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

  const categories = page.getByRole('list', { name: 'Categories' })
  await expect(page.getByText('No categories yet')).toBeVisible()

  // Journey 5: add several, one command at a time.
  for (const name of ['Stage Setup', 'Procession']) {
    await page.getByLabel('Category name').fill(name)
    await page.getByRole('button', { name: 'Add category' }).click()
    await expect(categories.getByText(name)).toBeVisible()
  }

  // Server order, from a real ORDER BY — not the order they were typed.
  await expect(categories.getByRole('listitem')).toHaveText(['Procession', 'Stage Setup'])

  // The reload is the persistence oracle: every byte of client state is gone.
  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()
  await expect(page.getByRole('list', { name: 'Categories' }).getByRole('listitem')).toHaveText([
    'Procession',
    'Stage Setup',
  ])
})

test('rejects a duplicate category name from the real database', async ({ page }) => {
  await page.goto('/integration.html')
  await openFixtureEvent(page)
  await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

  // The row already exists from the journey above; this is the unique index
  // answering, normalized for case and surrounding whitespace.
  await page.getByLabel('Category name').fill('  procession  ')
  await page.getByRole('button', { name: 'Add category' }).click()

  await expect(page.getByLabel('Category name')).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByText(/already exists/i)).toBeVisible()
  await expect(page.getByRole('list', { name: 'Categories' }).getByRole('listitem')).toHaveText([
    'Procession',
    'Stage Setup',
  ])
})

test('closes the aggregate when the event is archived', async ({ page }) => {
  await page.goto('/integration.html')

  const eventID = await currentEventID(page)

  const row = page.getByRole('row').filter({ hasText: eventName })
  await row.getByRole('button', { name: 'Archive' }).click()
  await page
    .getByRole('dialog', { name: `Archive ${eventName}` })
    .getByRole('button', { name: 'Archive event' })
    .click()
  await expect(row).toContainText('Archived')

  await openFixtureEvent(page)
  await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

  // History stays readable; only writing is closed.
  await expect(page.getByRole('list', { name: 'Categories' }).getByRole('listitem')).toHaveText([
    'Procession',
    'Stage Setup',
  ])
  await expect(page.getByLabel('Category name')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Add category' })).toHaveCount(0)

  // The API refuses independently of whether the UI offered the control.
  const response = await page.request.post(`${portalAPI}/api/v1/admin/events/${eventID}/categories`, {
    headers: { Authorization: 'Bearer portal-admin-token', 'Content-Type': 'application/json' },
    data: { name: 'Late Addition' },
  })
  expect(response.status()).toBe(409)
  await expect(response.json()).resolves.toMatchObject({ code: 'EVENT_ARCHIVED' })
})

test('denies a real Student principal on both category routes', async ({ request }) => {
  // The portal harness verifies real bearer tokens against seeded users, so this
  // exercises the production middleware chain, not a stub.
  const eventPath = `${portalAPI}/api/v1/admin/events`
  const listResponse = await request.get(eventPath, {
    headers: { Authorization: 'Bearer portal-admin-token' },
  })
  const events = (await listResponse.json()) as { data: Array<{ id: string; name: string }> }
  const target = events.data.find((event) => event.name === eventName)
  expect(target).toBeDefined()

  for (const method of ['get', 'post'] as const) {
    const response = await request[method](`${eventPath}/${target!.id}/categories`, {
      headers: { Authorization: 'Bearer portal-student-token', 'Content-Type': 'application/json' },
      ...(method === 'post' ? { data: { name: 'Student Attempt' } } : {}),
    })
    expect(response.status()).toBe(403)
    await expect(response.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })
  }
})

/**
 * Reads the fixture event's id from the API rather than from the page. The
 * harness runs on a memory router, so the browser URL is not an identifier
 * source — and asking the API keeps this independent of how the UI is routed.
 */
async function currentEventID(page: Page) {
  const response = await page.request.get(`${portalAPI}/api/v1/admin/events`, {
    headers: { Authorization: 'Bearer portal-admin-token' },
  })
  const events = (await response.json()) as { data: Array<{ id: string; name: string }> }
  const target = events.data.find((event) => event.name === eventName)
  expect(target, `no event named ${eventName}`).toBeDefined()
  return target!.id
}
