import { expect, test, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

// Story 2.2 browser journey, AC 7 (and AC 1-4 as the user observes them).
//
// Invoked by: web-deploy -> build job (`pnpm --filter od-amrita-web test:e2e`,
// chromium project, testMatch /e2e\/.*\.spec\.ts/).
//
// Every route is intercepted BEFORE page.goto, so no assertion can race a live
// request. There are no hard sleeps and no CSS or XPath selectors: everything is
// queried by accessible role, label or text, which is also how the a11y baseline
// gets exercised for free.
//

type CategoryRecord = {
  id: string
  event_id: string
  name: string
  created_at: string
}

type Branch = { id: string; code: string; name: string }

type EventRecord = {
  id: string
  name: string
  event_date: string
  status: 'active' | 'archived'
  branches: Branch[]
  created_at: string
  archived_at: string | null
}

type Scenario = {
  status?: 'active' | 'archived'
  categories?: CategoryRecord[]
  createStatus?: number
  createBody?: unknown
  role?: 'admin' | 'student'
}

const computerScience: Branch = {
  id: '11111111-1111-4111-8111-111111111111',
  code: 'CSE',
  name: 'Computer Science and Engineering',
}

const eventID = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc'
const eventName = 'Onam Celebrations'

function eventRecord(status: 'active' | 'archived'): EventRecord {
  return {
    id: eventID,
    name: eventName,
    event_date: '2026-08-15',
    status,
    branches: [computerScience],
    created_at: '2026-07-20T03:00:00Z',
    archived_at: status === 'archived' ? '2026-07-21T03:00:00Z' : null,
  }
}

function categoryRecord(name: string, index: number): CategoryRecord {
  return {
    id: `dddddddd-dddd-4ddd-8ddd-${String(index).padStart(12, '0')}`,
    event_id: eventID,
    name,
    created_at: '2026-07-21T03:00:00Z',
  }
}

const ordered = (categories: CategoryRecord[]) =>
  [...categories].sort((left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id))

/**
 * Installs every boundary the portal touches. Called before navigation so the
 * first render already has deterministic data — the network-first rule.
 */
async function mockApi(page: Page, scenario: Scenario = {}) {
  const { status = 'active', categories = [], createStatus = 201, createBody, role = 'admin' } = scenario
  await installPortalSession(page, role)
  let serverCategories = ordered(categories)
  let created = 0

  await page.route('**/api/v1/auth/me', (route) =>
    json(route, { subject: 'atdd-admin', email: 'admin@amrita.edu', name: 'ATDD admin', role: 'admin' }),
  )
  await page.route('**/api/v1/admin/branches', (route) => json(route, { data: [computerScience] }))
  await page.route('**/api/v1/admin/events?*', (route) =>
    json(route, { data: [eventRecord(status)], page: 1, page_size: 20, total: 1 }),
  )
  await page.route('**/api/v1/admin/events', (route) =>
    json(route, { data: [eventRecord(status)], page: 1, page_size: 20, total: 1 }),
  )
  // The detail view reads its event by ID rather than filtering the collection
  // above, so this route is what the whole journey depends on. Registered before
  // the categories route because Playwright matches the MOST RECENTLY added
  // handler first and `**/events/<id>` would otherwise swallow the nested path.
  await page.route(`**/api/v1/admin/events/${eventID}`, (route) =>
    json(route, eventRecord(status)),
  )
  await page.route(`**/api/v1/admin/events/${eventID}/categories`, async (route) => {
    // The browser preflights the cross-origin POST before it ever sends it.
    if (route.request().method() === 'OPTIONS') {
      await fulfilPreflight(route)
      return
    }
    if (route.request().method() === 'POST') {
      if (createStatus !== 201) {
        await json(route, createBody, createStatus)
        return
      }
      const input = route.request().postDataJSON() as { name: string }
      const record = categoryRecord(input.name.trim(), ++created + 100)
      serverCategories = ordered([...serverCategories, record])
      await json(route, record, 201)
      return
    }
    await json(route, { data: serverCategories })
  })

}

test.describe('Admin event categories journey (ATDD)', () => {
  test('[P0] completes the full Journey 5 path: open, add, reload, persist', async ({ page }) => {
    await mockApi(page, { categories: [categoryRecord('Registration', 1)] })
    await page.goto('/portal/events')

    // From the events list, open the event.
    await page.getByRole('link', { name: eventName }).click()
    await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

    const list = page.getByRole('list', { name: 'Categories' })
    await expect(list.getByRole('listitem')).toHaveText(['Registration'])

    // Journey 5 adds several in a row, so the form must survive each submit.
    for (const name of ['Procession', 'Stage Setup']) {
      await page.getByLabel('Category name').fill(name)
      await page.getByRole('button', { name: 'Add category' }).click()
      await expect(list.getByText(name)).toBeVisible()
    }

    // Server order is name ASC — not insertion order.
    await expect(list.getByRole('listitem')).toHaveText(['Procession', 'Registration', 'Stage Setup'])

    // The reload is the whole point: it discards every scrap of client state, so
    // whatever survives came from the server.
    await page.reload()
    await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()
    await expect(page.getByRole('list', { name: 'Categories' }).getByRole('listitem')).toHaveText([
      'Procession',
      'Registration',
      'Stage Setup',
    ])
  })

  test('[P0] shows an archived event read-only with no add affordance', async ({ page }) => {
    await mockApi(page, { status: 'archived', categories: [categoryRecord('Procession', 2)] })
    await page.goto('/portal/events')

    await page.getByRole('link', { name: eventName }).click()
    await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

    await expect(page.getByRole('list', { name: 'Categories' }).getByText('Procession')).toBeVisible()
    await expect(page.getByText('Archived', { exact: true })).toBeVisible()
    // Absent, not disabled: an archived aggregate can never accept this write.
    await expect(page.getByLabel('Category name')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Add category' })).toHaveCount(0)
  })

  test('[P0] surfaces a duplicate name as a field error and adds nothing', async ({ page }) => {
    await mockApi(page, {
      categories: [categoryRecord('Procession', 3)],
      createStatus: 400,
      createBody: {
        code: 'VALIDATION_FAILED',
        message: 'Category validation failed.',
        details: [{ field: 'name', code: 'duplicate', message: 'A category with this name already exists.' }],
      },
    })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    // Case and surrounding space are the server's business: it normalizes before
    // comparing, so the client must not pre-empt the answer.
    await page.getByLabel('Category name').fill('  procession  ')
    await page.getByRole('button', { name: 'Add category' }).click()

    await expect(page.getByText('A category with this name already exists.')).toBeVisible()
    await expect(page.getByLabel('Category name')).toHaveAttribute('aria-invalid', 'true')
    await expect(page.getByRole('list', { name: 'Categories' }).getByRole('listitem')).toHaveCount(1)
  })

  test('[P1] announces a state-conflict rejection without blaming the field', async ({ page }) => {
    await mockApi(page, {
      createStatus: 409,
      createBody: { code: 'EVENT_ARCHIVED', message: 'This event is archived and can no longer be changed.' },
    })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    await page.getByLabel('Category name').fill('Procession')
    await page.getByRole('button', { name: 'Add category' }).click()

    await expect(page.getByRole('alert')).toHaveText('This event is archived and can no longer be changed.')
    await expect(page.getByLabel('Category name')).not.toHaveAttribute('aria-invalid', 'true')
  })

  test('[P1] never issues a category request for a non-Admin caller', async ({ page }) => {
    const categoryRequests: string[] = []
    page.on('request', (request) => {
      if (new URL(request.url()).pathname.includes('/categories')) categoryRequests.push(request.url())
    })
    await mockApi(page, { role: 'student' })
    await page.route('**/api/v1/auth/me', (route) =>
      json(route, { subject: 'atdd-student', email: 'student@amrita.edu', name: 'ATDD student', role: 'student' }),
    )

    await page.goto(`/portal/events/${eventID}`)

    // The server is the authority, but a client that fires the request anyway
    // has already leaked the intent and will render a flash of privileged chrome.
    await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
    expect(categoryRequests).toEqual([])
  })

  test('[P2] shows an honest empty state and a retryable error', async ({ page }) => {
    await mockApi(page, { categories: [] })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    await expect(page.getByText('No categories yet')).toBeVisible()
    // Present, but gated until a name is typed — the same rule the component
    // test pins and Task 5 mandates. An empty list does not make blank a name.
    await expect(page.getByRole('button', { name: 'Add category' })).toBeVisible()

    // Failing the read must not leave a spinner running forever.
    await page.route(`**/api/v1/admin/events/${eventID}/categories`, (route) =>
      json(route, { code: 'INTERNAL', message: 'Categories could not be loaded' }, 500),
    )
    await page.reload()
    await expect(page.getByRole('alert')).toContainText('Categories could not be loaded')
    await expect(page.getByRole('button', { name: 'Try again' })).toBeEnabled()
  })

  test('[P2] keeps the detail view usable at a tablet viewport', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await mockApi(page, { categories: [categoryRecord('Procession', 4)] })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    const submit = page.getByRole('button', { name: 'Add category' })
    await expect(submit).toBeVisible()

    // A control that is present but clipped out of the viewport is not usable.
    const box = await submit.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x + box!.width).toBeLessThanOrEqual(768)
    await expect(page.getByLabel('Category name')).toBeVisible()
  })
})
