import { expect, test, type APIResponse } from '@playwright/test'

/**
 * ATDD RED PHASE — Story 0.12b teacher/admin OD record management.
 *
 * These request-level acceptance scaffolds remain skipped until the five portal
 * routes and their portal-harness fixtures exist. They deliberately call the
 * API directly: browser-flow coverage belongs in the separate E2E scaffold.
 *
 * Required portal-harness setup:
 * - teacher token belongs only to BRANCH_CSE; admin is global; student is denied.
 * - MANAGED_EVENT_ID spans 2026-08-10..2026-09-08 and is active.
 * - each fixture record exists before its named test and is isolated by record id.
 * - CROSS_BRANCH_* rows belong to BRANCH_ECE, never to the teacher's CSE branch.
 * - REMOVABLE_RECORD_ID starts present and has a free re-mark path after removal.
 * - DUPLICATE_TARGET_RECORD_ID starts present in the target tuple.
 */
const apiBaseURL = process.env.PORTAL_API_BASE_URL ?? 'http://127.0.0.1:8081'
const teacherToken = 'portal-teacher-token'
const adminToken = 'portal-admin-token'
const studentToken = 'portal-student-token'

const MANAGED_EVENT_ID = '120b0000-0000-4000-8000-000000000001'
const CSE_CATEGORY_ID = '120b0000-0000-4000-8000-000000000002'
const ECE_CATEGORY_ID = '120b0000-0000-4000-8000-000000000003'
const CSE_STUDENT_ID = '120b0000-0000-4000-8000-000000000004'
const ECE_STUDENT_ID = '120b0000-0000-4000-8000-000000000005'
const COORDINATOR_USER_ID = '120b0000-0000-4000-8000-000000000006'
const CORRECTABLE_RECORD_ID = '120b0000-0000-4000-8000-000000000007'
const REMOVABLE_RECORD_ID = '120b0000-0000-4000-8000-000000000008'
const REMOVED_RECORD_ID = '120b0000-0000-4000-8000-000000000009'
const CROSS_BRANCH_RECORD_ID = '120b0000-0000-4000-8000-000000000010'
const DUPLICATE_TARGET_RECORD_ID = '120b0000-0000-4000-8000-000000000011'
const UNKNOWN_RECORD_ID = '120b0000-0000-4000-8000-000000000012'
const IN_RANGE_OD_DATE = '2026-08-19'
const OUT_OF_RANGE_OD_DATE = '2026-10-01'

type ErrorBody = {
  code: string
  message: string
  details?: Array<{ field: string; code: string; message: string }>
}

function headers(token: string) {
  return { Authorization: 'Bearer ' + token }
}

async function expectError(response: APIResponse, status: number, code: string) {
  expect(response.status(), await response.text()).toBe(status)
  const body = (await response.json()) as ErrorBody
  expect(body).toMatchObject({ code, message: expect.any(String) })
}

test.describe('Teacher and admin OD record management API (ATDD red phase)', () => {
  test.skip('[P0] teacher lists only its branch records with event, category, slot, day, section, and coordinator filters', async ({ request }) => {
    const response = await request.get(
      apiBaseURL + '/api/v1/portal/events/' + MANAGED_EVENT_ID + '/records?category_id=' + CSE_CATEGORY_ID + '&slot=6&od_date=' + IN_RANGE_OD_DATE + '&section=A&actor_user_id=' + COORDINATOR_USER_ID + '&page=1&page_size=20',
      { headers: headers(teacherToken) },
    )

    expect(response.status(), await response.text()).toBe(200)
    const body = await response.json() as {
      data: Array<{
        record_id: string
        event_id: string
        slot: number
        od_date: string
        status: 'present' | 'removed'
        reason_note: string | null
        student: { student_id: string; branch: string; section: string }
        marked_by: { user_id: string | null }
      }>
      page: number
      page_size: number
      total: number
    }
    expect(body).toMatchObject({ page: 1, page_size: 20, total: expect.any(Number) })
    expect(body.data).toEqual(expect.arrayContaining([
      expect.objectContaining({
        event_id: MANAGED_EVENT_ID,
        slot: 6,
        od_date: IN_RANGE_OD_DATE,
        status: expect.stringMatching(/^(present|removed)$/),
        student: expect.objectContaining({ branch: 'CSE', section: 'A' }),
        marked_by: expect.objectContaining({ user_id: COORDINATOR_USER_ID }),
      }),
    ]))
    expect(body.data.some((record) => record.student.branch === 'ECE')).toBeFalsy()
  })

  test.skip('[P0] admin lists filtered records across branches and includes removed records with nullable historical reasons', async ({ request }) => {
    const response = await request.get(
      apiBaseURL + '/api/v1/portal/events/' + MANAGED_EVENT_ID + '/records?od_date=' + IN_RANGE_OD_DATE + '&page=1&page_size=100',
      { headers: headers(adminToken) },
    )

    expect(response.status(), await response.text()).toBe(200)
    const body = await response.json() as {
      data: Array<{
        record_id: string
        status: 'present' | 'removed'
        reason_note: string | null
        student: { branch: string }
      }>
    }
    expect(body.data).toEqual(expect.arrayContaining([
      expect.objectContaining({ record_id: REMOVED_RECORD_ID, status: 'removed', reason_note: expect.any(String) }),
      expect.objectContaining({ student: expect.objectContaining({ branch: 'ECE' }) }),
    ]))
    expect(body.data.every((record) => record.status === 'present' || record.status === 'removed')).toBeTruthy()
  })

  test.skip('[P0] teacher reads complete OD history for an in-branch student, including removed rows and actor identity', async ({ request }) => {
    const response = await request.get(
      apiBaseURL + '/api/v1/portal/students/' + CSE_STUDENT_ID + '/od-records?page=1&page_size=100',
      { headers: headers(teacherToken) },
    )

    expect(response.status(), await response.text()).toBe(200)
    const body = await response.json() as {
      data: Array<{
        record_id: string
        student: { student_id: string }
        status: 'present' | 'removed'
        reason_note: string | null
        marked_by: { user_id: string | null; email: string | null; name: string | null }
      }>
      page: number
      page_size: number
      total: number
    }
    expect(body).toMatchObject({ page: 1, page_size: 100, total: expect.any(Number) })
    expect(body.data).toEqual(expect.arrayContaining([
      expect.objectContaining({
        student: expect.objectContaining({ student_id: CSE_STUDENT_ID }),
        status: expect.stringMatching(/^(present|removed)$/),
        marked_by: expect.objectContaining({
          user_id: expect.anything(),
          email: expect.any(String),
          name: expect.any(String),
        }),
      }),
    ]))
  })

  test.skip('[P0] admin corrects slot, category, and OD day in place with a mandatory reason and a stable record id', async ({ request }) => {
    const response = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/correction',
      {
        headers: headers(adminToken),
        data: {
          slot: 6,
          category_id: ECE_CATEGORY_ID,
          od_date: IN_RANGE_OD_DATE,
          reason_note: 'Wrong category selected at capture time; corrected after register verification.',
        },
      },
    )

    expect(response.status(), await response.text()).toBe(200)
    const body = await response.json() as {
      record_id: string
      category: { id: string }
      slot: number
      od_date: string
      status: string
      reason_note: string | null
      actor_role: string
      marked_by: { user_id: string | null }
    }
    expect(body).toMatchObject({
      record_id: CORRECTABLE_RECORD_ID,
      category: { id: ECE_CATEGORY_ID },
      slot: 6,
      od_date: IN_RANGE_OD_DATE,
      status: 'present',
      reason_note: 'Wrong category selected at capture time; corrected after register verification.',
      actor_role: 'admin',
      marked_by: { user_id: expect.any(String) },
    })
  })

  test.skip('[P0] correction rejects missing reasons, no-op payloads, duplicate slots, out-of-range days, archived events, and terminal rows with typed errors', async ({ request }) => {
    const cases: Array<{
      name: string
      recordID: string
      data: Record<string, unknown>
      status: number
      code: string
    }> = [
      {
        name: 'missing reason note',
        recordID: CORRECTABLE_RECORD_ID,
        data: { slot: 5, reason_note: '' },
        status: 400,
        code: 'VALIDATION_FAILED',
      },
      {
        name: 'no effective field change',
        recordID: CORRECTABLE_RECORD_ID,
        data: { slot: 4, reason_note: 'Re-entered the same slot after checking the register.' },
        status: 400,
        code: 'VALIDATION_FAILED',
      },
      {
        name: 'present-row duplicate',
        recordID: CORRECTABLE_RECORD_ID,
        data: { slot: 6, category_id: CSE_CATEGORY_ID, od_date: IN_RANGE_OD_DATE, reason_note: 'Move to the verified duplicate target.' },
        status: 409,
        code: 'SCAN_DUPLICATE',
      },
      {
        name: 'event-range violation',
        recordID: CORRECTABLE_RECORD_ID,
        data: { od_date: OUT_OF_RANGE_OD_DATE, reason_note: 'Date is intentionally outside the event for validation.' },
        status: 400,
        code: 'VALIDATION_FAILED',
      },
      {
        name: 'already removed row',
        recordID: REMOVED_RECORD_ID,
        data: { slot: 5, reason_note: 'This must not revive a removed record.' },
        status: 409,
        code: 'OD_RECORD_ALREADY_REMOVED',
      },
    ]

    for (const scenario of cases) {
      const response = await request.post(
        apiBaseURL + '/api/v1/portal/records/' + scenario.recordID + '/correction',
        { headers: headers(adminToken), data: scenario.data },
      )
      await expectError(response, scenario.status, scenario.code)
    }

    const archivedResponse = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + CROSS_BRANCH_RECORD_ID + '/correction',
      { headers: headers(adminToken), data: { slot: 7, reason_note: 'Attempted correction after event archival.' } },
    )
    await expectError(archivedResponse, 409, 'EVENT_ARCHIVED')
  })

  test.skip('[P0] removal soft-removes a present record, writes a terminal reason, and exposes both lifecycle audit entries', async ({ request }) => {
    const removal = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + REMOVABLE_RECORD_ID + '/removal',
      {
        headers: headers(adminToken),
        data: { reason_note: 'Duplicate paper register entry; remove before re-marking the verified student.' },
      },
    )

    expect(removal.status(), await removal.text()).toBe(200)
    await expect(removal.json()).resolves.toMatchObject({
      record_id: REMOVABLE_RECORD_ID,
      status: 'removed',
      reason_note: 'Duplicate paper register entry; remove before re-marking the verified student.',
      actor_role: 'admin',
    })

    const audit = await request.get(
      apiBaseURL + '/api/v1/portal/records/' + REMOVABLE_RECORD_ID + '/audit?page=1&page_size=100',
      { headers: headers(adminToken) },
    )
    expect(audit.status(), await audit.text()).toBe(200)
    await expect(audit.json()).resolves.toMatchObject({
      data: expect.arrayContaining([
        expect.objectContaining({ action: 'od.created', payload: expect.any(Object) }),
        expect.objectContaining({
          action: 'od.removed',
          payload: expect.objectContaining({
            record_id: REMOVABLE_RECORD_ID,
            reason_note: 'Duplicate paper register entry; remove before re-marking the verified student.',
            previous: expect.any(Object),
          }),
        }),
      ]),
    })
  })

  test.skip('[P0] removal rejects an empty reason, a missing id, and a second removal with canonical error bodies', async ({ request }) => {
    const emptyReason = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + REMOVABLE_RECORD_ID + '/removal',
      { headers: headers(adminToken), data: { reason_note: '' } },
    )
    await expectError(emptyReason, 400, 'VALIDATION_FAILED')

    const unknown = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + UNKNOWN_RECORD_ID + '/removal',
      { headers: headers(adminToken), data: { reason_note: 'This UUID is deliberately absent from the fixture database.' } },
    )
    await expectError(unknown, 404, 'OD_RECORD_NOT_FOUND')

    const repeated = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + REMOVED_RECORD_ID + '/removal',
      { headers: headers(adminToken), data: { reason_note: 'A terminal row must not be removed twice.' } },
    )
    await expectError(repeated, 409, 'OD_RECORD_ALREADY_REMOVED')
  })

  test.skip('[P0] teacher is denied cross-branch correction, removal, student history, and record audit without leaking the record', async ({ request }) => {
    const correction = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + CROSS_BRANCH_RECORD_ID + '/correction',
      { headers: headers(teacherToken), data: { slot: 6, reason_note: 'Teacher must not alter another branch.' } },
    )
    await expectError(correction, 403, 'RBAC_FORBIDDEN')

    const removal = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + CROSS_BRANCH_RECORD_ID + '/removal',
      { headers: headers(teacherToken), data: { reason_note: 'Teacher must not remove another branch record.' } },
    )
    await expectError(removal, 403, 'RBAC_FORBIDDEN')

    const history = await request.get(
      apiBaseURL + '/api/v1/portal/students/' + ECE_STUDENT_ID + '/od-records',
      { headers: headers(teacherToken) },
    )
    await expectError(history, 403, 'RBAC_FORBIDDEN')

    const audit = await request.get(
      apiBaseURL + '/api/v1/portal/records/' + CROSS_BRANCH_RECORD_ID + '/audit',
      { headers: headers(teacherToken) },
    )
    await expectError(audit, 403, 'RBAC_FORBIDDEN')
  })

  test.skip('[P0] audit read returns chronological previous/new snapshots after correction without exposing a different record audit trail', async ({ request }) => {
    const response = await request.get(
      apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/audit?page=1&page_size=100',
      { headers: headers(adminToken) },
    )

    expect(response.status(), await response.text()).toBe(200)
    const body = await response.json() as {
      data: Array<{
        action: string
        actor_email: string | null
        actor_role: string | null
        occurred_at: string
        payload: Record<string, unknown>
      }>
    }
    expect(body.data).toEqual(expect.arrayContaining([
      expect.objectContaining({
        action: 'od.overridden',
        actor_email: expect.any(String),
        actor_role: 'admin',
        occurred_at: expect.stringMatching(/^\d{4}-\d{2}-\d{2}T/),
        payload: expect.objectContaining({
          record_id: CORRECTABLE_RECORD_ID,
          reason_note: expect.any(String),
          previous: expect.objectContaining({ slot: expect.any(Number), category_id: expect.any(String), od_date: expect.any(String) }),
          new: expect.objectContaining({ slot: expect.any(Number), category_id: expect.any(String), od_date: expect.any(String) }),
        }),
      }),
    ]))
    expect(body.data.every((entry) => entry.payload.record_id === CORRECTABLE_RECORD_ID)).toBeTruthy()
  })

  test.skip('[P1] all five new portal paths require an authenticated principal and return the canonical authentication error', async ({ request }) => {
    const unauthenticatedRequests = [
      request.get(apiBaseURL + '/api/v1/portal/events/' + MANAGED_EVENT_ID + '/records'),
      request.get(apiBaseURL + '/api/v1/portal/students/' + CSE_STUDENT_ID + '/od-records'),
      request.post(apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/correction', {
        data: { slot: 6, reason_note: 'No token must be rejected before this correction is considered.' },
      }),
      request.post(apiBaseURL + '/api/v1/portal/records/' + REMOVABLE_RECORD_ID + '/removal', {
        data: { reason_note: 'No token must be rejected before this removal is considered.' },
      }),
      request.get(apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/audit'),
    ]

    for (const response of await Promise.all(unauthenticatedRequests)) {
      await expectError(response, 401, 'AUTH_TOKEN_INVALID')
    }
  })

  test.skip('[P1] student principals are rejected from every new portal record-management path', async ({ request }) => {
    const list = await request.get(
      apiBaseURL + '/api/v1/portal/events/' + MANAGED_EVENT_ID + '/records',
      { headers: headers(studentToken) },
    )
    await expectError(list, 403, 'RBAC_FORBIDDEN')

    const history = await request.get(
      apiBaseURL + '/api/v1/portal/students/' + CSE_STUDENT_ID + '/od-records',
      { headers: headers(studentToken) },
    )
    await expectError(history, 403, 'RBAC_FORBIDDEN')

    const correction = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/correction',
      { headers: headers(studentToken), data: { slot: 6, reason_note: 'Student must not alter OD records.' } },
    )
    await expectError(correction, 403, 'RBAC_FORBIDDEN')

    const removal = await request.post(
      apiBaseURL + '/api/v1/portal/records/' + REMOVABLE_RECORD_ID + '/removal',
      { headers: headers(studentToken), data: { reason_note: 'Student must not remove OD records.' } },
    )
    await expectError(removal, 403, 'RBAC_FORBIDDEN')

    const audit = await request.get(
      apiBaseURL + '/api/v1/portal/records/' + CORRECTABLE_RECORD_ID + '/audit',
      { headers: headers(studentToken) },
    )
    await expectError(audit, 403, 'RBAC_FORBIDDEN')
  })
})


