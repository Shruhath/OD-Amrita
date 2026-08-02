import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import type { SessionPort } from '../features/auth/session'
import { ApiFailure, createEventApi } from './client'
import { server } from '../test/server'
import {
  cardPayload,
  cardPopulatorBranch,
  cardPopulatorStudent,
  sealResponse,
  studentCollection,
} from '../../tests/support/factories/card-populator'

function sessionPort(overrides: Partial<SessionPort> = {}): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
    ...overrides,
  }
}

describe('card populator API client', () => {
  test('[P0] lists database students with bounded section filters', async () => {
    const student = cardPopulatorStudent(1, { display_name: 'Ananya Rao' })
    let query = ''
    server.use(
      http.get('*/api/v1/admin/students', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json(studentCollection([student]))
      }),
    )

    const result = await createEventApi(sessionPort()).students({
      branchID: cardPopulatorBranch.id,
      batch: '2024',
      section: 'A',
      page: 1,
      pageSize: 100,
    })

    expect(query).toContain(`branch_id=${cardPopulatorBranch.id}`)
    expect(query).toContain('batch=2024')
    expect(query).toContain('section=A')
    expect(query).toContain('page_size=100')
    expect(result.data.map((row) => row.roll_no)).toEqual(['CB.SC.U4CSE24001'])
  })

  // An unset filter must be ABSENT from the query, not sent empty. `section=`
  // is a filter for the empty string, which no row can match — the roster would
  // come back empty and look like a correctly-empty section.
  test('[P0] omits unset filters instead of sending them blank', async () => {
    let query = ''
    server.use(
      http.get('*/api/v1/admin/students', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json(studentCollection([]))
      }),
    )

    await createEventApi(sessionPort()).students({ branchID: cardPopulatorBranch.id })

    expect(query).toContain(`branch_id=${cardPopulatorBranch.id}`)
    expect(query).not.toContain('section=')
    expect(query).not.toContain('batch=')
  })

  test('[P0] defaults to the first page at the section-sized page size', async () => {
    let query = ''
    server.use(
      http.get('*/api/v1/admin/students', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json(studentCollection([]))
      }),
    )

    await createEventApi(sessionPort()).students()

    expect(query).toContain('page=1')
    expect(query).toContain('page_size=100')
  })

  test('[P0] seals one selected student and returns only the server payload for NFC writes', async () => {
    const student = cardPopulatorStudent(2)
    const payload = cardPayload()
    server.use(
      http.post(`*/api/v1/admin/students/${student.student_id}/card-seal`, () =>
        HttpResponse.json(sealResponse(student, payload), { status: 201 }),
      ),
    )

    const sealed = await createEventApi(sessionPort()).sealStudentCard(student.student_id)

    expect(sealed.payload).toEqual(payload)
    expect(JSON.stringify(sealed.payload)).not.toMatch(/roll_no|display_name|student_id|branch|section|batch|email|key/i)
    expect(Object.keys(sealed.payload).sort()).toEqual(['token', 'version'])
  })

  test('[P0] resolves a read token through the server and inherits the single 401 refresh retry', async () => {
    const student = cardPopulatorStudent(3, { display_name: 'Madhav Rao' })
    const session = sessionPort({ accessToken: vi.fn(async () => 'expired-token') })
    const seenAuth: string[] = []
    server.use(
      http.post('*/api/v1/admin/card-tokens/resolve', ({ request }) => {
        const authorization = request.headers.get('authorization') ?? ''
        seenAuth.push(authorization)
        if (authorization === 'Bearer expired-token') {
          return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })
        }
        return HttpResponse.json(student)
      }),
    )

    await expect(createEventApi(session).resolveCardToken(cardPayload())).resolves.toMatchObject({
      student_id: student.student_id,
      roll_no: student.roll_no,
    })
    expect(seenAuth).toEqual(['Bearer expired-token', 'Bearer fresh-token'])
    expect(session.refresh).toHaveBeenCalledTimes(1)
  })

  test('[P0] sends the read token verbatim to the server rather than decoding it locally', async () => {
    const payload = cardPayload()
    let received: unknown
    server.use(
      http.post('*/api/v1/admin/card-tokens/resolve', async ({ request }) => {
        received = await request.json()
        return HttpResponse.json(cardPopulatorStudent(4))
      }),
    )

    await createEventApi(sessionPort()).resolveCardToken(payload)

    expect(received).toEqual({ version: payload.version, token: payload.token })
  })

  // An unrecognised card is a 422 the UI must be able to distinguish from a
  // transport failure, so the typed envelope has to survive as an ApiFailure.
  test('[P0] surfaces an unrecognized card as a typed failure', async () => {
    server.use(
      http.post('*/api/v1/admin/card-tokens/resolve', () =>
        HttpResponse.json({ code: 'CARD_NOT_RECOGNIZED', message: 'Card was not recognized.' }, { status: 422 }),
      ),
    )

    const failure = await createEventApi(sessionPort())
      .resolveCardToken(cardPayload())
      .catch((error: unknown) => error)

    expect(failure).toBeInstanceOf(ApiFailure)
    expect(failure).toMatchObject({ status: 422, body: { code: 'CARD_NOT_RECOGNIZED' } })
  })

  test('[P0] surfaces a non-admin roster read as a typed 403', async () => {
    server.use(
      http.get('*/api/v1/admin/students', () =>
        HttpResponse.json({ code: 'RBAC_FORBIDDEN', message: 'Administrator access is required.' }, { status: 403 }),
      ),
    )

    const failure = await createEventApi(sessionPort())
      .students({ branchID: cardPopulatorBranch.id })
      .catch((error: unknown) => error)

    expect(failure).toBeInstanceOf(ApiFailure)
    expect(failure).toMatchObject({ status: 403, body: { code: 'RBAC_FORBIDDEN' } })
  })
})
