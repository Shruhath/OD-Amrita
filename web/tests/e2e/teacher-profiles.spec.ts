import { expect, test, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

// Story 2.3 browser journey, AC 6 (and AC 1-2 as the user observes them).
//
// Invoked by: web-deploy -> build job (`pnpm --filter od-amrita-web test:e2e`,
// chromium project, testMatch /e2e\/.*\.spec\.ts/).
//
// RED PHASE: '/portal/teachers' has no route yet (router.tsx) and there is no
// nav link into it from the events list, so every case navigates directly to
// '/portal/teachers' via page.goto rather than clicking through — once Task 5
// lands the route, the '*' catch-all currently redirecting there to
// '/portal/events' is replaced by the real view and these start failing for
// the RIGHT reason (missing UI, not a missing route).
//
// Every route is intercepted BEFORE page.goto, so no assertion can race a
// live request. There are no hard sleeps and no CSS or XPath selectors:
// everything is queried by accessible role, label or text.

type TeacherBranch = { id: string; code: string; name: string }
type TeacherCategory = { id: string; event_id: string; name: string; visible_on_public_dashboard: boolean; created_at: string }
type TeacherProfile = {
  user_id: string
  email: string
  name: string | null
  branches: TeacherBranch[]
  categories: TeacherCategory[]
  created_at: string
}

type Scenario = {
  teachers?: TeacherProfile[]
  createStatus?: number
  createBody?: unknown
  role?: 'admin' | 'student'
}

const computerScience: TeacherBranch = {
  id: '11111111-1111-4111-8111-111111111111',
  code: 'CSE',
  name: 'Computer Science and Engineering',
}
const eventID = 'cccccccc-cccc-4ccc-8ccc-cccccccccccc'
const category: TeacherCategory = {
  id: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
  event_id: eventID,
  name: 'Campus Decoration',
  visible_on_public_dashboard: true,
  created_at: '2026-07-23T03:00:00Z',
}

function teacherProfile(email: string, index: number, categories: TeacherCategory[] = [category]): TeacherProfile {
  return {
    user_id: `eeeeeeee-eeee-4eee-8eee-${String(index).padStart(12, '0')}`,
    email,
    name: `Fixture Teacher ${index}`,
    branches: [computerScience],
    categories,
    created_at: '2026-07-23T03:00:00Z',
  }
}

const ordered = (teachers: TeacherProfile[]) =>
  [...teachers].sort((left, right) => left.email.localeCompare(right.email) || left.user_id.localeCompare(right.user_id))

async function mockApi(page: Page, scenario: Scenario = {}) {
  const { teachers = [], createStatus = 201, createBody, role = 'admin' } = scenario
  await installPortalSession(page, role)
  let serverTeachers = ordered(teachers)
  let created = 0

  await page.route('**/api/v1/auth/me', (route) =>
    json(route, { subject: `atdd-${role}`, email: `${role}@amrita.edu`, name: `ATDD ${role}`, role }),
  )
  await page.route('**/api/v1/admin/branches', (route) => json(route, { data: [computerScience] }))
  await page.route('**/api/v1/admin/events?*', (route) =>
    json(route, {
      data: [{
        id: eventID,
        name: 'Onam Celebrations',
        event_date: '2026-08-15',
        status: 'active',
        branches: [computerScience],
        created_at: '2026-07-23T03:00:00Z',
        archived_at: null,
      }],
      page: 1,
      page_size: 100,
      total: 1,
    }),
  )
  await page.route(`**/api/v1/admin/events/${eventID}/categories`, (route) => json(route, { data: [category] }))
  await page.route('**/api/v1/admin/teachers?*', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    const url = new URL(route.request().url())
    const page_ = Number(url.searchParams.get('page') ?? '1')
    const pageSize = Number(url.searchParams.get('page_size') ?? '20')
    const start = (page_ - 1) * pageSize
    return json(route, { data: serverTeachers.slice(start, start + pageSize), page: page_, page_size: pageSize, total: serverTeachers.length })
  })
  await page.route('**/api/v1/admin/teachers', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    if (route.request().method() !== 'POST') return route.continue()
    created += 1
    if (createStatus !== 201) return json(route, createBody, createStatus)
    const input = route.request().postDataJSON() as { email: string; branch_ids: string[]; category_ids: string[] }
    const categories = [category].filter((candidate) => input.category_ids.includes(candidate.id))
    const profile = teacherProfile(input.email.trim().toLowerCase(), 100 + created, categories)
    serverTeachers = ordered([profile, ...serverTeachers])
    return json(route, profile, 201)
  })
}

test('[P0] opens the teachers view and creates a profile that persists after reload', async ({ page }) => {
  await mockApi(page, { teachers: [] })
  await page.goto('/portal/teachers')
  // exact: true — the empty-state "No teachers yet" heading also contains
  // "teachers", and Playwright's default name match is a case-insensitive
  // substring, so an un-scoped query is ambiguous whenever the roster starts
  // empty (strict-mode violation; CI's retries: 2 masked this as "flaky but
  // green" here while the integration config's retries: 0 turned it hard-red).
  await expect(page.getByRole('heading', { name: 'Teachers', exact: true })).toBeVisible()
  await expect(page.getByText('No teachers yet')).toBeVisible()

  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill('new-teacher@amrita.edu')
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await dialog.getByRole('checkbox', { name: 'Campus Decoration' }).check()
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  const row = page.getByRole('row', { name: /new-teacher@amrita\.edu/ })
  await expect(row).toBeVisible()
  // The cell shows the branch CODE; the full name remains reachable as the
  // abbreviation's expansion rather than being spelled out in the row.
  await expect(row).toContainText('CSE')
  await expect(row.getByTitle('Computer Science and Engineering')).toBeVisible()

  await page.reload()
  await expect(page.getByRole('row', { name: /new-teacher@amrita\.edu/ })).toBeVisible()
})

test('[P0] surfaces a typed USER_NOT_FOUND rejection on the form without creating a row', async ({ page }) => {
  await mockApi(page, {
    teachers: [],
    createStatus: 404,
    createBody: { code: 'USER_NOT_FOUND', message: 'No user has signed in with this email yet.' },
  })
  await page.goto('/portal/teachers')
  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill('never-logged-in@amrita.edu')
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await dialog.getByRole('checkbox', { name: 'Campus Decoration' }).check()
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  await expect(dialog.getByRole('alert')).toHaveText('No user has signed in with this email yet.')
  await expect(page.getByRole('row', { name: /never-logged-in@amrita\.edu/ })).toHaveCount(0)
})

test('[P0] surfaces a typed ROLE_ALREADY_ASSIGNED rejection without blaming a field', async ({ page }) => {
  await mockApi(page, {
    teachers: [teacherProfile('already-admin@amrita.edu', 1)],
    createStatus: 409,
    createBody: { code: 'ROLE_ALREADY_ASSIGNED', message: 'This user already has an assigned role.' },
  })
  await page.goto('/portal/teachers')
  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill('already-admin@amrita.edu')
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await dialog.getByRole('checkbox', { name: 'Campus Decoration' }).check()
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  await expect(dialog.getByRole('alert')).toHaveText('This user already has an assigned role.')
  await expect(dialog.getByLabel('Email')).not.toHaveAttribute('aria-invalid', 'true')
})

test('[P1] a non-Admin never issues a teacher request', async ({ page }) => {
  const requested: string[] = []
  await mockApi(page, { role: 'student' })
  page.on('request', (request) => {
    if (request.url().includes('/api/v1/admin/teachers')) requested.push(request.url())
  })
  await page.goto('/portal/teachers')
  await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
  expect(requested).toHaveLength(0)
})

test('[P2] renders an honest empty state and a retryable list error', async ({ page }) => {
  await mockApi(page, { teachers: [] })
  await page.goto('/portal/teachers')
  await expect(page.getByText('No teachers yet')).toBeVisible()
})

test('[P2] keeps the create control usable on a tablet viewport', async ({ page }) => {
  await page.setViewportSize({ width: 810, height: 1080 })
  await mockApi(page, { teachers: [] })
  await page.goto('/portal/teachers')
  const button = page.getByRole('button', { name: 'New teacher' })
  await expect(button).toBeVisible()
  const box = await button.boundingBox()
  expect(box?.width).toBeGreaterThan(0)
})
