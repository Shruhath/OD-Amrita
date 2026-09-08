import { selectValue } from '../support/select'
import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import {
  attendanceCollection,
  attendanceRecord,
  teacherMarkReads,
  teacherMarkRoute,
  teacherSearchMarkResult,
} from '../support/factories/teacher-admin-marking'
import { markableCategory, markableEvent } from '../support/factories/marking'

const redTest = test

async function installTeacherAdminRoutes(page: import('@playwright/test').Page, role: 'teacher' | 'admin' = 'teacher') {
  const reads = teacherMarkReads()
  const marks: unknown[] = []

  await installPortalSession(page, role)
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)
    if (url.pathname.endsWith('/auth/me')) {
      return json(route, {
        user_id: '77777777-7777-4777-8777-777777777777',
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        role,
      })
    }
    if (url.pathname.endsWith('/portal/mark/events')) return json(route, reads.events)
    if (url.pathname.includes('/portal/mark/events/') && url.pathname.endsWith('/categories')) return json(route, reads.categories)
    if (url.pathname.endsWith('/students/search')) return json(route, reads.students)
    if (url.pathname.endsWith('/mark') && request.method() === 'POST') {
      marks.push(request.postDataJSON())
      return json(route, teacherSearchMarkResult(4, role), 201)
    }
    if (url.pathname.endsWith('/attendance')) return json(route, attendanceCollection([attendanceRecord({ actor_role: role })]))
    // The admin event-detail page (EventDetailPortal) that hosts the Attendance
    // section also needs the event item, its admin categories, coordinators, and
    // capture-credit reads before it will render at all.
    if (/\/admin\/events\/[^/]+$/.test(url.pathname)) return json(route, markableEvent())
    if (url.pathname.includes('/admin/events/') && url.pathname.endsWith('/categories')) {
      return json(route, { data: [markableCategory()] })
    }
    if (url.pathname.endsWith('/coordinators')) return json(route, { data: [] })
    if (url.pathname.endsWith('/capture-credits')) {
      return json(route, {
        config: { event_id: '11111111-1111-4111-8111-111111111111', limit: 5, updated_by_user_id: null, updated_at: new Date().toISOString() },
        data: [],
      })
    }
    return json(route, { code: 'NOT_FOUND', message: `unmocked ${url.pathname}` }, 404)
  })

  return marks
}

test.describe('teacher/admin search marking', () => {
  redTest('[P0] a teacher search-marks with an explicit slot and no device capture controls', async ({ page }) => {
    const marks = await installTeacherAdminRoutes(page, 'teacher')

    await page.goto(teacherMarkRoute)

    await expect(page.getByRole('heading', { name: /mark attendance/i })).toBeVisible()
    await expect(page.getByRole('button', { name: /tap|nfc/i })).toHaveCount(0)
    await expect(page.getByRole('button', { name: /camera|scan/i })).toHaveCount(0)
    await selectValue(page, page.getByRole('combobox', { name: /slot/i }), '4')
    await page.getByRole('searchbox', { name: /student/i }).fill('CB.SC.U4CSE24')
    await page.getByRole('option', { name: /CB\.SC\.U4CSE/i }).click()
    // Story 0.12b made a reason note mandatory for teacher/admin marks; this
    // pre-existing (0.9) journey never filled one, leaving the submit action
    // permanently disabled. Fixed during code review — CI was already red on
    // this exact failure before the review started.
    await page.getByLabel(/reason note/i).fill('Manual search mark for the journey fixture student.')
    await page.getByRole('button', { name: /mark selected students/i }).click()

    await expect(page.getByRole('status')).toContainText(/marked|present/i)
    expect(marks).toHaveLength(1)
    expect(marks[0]).toMatchObject({ capture_method: 'search', slot: 4 })
  })

  redTest('[P1] admin attendance filters by category and slot from server truth', async ({ page }) => {
    await installTeacherAdminRoutes(page, 'admin')

    await page.goto('/portal/events/11111111-1111-4111-8111-111111111111')

    await expect(page.getByRole('heading', { name: /attendance/i })).toBeVisible()
    await selectValue(page, page.getByRole('combobox', { name: /slot/i }), '4')
    const attendanceTable = page.locator('.table-wrap')
    await expect(attendanceTable.getByText(/CB\.SC\.U4CSE/i)).toBeVisible()
    await expect(attendanceTable.getByRole('cell', { name: 'admin', exact: true })).toBeVisible()
    await expect(attendanceTable.getByRole('cell', { name: 'search', exact: true })).toBeVisible()
  })

  redTest('[P1] a teacher landing on the app root is routed to the record-management surface', async ({ page }) => {
    await installTeacherAdminRoutes(page, 'teacher')

    await page.goto('/')

    // Updated during code review, ratified by Shruhath: teachers primarily
    // view live OD status rather than actively mark, so the default landing
    // page was changed from /portal/mark to /portal/od-records
    // (RoleLanding.tsx). CI was already red on the stale /portal/mark
    // expectation before the review started.
    await expect(page).toHaveURL(/\/portal\/od-records$/)
    await expect(page.getByRole('heading', { name: /access denied/i })).toHaveCount(0)
  })
})
