import { expect, test, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

type CoordinatorRecord = {
  id: string
  user_id: string
  email: string
  name: string | null
  event_id: string
  categories: CoordinatorCategory[]
  created_at: string
}

type CoordinatorCategory = {
  id: string
  name: string
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
  coordinators?: CoordinatorRecord[]
  createStatus?: number
  createBody?: unknown
  role?: 'admin' | 'student'
}

const branch: Branch = {
  id: '11111111-1111-4111-8111-111111111111',
  code: 'CSE',
  name: 'Computer Science and Engineering',
}

const eventID = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc'
const eventName = 'Onam Celebrations'
const processionCategory: CoordinatorCategory = {
  id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  name: 'Procession',
}

function eventRecord(status: 'active' | 'archived'): EventRecord {
  return {
    id: eventID,
    name: eventName,
    event_date: '2026-08-15',
    status,
    branches: [branch],
    created_at: '2026-08-03T03:00:00Z',
    archived_at: status === 'archived' ? '2026-08-03T04:00:00Z' : null,
  }
}

function coordinatorRecord(email: string, index: number, categories: CoordinatorCategory[] = [processionCategory]): CoordinatorRecord {
  return {
    id: `dddddddd-dddd-4ddd-8ddd-${String(index).padStart(12, '0')}`,
    user_id: `eeeeeeee-eeee-4eee-8eee-${String(index).padStart(12, '0')}`,
    email,
    name: email.split('@')[0],
    event_id: eventID,
    categories,
    created_at: `2026-08-03T03:${String(index).padStart(2, '0')}:00Z`,
  }
}

async function mockApi(page: Page, scenario: Scenario = {}) {
  const { status = 'active', coordinators = [], createStatus = 201, createBody, role = 'admin' } = scenario
  await installPortalSession(page, role)
  let serverCoordinators = [...coordinators]
  let created = 100

  await page.route('**/api/v1/auth/me', (route) =>
    json(route, {
      subject: `assignment-${role}`,
      email: `${role}@amrita.edu`,
      name: `Assignment ${role}`,
      role,
    }),
  )
  await page.route('**/api/v1/admin/branches', (route) => json(route, { data: [branch] }))
  await page.route('**/api/v1/admin/events?*', (route) =>
    json(route, { data: [eventRecord(status)], page: 1, page_size: 20, total: 1 }),
  )
  await page.route('**/api/v1/admin/events', (route) =>
    json(route, { data: [eventRecord(status)], page: 1, page_size: 20, total: 1 }),
  )
  await page.route(`**/api/v1/admin/events/${eventID}`, (route) => json(route, eventRecord(status)))
  await page.route(`**/api/v1/admin/events/${eventID}/capture-credits`, async (route) => {
    if (route.request().method() === 'OPTIONS') {
      await fulfilPreflight(route)
      return
    }
    await json(route, {
      config: {
        event_id: eventID,
        limit: 5,
        updated_by_user_id: null,
        updated_at: '2026-08-03T03:00:00Z',
      },
      data: [],
    })
  })
  await page.route(`**/api/v1/admin/events/${eventID}/categories`, (route) =>
    json(route, {
      data: [
        {
          id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
          event_id: eventID,
          name: 'Procession',
          visible_on_public_dashboard: true,
          created_at: '2026-08-03T03:00:00Z',
        },
      ],
    }),
  )
  await page.route(`**/api/v1/admin/events/${eventID}/attendance*`, async (route) => {
    if (route.request().method() === 'OPTIONS') {
      await fulfilPreflight(route)
      return
    }
    await json(route, { data: [], page: 1, page_size: 20, total: 0 })
  })
  await page.route(`**/api/v1/admin/events/${eventID}/coordinators`, async (route) => {
    if (route.request().method() === 'OPTIONS') {
      await fulfilPreflight(route)
      return
    }
    if (route.request().method() === 'POST') {
      if (createStatus !== 201) {
        await json(route, createBody, createStatus)
        return
      }
      const input = route.request().postDataJSON() as { email: string; category_ids: string[] }
      const categories = [processionCategory].filter((category) => input.category_ids.includes(category.id))
      const record = coordinatorRecord(input.email.trim().toLowerCase(), ++created, categories)
      serverCoordinators = [...serverCoordinators, record]
      await json(route, record, 201)
      return
    }
    await json(route, { data: serverCoordinators })
  })
}

test.describe('Admin event coordinators journey', () => {
  test('[P0] assigns a coordinator and persists the row across reload', async ({ page }) => {
    await mockApi(page, { coordinators: [coordinatorRecord('existing@amrita.edu', 1)] })
    await page.goto('/portal/events')

    await page.getByRole('link', { name: eventName }).click()
    await expect(page.getByRole('heading', { level: 1, name: eventName })).toBeVisible()

    const list = page.getByRole('list', { name: 'Coordinators' })
    await expect(list.getByText('existing@amrita.edu')).toBeVisible()

    await page.getByLabel('Coordinator email').fill('candidate@amrita.edu')
    await page.getByRole('checkbox', { name: 'Procession' }).check()
    await page.getByRole('button', { name: 'Assign coordinator' }).click()
    await expect(list.getByText('candidate@amrita.edu')).toBeVisible()

    await page.reload()
    await expect(page.getByRole('list', { name: 'Coordinators' }).getByText('candidate@amrita.edu')).toBeVisible()
  })

  test('[P0] surfaces duplicate assignment as a state conflict and adds nothing', async ({ page }) => {
    await mockApi(page, {
      coordinators: [coordinatorRecord('existing@amrita.edu', 1)],
      createStatus: 409,
      createBody: {
        code: 'COORDINATOR_ASSIGNMENT_EXISTS',
        message: 'This coordinator is already assigned to this event.',
      },
    })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    await page.getByLabel('Coordinator email').fill('existing@amrita.edu')
    await page.getByRole('checkbox', { name: 'Procession' }).check()
    await page.getByRole('button', { name: 'Assign coordinator' }).click()

    await expect(page.getByRole('alert')).toHaveText(/already assigned/i)
    await expect(page.getByRole('list', { name: 'Coordinators' }).getByRole('listitem')).toHaveCount(1)
  })

  test('[P1] keeps archived events readable without an add affordance', async ({ page }) => {
    await mockApi(page, { status: 'archived', coordinators: [coordinatorRecord('kept@amrita.edu', 2)] })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    await expect(page.getByRole('list', { name: 'Coordinators' }).getByText('kept@amrita.edu')).toBeVisible()
    await expect(page.getByLabel('Coordinator email')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Assign coordinator' })).toHaveCount(0)
  })

  test('[P1] never issues coordinator assignment requests for a non-admin caller', async ({ page }) => {
    const coordinatorRequests: string[] = []
    page.on('request', (request) => {
      if (new URL(request.url()).pathname.includes('/coordinators')) coordinatorRequests.push(request.url())
    })
    await mockApi(page, { role: 'student' })
    await page.goto(`/portal/events/${eventID}`)

    await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
    expect(coordinatorRequests).toEqual([])
  })

  test('[P2] keeps the detail surface usable at a tablet viewport', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await mockApi(page, { coordinators: [coordinatorRecord('tablet@amrita.edu', 3)] })
    await page.goto('/portal/events')
    await page.getByRole('link', { name: eventName }).click()

    const submit = page.getByRole('button', { name: 'Assign coordinator' })
    await expect(submit).toBeVisible()
    const box = await submit.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x + box!.width).toBeLessThanOrEqual(768)
  })
})
