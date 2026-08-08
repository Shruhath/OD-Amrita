import { expect, test, type Page, type Route } from '@playwright/test'

type Role = 'admin' | 'teacher' | 'coordinator' | 'student'

type Branch = {
  id: string
  code: string
  name: string
}

type EventRecord = {
  id: string
  name: string
  event_date: string
  status: 'active' | 'archived'
  branches: Branch[]
  created_at: string
  archived_at: string | null
}

type Deferred = {
  promise: Promise<void>
  resolve: () => void
}

type ApiScenario = {
  role?: Role
  branches?: Branch[]
  events?: EventRecord[]
  branchesStatus?: number
  eventsStatuses?: number[]
  createStatus?: number
  archiveStatus?: number
  branchesGate?: Promise<void>
  eventsGate?: Promise<void>
  createGate?: Promise<void>
  archiveGate?: Promise<void>
}

type ApiMock = {
  calls: {
    branches: number
    listEvents: number
    create: number
    archive: number
  }
  events: EventRecord[]
}

const computerScience: Branch = {
  id: '11111111-1111-4111-8111-111111111111',
  code: 'CSE',
  name: 'Computer Science and Engineering',
}

const electronics: Branch = {
  id: '22222222-2222-4222-8222-222222222222',
  code: 'ECE',
  name: 'Electronics and Communication Engineering',
}

const activeEvent: EventRecord = {
  id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  name: 'Innovation Day',
  event_date: '2026-08-15',
  status: 'active',
  branches: [computerScience, electronics],
  created_at: '2026-07-20T03:00:00Z',
  archived_at: null,
}

const archivedEvent: EventRecord = {
  id: 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb',
  name: 'Research Symposium',
  event_date: '2026-06-10',
  status: 'archived',
  branches: [computerScience],
  created_at: '2026-05-01T03:00:00Z',
  archived_at: '2026-06-11T03:00:00Z',
}

function deferred(): Deferred {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

function json(route: Route, status: number, body: unknown) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    headers: {
      'Access-Control-Allow-Origin': 'http://127.0.0.1:4173',
      'Access-Control-Allow-Headers': 'Authorization, Content-Type',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    },
    body: JSON.stringify(body),
  })
}

async function installSession(page: Page, role: Role | null) {
  await page.addInitScript(({ sessionRole }) => {
    localStorage.clear()
    if (sessionRole === null) return

    localStorage.setItem(
      'sb-test-auth-token',
      JSON.stringify({
        access_token: 'e2e-access-token',
        refresh_token: 'e2e-refresh-token',
        token_type: 'bearer',
        expires_in: 3600,
        expires_at: 4102444800,
        user: {
          id: '99999999-9999-4999-8999-999999999999',
          email: 'admin@amrita.edu',
          role: 'authenticated',
          app_metadata: { provider: 'azure', providers: ['azure'] },
          user_metadata: { name: 'E2E Admin' },
        },
      }),
    )
  }, { sessionRole: role })

  await page.route('**/auth/v1/**', async (route) => {
    if (role === null) {
      await json(route, 401, { message: 'No session' })
      return
    }

    await json(route, 200, {
      id: '99999999-9999-4999-8999-999999999999',
      email: role === 'admin' ? 'admin@amrita.edu' : `${role}@amrita.edu`,
      role: 'authenticated',
      user_metadata: { name: `E2E ${role}` },
    })
  })
}

async function installApi(page: Page, scenario: ApiScenario = {}): Promise<ApiMock> {
  const branches = scenario.branches ?? [computerScience, electronics]
  const state: ApiMock = {
    calls: { branches: 0, listEvents: 0, create: 0, archive: 0 },
    events: structuredClone(scenario.events ?? [activeEvent, archivedEvent]),
  }
  let eventStatusIndex = 0

  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const method = request.method()

    if (method === 'OPTIONS') {
      await route.fulfill({
        status: 204,
        headers: {
          'Access-Control-Allow-Origin': 'http://127.0.0.1:4173',
          'Access-Control-Allow-Headers': 'Authorization, Content-Type',
          'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
        },
      })
      return
    }

    if (url.pathname === '/api/v1/auth/me' && method === 'GET') {
      const role = scenario.role ?? 'admin'
      await json(route, 200, {
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        subject: `e2e-${role}-subject`,
        role,
      })
      return
    }

    if (url.pathname === '/api/v1/admin/branches' && method === 'GET') {
      state.calls.branches += 1
      if (scenario.branchesGate) await scenario.branchesGate
      const status = scenario.branchesStatus ?? 200
      await json(route, status, status === 200
        ? { data: branches }
        : { code: 'INTERNAL', message: 'Branches could not be loaded', details: null })
      return
    }

    if (url.pathname === '/api/v1/admin/events' && method === 'GET') {
      state.calls.listEvents += 1
      if (scenario.eventsGate) await scenario.eventsGate
      const statuses = scenario.eventsStatuses ?? [200]
      const status = statuses[Math.min(eventStatusIndex, statuses.length - 1)]
      eventStatusIndex += 1
      await json(route, status, status === 200
        ? { data: state.events, page: 1, page_size: 20, total: state.events.length }
        : { code: 'INTERNAL', message: 'Events could not be loaded', details: null })
      return
    }

    if (url.pathname === '/api/v1/admin/events' && method === 'POST') {
      state.calls.create += 1
      if (scenario.createGate) await scenario.createGate
      const status = scenario.createStatus ?? 201
      if (status !== 201) {
        await json(route, status, {
          code: 'VALIDATION_FAILED',
          message: 'Event validation failed',
          details: [
            { field: 'name', code: 'duplicate', message: 'An event with this name and date already exists' },
          ],
        })
        return
      }

      const input = request.postDataJSON() as {
        name: string
        event_date: string
        branch_ids: string[]
      }
      const created: EventRecord = {
        id: 'cccccccc-cccc-4ccc-8ccc-cccccccccccc',
        name: input.name,
        event_date: input.event_date,
        status: 'active',
        branches: branches.filter((branch) => input.branch_ids.includes(branch.id)),
        created_at: '2026-07-20T04:00:00Z',
        archived_at: null,
      }
      state.events = [created, ...state.events]
      await json(route, 201, created)
      return
    }

    const archiveMatch = url.pathname.match(/^\/api\/v1\/admin\/events\/([^/]+)\/archive$/)
    if (archiveMatch && method === 'POST') {
      state.calls.archive += 1
      if (scenario.archiveGate) await scenario.archiveGate
      const status = scenario.archiveStatus ?? 200
      const event = state.events.find((candidate) => candidate.id === archiveMatch[1])

      if (status !== 200) {
        await json(route, status, {
          code: 'INTERNAL',
          message: 'The event could not be archived',
          details: null,
        })
        return
      }

      if (!event) {
        await json(route, 404, { code: 'EVENT_NOT_FOUND', message: 'Event not found', details: null })
        return
      }

      if (event.status === 'active') {
        event.status = 'archived'
        event.archived_at = '2026-07-20T04:10:00Z'
      }
      await json(route, 200, event)
      return
    }

    await json(route, 404, { code: 'NOT_FOUND', message: 'Unexpected E2E request', details: null })
  })

  return state
}

async function openAsAdmin(page: Page, scenario: ApiScenario = {}) {
  await installSession(page, 'admin')
  const api = await installApi(page, { ...scenario, role: 'admin' })
  await page.goto('/portal/events')
  return api
}

function eventRow(page: Page, eventName: string) {
  return page.getByRole('row').filter({ hasText: eventName })
}

test.describe('Admin event administration E2E journeys (ATDD red phase)', () => {
  test('[P0] gates the portal behind Microsoft authentication and sends no Admin event request without a session', async ({ page }) => {
    await installSession(page, null)
    const api = await installApi(page)

    await page.goto('/portal/events')

    await expect(page.getByRole('heading', { name: 'Sign in to OD-Amrita' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Sign in with Microsoft' })).toBeVisible()
    expect(api.calls.branches).toBe(0)
    expect(api.calls.listEvents).toBe(0)
  })

  test('[P1] resolves the Admin identity and loads server-owned branches plus active and archived events', async ({ page }) => {
    const api = await openAsAdmin(page)

    await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible()
    // Branches render as their CODE, so the server-owned NAME is asserted
    // through the abbreviation's expansion. Still a real check that the name
    // came from the server rather than being invented by the client.
    await expect(page.getByTitle('Computer Science and Engineering').first()).toBeVisible()
    await expect(page.getByTitle('Electronics and Communication Engineering').first()).toBeVisible()

    const activeRow = eventRow(page, 'Innovation Day')
    await expect(activeRow).toContainText('15 Aug 2026')
    await expect(activeRow).toContainText('Active')
    await expect(activeRow).toContainText('CSE')
    await expect(activeRow).toContainText('ECE')

    const archivedRow = eventRow(page, 'Research Symposium')
    await expect(archivedRow).toContainText('Archived')
    expect(api.calls.branches).toBe(1)
    expect(api.calls.listEvents).toBe(1)
  })

  test('[P2] exposes determinate loading states while branch and event requests are pending', async ({ page }) => {
    const branchesGate = deferred()
    const eventsGate = deferred()
    await installSession(page, 'admin')
    await installApi(page, { branchesGate: branchesGate.promise, eventsGate: eventsGate.promise })

    await page.goto('/portal/events')

    await expect(page.getByRole('status', { name: 'Loading branches' })).toBeVisible()
    await expect(page.getByRole('status', { name: 'Loading events' })).toBeVisible()

    branchesGate.resolve()
    eventsGate.resolve()
    await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible()
    await expect(page.getByRole('status', { name: 'Loading events' })).toHaveCount(0)
  })

  test('[P1] creates a multi-branch event, reloads persisted state, archives it, and retains its read-only history after reload', async ({ page }) => {
    const api = await openAsAdmin(page, { events: [] })

    await page.getByRole('button', { name: 'New event' }).click()
    const dialog = page.getByRole('dialog', { name: 'New event' })
    await dialog.getByLabel('Event name').fill('Community Outreach Day')
    await dialog.getByLabel('Start date').fill('2026-09-05')
    await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
    await dialog.getByRole('checkbox', { name: 'Electronics and Communication Engineering' }).check()

    const createRequest = page.waitForRequest((request) =>
      request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/admin/events')
    await dialog.getByRole('button', { name: 'Create event' }).click()
    const request = await createRequest
    // `end_date` follows the start date until the admin moves it, so a one-day
    // event sends the two dates equal rather than omitting the second. Kept an
    // exact `toEqual`: this is the gate that forces a new request field to be a
    // decision instead of a drift.
    expect(request.postDataJSON()).toEqual({
      name: 'Community Outreach Day',
      event_date: '2026-09-05',
      end_date: '2026-09-05',
      branch_ids: [computerScience.id, electronics.id],
    })

    const row = eventRow(page, 'Community Outreach Day')
    await expect(row).toContainText('CSE')
    await expect(row).toContainText('ECE')
    await expect(row).toContainText('Active')

    await page.reload()
    await expect(eventRow(page, 'Community Outreach Day')).toBeVisible()

    await eventRow(page, 'Community Outreach Day').getByRole('button', { name: 'Archive' }).click()
    const confirmation = page.getByRole('dialog', { name: 'Archive Community Outreach Day' })
    await expect(confirmation).toContainText('Community Outreach Day')
    const archiveRequest = page.waitForRequest((candidate) =>
      candidate.method() === 'POST' && candidate.url().endsWith('/events/cccccccc-cccc-4ccc-8ccc-cccccccccccc/archive'))
    await confirmation.getByRole('button', { name: 'Archive event' }).click()
    await archiveRequest

    await expect(eventRow(page, 'Community Outreach Day')).toContainText('Archived')
    await page.reload()
    const reloadedRow = eventRow(page, 'Community Outreach Day')
    await expect(reloadedRow).toContainText('Archived')
    await expect(reloadedRow.getByRole('button', { name: 'Archive' })).toHaveCount(0)
    expect(api.calls.create).toBe(1)
    expect(api.calls.archive).toBe(1)
  })

  test('[P1] cancels archive confirmation without mutating the event and returns focus to the invoking command', async ({ page }) => {
    const api = await openAsAdmin(page, { events: [activeEvent] })
    const archiveButton = eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' })

    await archiveButton.click()
    const dialog = page.getByRole('dialog', { name: 'Archive Innovation Day' })
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancel' }).click()

    await expect(dialog).toHaveCount(0)
    await expect(archiveButton).toBeFocused()
    await expect(eventRow(page, 'Innovation Day')).toContainText('Active')
    expect(api.calls.archive).toBe(0)
  })

  test('[P0] disables an in-flight archive confirmation and sends only one transition request', async ({ page }) => {
    const archiveGate = deferred()
    const api = await openAsAdmin(page, { events: [activeEvent], archiveGate: archiveGate.promise })

    await eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' }).click()
    const confirmation = page.getByRole('dialog', { name: 'Archive Innovation Day' })
    const confirmButton = confirmation.getByRole('button', { name: 'Archive event' })
    const archiveRequest = page.waitForRequest((request) =>
      request.method() === 'POST' && request.url().endsWith(`/events/${activeEvent.id}/archive`))
    await confirmButton.click()
    await archiveRequest

    await expect(confirmButton).toBeDisabled()
    await expect(confirmation.getByRole('status')).toContainText('Archiving')
    expect(api.calls.archive).toBe(1)

    archiveGate.resolve()
    await expect(eventRow(page, 'Innovation Day')).toContainText('Archived')
    expect(api.calls.archive).toBe(1)
  })

  test('[P1] keeps archived events visible and removes every mutation affordance', async ({ page }) => {
    await openAsAdmin(page, { events: [archivedEvent] })

    const row = eventRow(page, 'Research Symposium')
    await expect(row).toBeVisible()
    await expect(row.getByText('Archived', { exact: true })).toBeVisible()
    await expect(row.getByRole('button', { name: /archive/i })).toHaveCount(0)
    await expect(row.getByRole('button', { name: /edit/i })).toHaveCount(0)
    await expect(row.getByRole('button', { name: /delete/i })).toHaveCount(0)
  })

  test('[P0] shows explicit access denied for a non-Admin and never requests branches or events', async ({ page }) => {
    await installSession(page, 'teacher')
    const api = await installApi(page, { role: 'teacher' })

    await page.goto('/portal/events')

    await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
    await expect(page.getByText('Administrator access is required')).toBeVisible()
    expect(api.calls.branches).toBe(0)
    expect(api.calls.listEvents).toBe(0)
    expect(api.calls.create).toBe(0)
    expect(api.calls.archive).toBe(0)
  })

  test('[P2] explains an empty branch source and prevents event creation', async ({ page }) => {
    await openAsAdmin(page, { branches: [], events: [] })

    await expect(page.getByRole('heading', { name: 'No branches available' })).toBeVisible()
    await expect(page.getByText('Branches must be configured before an event can be created')).toBeVisible()
    await expect(page.getByRole('button', { name: 'New event' })).toBeDisabled()
  })

  test('[P2] renders an honest empty-events state without hiding the creation command', async ({ page }) => {
    await openAsAdmin(page, { events: [] })

    await expect(page.getByRole('heading', { name: 'No events yet' })).toBeVisible()
    await expect(page.getByText('Create the first event to begin the calendar')).toBeVisible()
    await expect(page.getByRole('button', { name: 'New event' })).toBeEnabled()
  })

  test('[P1] presents a retryable list error and recovers through a deterministic refetch', async ({ page }) => {
    const api = await openAsAdmin(page, { eventsStatuses: [500, 200] })

    const alert = page.getByRole('alert')
    await expect(alert).toContainText('Events could not be loaded')
    const retryRequest = page.waitForRequest((request) =>
      request.method() === 'GET' && new URL(request.url()).pathname === '/api/v1/admin/events')
    await alert.getByRole('button', { name: 'Try again' }).click()
    await retryRequest

    await expect(eventRow(page, 'Innovation Day')).toBeVisible()
    expect(api.calls.listEvents).toBe(2)
  })

  test('[P1] maps typed server validation details to the field and announces the mutation error', async ({ page }) => {
    await openAsAdmin(page, { events: [], createStatus: 400 })

    await page.getByRole('button', { name: 'New event' }).click()
    const dialog = page.getByRole('dialog', { name: 'New event' })
    const name = dialog.getByLabel('Event name')
    await name.fill('Innovation Day')
    await dialog.getByLabel('Start date').fill('2026-08-15')
    await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
    await dialog.getByRole('button', { name: 'Create event' }).click()

    await expect(dialog.getByText('An event with this name and date already exists')).toBeVisible()
    await expect(name).toHaveAttribute('aria-invalid', 'true')
    await expect(name).toHaveAttribute('aria-describedby')
    await expect(dialog.getByRole('alert')).toContainText('Event validation failed')
  })

  test('[P1] preserves the active row and announces a retryable archive failure', async ({ page }) => {
    await openAsAdmin(page, { events: [activeEvent], archiveStatus: 500 })

    await eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' }).click()
    const dialog = page.getByRole('dialog', { name: 'Archive Innovation Day' })
    await dialog.getByRole('button', { name: 'Archive event' }).click()

    await expect(page.getByRole('alert')).toContainText('The event could not be archived')
    await expect(eventRow(page, 'Innovation Day')).toContainText('Active')
    await expect(eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' })).toBeEnabled()
  })

  test('[P2] supports keyboard-only form and archive-dialog operation with trapped and restored focus', async ({ page }) => {
    await openAsAdmin(page, { events: [activeEvent] })

    const newEvent = page.getByRole('button', { name: 'New event' })
    await newEvent.press('Enter')
    const createDialog = page.getByRole('dialog', { name: 'New event' })
    await expect(createDialog.getByLabel('Event name')).toBeFocused()
    await createDialog.getByLabel('Event name').fill('Keyboard Event')
    await createDialog.getByLabel('Start date').fill('2026-10-12')
    const branch = createDialog.getByRole('checkbox', { name: 'Computer Science and Engineering' })
    await branch.focus()
    await page.keyboard.press('Space')
    await expect(branch).toBeChecked()
    await page.keyboard.press('Escape')
    await expect(createDialog).toHaveCount(0)
    await expect(newEvent).toBeFocused()

    const archive = eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' })
    await archive.press('Enter')
    const archiveDialog = page.getByRole('dialog', { name: 'Archive Innovation Day' })
    await expect(archiveDialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(archiveDialog.getByRole('button', { name: 'Archive event' })).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(archive).toBeFocused()
  })

  test('[P2] remains usable at a tablet viewport without clipped controls or horizontal overflow', async ({ page }) => {
    await page.setViewportSize({ width: 834, height: 1112 })
    await openAsAdmin(page)

    await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'New event' })).toBeInViewport()
    await expect(eventRow(page, 'Innovation Day').getByRole('button', { name: 'Archive' })).toBeInViewport()
    const hasHorizontalOverflow = await page.evaluate(() =>
      document.documentElement.scrollWidth > document.documentElement.clientWidth)
    expect(hasHorizontalOverflow).toBe(false)
  })
})
