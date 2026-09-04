import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'
import {
  publicAttendanceRoute,
  publicEmptyRecordCollection,
  publicStudentID,
} from '../support/factories/public-attendance'

/**
 * RED-PHASE SCAFFOLD — Story 0.12b Teacher & Admin OD Record Management.
 *
 * Assumptions intentionally captured until the UI exists and selectors can be
 * verified in a browser snapshot:
 * - teacher add remains at /portal/mark; its reason field is labelled "Reason note";
 * - the new teacher/admin screen is /portal/od-records and exposes labelled
 *   Event, Category, Slot, Section, and Coordinator filters;
 * - each record row is a named row containing Correct and Remove buttons;
 * - correction/removal open labelled dialogs with a required Reason note field.
 *
 * Replace only an assumption proven false by the implemented, accessible UI;
 * preserve role/label-based locators rather than substituting CSS selectors.
 */

const eventID = '93000000-0000-4000-8000-000000000012'
const recordID = '66000000-0000-4000-8000-000000000012'
const studentID = publicStudentID
const categoryID = '94000000-0000-4000-8000-000000000012'

type RecordState = {
  category_id: string
  od_date: string
  reason_note: string
  slot: number
  status: 'present' | 'removed'
}

type SeenRequests = {
  corrections: unknown[]
  manualAdds: unknown[]
  removals: unknown[]
  recordListQueries: URL[]
}

function recordCollection(state: RecordState) {
  return {
    data: [{
      record_id: recordID,
      student: {
        student_id: studentID,
        roll_no: 'CB.SC.U4CSE24101',
        display_name: 'Ananya Rao',
        branch: 'CSE',
        section: 'A',
        batch: '2024',
      },
      event_id: eventID,
      category: { category_id: state.category_id, name: 'Exception Desk' },
      slot: state.slot,
      od_date: state.od_date,
      capture_method: 'search',
      marked_by: { user_id: '77777777-7777-4777-8777-777777777777', email: 'teacher@amrita.edu', name: 'E2E Teacher' },
      actor_role: 'teacher',
      status: state.status,
      reason_note: state.reason_note,
      server_received_at: '2026-08-09T04:00:00Z',
    }],
    page: 1,
    page_size: 25,
    total: 1,
  }
}

async function installRecordManagementRoutes(page: import('@playwright/test').Page) {
  const state: RecordState = {
    category_id: categoryID,
    od_date: '2026-08-09',
    reason_note: 'ID card left at hostel',
    slot: 6,
    status: 'present',
  }
  const seen: SeenRequests = { corrections: [], manualAdds: [], removals: [], recordListQueries: [] }

  await installPortalSession(page, 'teacher')
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)

    if (url.pathname.endsWith('/auth/me')) {
      return json(route, {
        user_id: '77777777-7777-4777-8777-777777777777',
        email: 'teacher@amrita.edu',
        name: 'E2E Teacher',
        role: 'teacher',
      })
    }

    // Existing add reads. Keep the stub narrow so unexpected calls reveal
    // integration drift instead of making an accidental screen pass.
    if (url.pathname.endsWith('/portal/mark/events')) {
      return json(route, { data: [{ id: eventID, name: 'Onam Celebrations', event_date: '2026-08-09', end_date: '2026-08-09' }] })
    }
    if (url.pathname.includes('/portal/mark/events/') && url.pathname.endsWith('/categories')) {
      return json(route, { data: [
        { id: categoryID, event_id: eventID, name: 'Exception Desk' },
        { id: '94000000-0000-4000-8000-000000000099', event_id: eventID, name: 'Volunteer help' },
      ] })
    }
    if (url.pathname.endsWith('/students/search')) {
      return json(route, { data: [{ student_id: studentID, roll_no: 'CB.SC.U4CSE24101', display_name: 'Ananya Rao' }] })
    }
    if (url.pathname.endsWith('/mark') && request.method() === 'POST') {
      seen.manualAdds.push(request.postDataJSON())
      return json(route, {
        record_id: recordID,
        student: { student_id: studentID, roll_no: 'CB.SC.U4CSE24101', display_name: 'Ananya Rao', branch: 'CSE', section: 'A', batch: '2024' },
        event_id: eventID,
        category_id: categoryID,
        slot: 6,
        od_date: '2026-08-09',
        reason_note: state.reason_note,
      }, 201)
    }

    if (url.pathname === `/api/v1/portal/events/${eventID}/records` && request.method() === 'GET') {
      seen.recordListQueries.push(url)
      return json(route, recordCollection(state))
    }
    if (url.pathname === `/api/v1/portal/records/${recordID}/correction` && request.method() === 'POST') {
      const body = request.postDataJSON() as { category_id: string; reason_note: string }
      seen.corrections.push(body)
      state.category_id = body.category_id
      state.reason_note = body.reason_note
      return json(route, { record_id: recordID, ...state })
    }
    if (url.pathname === `/api/v1/portal/records/${recordID}/removal` && request.method() === 'POST') {
      const body = request.postDataJSON() as { reason_note: string }
      seen.removals.push(body)
      state.status = 'removed'
      state.reason_note = body.reason_note
      return json(route, { record_id: recordID, ...state })
    }
    if (url.pathname.includes(`/public/students/${studentID}/od-records`)) {
      // This is the observable public contract: only present rows appear.
      return json(route, state.status === 'removed' ? publicEmptyRecordCollection() : {
        student: { student_id: studentID, roll_no: 'CB.SC.U4CSE24101', display_name: 'Ananya Rao', branch: 'CSE', section: 'A', batch: '2024' },
        days: [{
          od_date: state.od_date,
          records: [{
            record_id: recordID,
            slot: state.slot,
            event_id: eventID,
            event_name: 'Onam Celebrations',
            event_date: '2026-08-09',
            od_date: state.od_date,
            category_id: state.category_id,
            category_name: 'Exception Desk',
            marked_by_name: 'E2E Teacher',
            marked_by_role: 'teacher',
            marked_at: '2026-08-09T04:00:00Z',
          }],
        }],
        generated_at: '2026-08-09T04:00:00Z',
      })
    }

    return json(route, { code: 'NOT_FOUND', message: `unmocked ${request.method()} ${url.pathname}` }, 404)
  })

  return { seen, state }
}

test.describe('teacher/admin OD record management (ATDD red phase)', () => {
  test('[P0] a teacher cannot manually add a search mark without a reason note, then submits one with the note', async ({ page }) => {
    const { seen } = await installRecordManagementRoutes(page)

    await page.goto('/portal/mark')
    await expect(page.getByRole('heading', { name: /mark attendance/i })).toBeVisible()

    // The current search-mark controls are not wrapped in a semantic form. Keep
    // the scope at the page until the record-management form exists, rather than
    // creating an impossible prerequisite for the reason-note assertion.
    const form = page
    await form.getByRole('combobox', { name: /event/i }).click()
    await page.getByRole('option', { name: /Onam Celebrations/i }).click()
    await form.getByRole('combobox', { name: /category/i }).click()
    await page.getByRole('option', { name: /Exception Desk/i }).click()
    await form.getByRole('combobox', { name: /slot/i }).click()
    await page.getByRole('option', { name: '6', exact: true }).click()
    await form.getByRole('searchbox', { name: /student/i }).fill('CB.SC.U4CSE24101')
    await page.getByRole('option', { name: /CB\.SC\.U4CSE24101.*Ananya Rao/i }).click()
    const reasonNote = form.getByLabel(/reason note/i)
    await expect(reasonNote).toBeVisible()
    await reasonNote.fill('')
    await expect(form.getByRole('button', { name: /mark present/i })).toBeDisabled()
    expect(seen.manualAdds).toHaveLength(0)

    await reasonNote.fill('ID card left at hostel')
    const marked = page.waitForResponse((response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/v1/mark') &&
      response.status() === 201,
    )
    await form.getByRole('button', { name: /mark present/i }).click()
    await marked

    expect(seen.manualAdds).toHaveLength(1)
    expect(seen.manualAdds[0]).toMatchObject({ reason_note: 'ID card left at hostel' })
  })

  test('[P1] a teacher filters records, corrects a record, then removes it with auditable reasons', async ({ page }) => {
    const { seen } = await installRecordManagementRoutes(page)

    const initialRecords = page.waitForResponse((response) =>
      response.request().method() === 'GET' &&
      response.url().includes(`/api/v1/portal/events/${eventID}/records`) &&
      response.status() === 200,
    )
    await page.goto(`/portal/od-records?event_id=${eventID}`)
    await initialRecords

    await expect(page.getByRole('heading', { name: /manage records|od records/i })).toBeVisible()
    await page.getByRole('combobox', { name: /section/i }).selectOption('A')
    const filteredRecords = page.waitForResponse((response) =>
      response.request().method() === 'GET' &&
      response.url().includes(`/api/v1/portal/events/${eventID}/records`) &&
      new URL(response.url()).searchParams.get('section') === 'A',
    )
    await page.getByRole('combobox', { name: /coordinator|marked by/i }).selectOption('77777777-7777-4777-8777-777777777777')
    await filteredRecords

    const row = page.getByRole('row', { name: /Ananya Rao.*slot 6.*present/i })
    await expect(row).toBeVisible()

    await row.getByRole('button', { name: /correct/i }).click()
    const correction = page.getByRole('dialog', { name: /correct record/i })
    await correction.getByLabel(/reason note/i).fill('Wrong category selected at capture time')
    await correction.getByRole('combobox', { name: /category/i }).selectOption('94000000-0000-4000-8000-000000000099')
    const corrected = page.waitForResponse((response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith(`/portal/records/${recordID}/correction`) &&
      response.status() === 200,
    )
    await correction.getByRole('button', { name: /save correction|correct/i }).click()
    await corrected

    expect(seen.corrections).toEqual([{
      category_id: '94000000-0000-4000-8000-000000000099',
      reason_note: 'Wrong category selected at capture time',
    }])

    await row.getByRole('button', { name: /remove/i }).click()
    const removal = page.getByRole('dialog', { name: /remove record/i })
    await removal.getByLabel(/reason note/i).fill('Marked in error; student was not present')
    const removed = page.waitForResponse((response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith(`/portal/records/${recordID}/removal`) &&
      response.status() === 200,
    )
    await removal.getByRole('button', { name: /confirm remove|remove record/i }).click()
    await removed

    expect(seen.removals).toEqual([{ reason_note: 'Marked in error; student was not present' }])
    await expect(row).toContainText(/removed/i)
    expect(seen.recordListQueries.some((query) => query.searchParams.get('section') === 'A')).toBe(true)
  })

  test('[P1] a removed record is absent from the public attendance dashboard', async ({ page }) => {
    const { state } = await installRecordManagementRoutes(page)
    // The removal write is seeded at the API boundary for this focused public
    // assertion; the preceding test exercises the portal UI action itself.
    state.status = 'removed'

    const publicRecords = page.waitForResponse((response) =>
      response.request().method() === 'GET' &&
      response.url().includes(`/api/v1/public/students/${studentID}/od-records`) &&
      response.status() === 200,
    )
    await page.goto(`${publicAttendanceRoute}?student=${studentID}`)
    await publicRecords

    await expect(page.getByText(/no OD records/i)).toBeVisible()
    await expect(page.getByText(/Onam Celebrations/i)).toHaveCount(0)
  })
})
