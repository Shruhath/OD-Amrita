import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import type { SessionPort } from '../features/auth/session'
import { ApiFailure, createEventApi } from './client'
import { server } from '../test/server'

function sessionPort(overrides: Partial<SessionPort> = {}): SessionPort {
  return {
    accessToken: vi.fn(async () => 'expired-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
    ...overrides,
  }
}

describe('authorized API client', () => {
  test('requests the selected bounded event page', async () => {
    const session = sessionPort({ accessToken: vi.fn(async () => 'fresh-token') })
    let query = ''
    server.use(
      http.get('*/api/v1/admin/events', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json({ data: [], page: 3, page_size: 20, total: 41 })
      }),
    )

    await expect(createEventApi(session).events(3, 20)).resolves.toMatchObject({ page: 3, total: 41 })
    expect(query).toContain('page=3')
    expect(query).toContain('page_size=20')
  })

  test('caps portal record reads at the management endpoint page-size contract', async () => {
    const session = sessionPort({ accessToken: vi.fn(async () => 'fresh-token') })
    const queries: string[] = []
    server.use(
      http.get('*/api/v1/portal/events/:eventID/records', ({ request }) => {
        queries.push(new URL(request.url).search)
        return HttpResponse.json({ data: [], page: 1, page_size: 100, total: 0 })
      }),
      http.get('*/api/v1/portal/students/:studentID/od-records', ({ request }) => {
        queries.push(new URL(request.url).search)
        return HttpResponse.json({ data: [], page: 1, page_size: 100, total: 0 })
      }),
    )
    const api = createEventApi(session)

    await api.portalRecords('11111111-1111-4111-8111-111111111111', { pageSize: 1000 })
    await api.studentODHistory('55555555-5555-4555-8555-555555555555', 1, 200)

    expect(queries).toEqual(['?page=1&page_size=100', '?page=1&page_size=100'])
  })

  test('requests the admin audit ledger with exact-match filters', async () => {
    const session = sessionPort({ accessToken: vi.fn(async () => 'fresh-token') })
    let query = ''
    server.use(
      http.get('*/api/v1/admin/audit-log', ({ request }) => {
        query = new URL(request.url).search
        return HttpResponse.json({ data: [], page: 2, page_size: 25, total: 51 })
      }),
    )

    await expect(createEventApi(session).adminAuditLog({
      action: 'app_setting.updated',
      actorAccount: 'admin@amrita.edu',
      targetTable: 'app_settings',
      targetID: 'extended_slots_enabled',
      page: 2,
      pageSize: 25,
    })).resolves.toMatchObject({ page: 2, total: 51 })

    expect(query).toContain('action=app_setting.updated')
    expect(query).toContain('actor_account=admin%40amrita.edu')
    expect(query).toContain('target_table=app_settings')
    expect(query).toContain('target_id=extended_slots_enabled')
    expect(query).toContain('page=2')
    expect(query).toContain('page_size=25')
  })

  test('refreshes once after 401 and retries with the new access token', async () => {
    const session = sessionPort()
    const authorizations: string[] = []
    server.use(
      http.get('*/api/v1/auth/me', ({ request }) => {
        const authorization = request.headers.get('authorization') ?? ''
        authorizations.push(authorization)
        if (authorization === 'Bearer expired-token') {
          return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })
        }
        return HttpResponse.json({ subject: 'admin-subject', email: 'admin@amrita.edu', role: 'admin' })
      }),
    )

    await expect(createEventApi(session).identity()).resolves.toEqual({
      subject: 'admin-subject',
      email: 'admin@amrita.edu',
      role: 'admin',
    })
    expect(session.refresh).toHaveBeenCalledTimes(1)
    expect(session.signOut).not.toHaveBeenCalled()
    expect(authorizations).toEqual(['Bearer expired-token', 'Bearer fresh-token'])
  })

  test('signs out after the single retry also returns 401', async () => {
    const session = sessionPort()
    let requests = 0
    server.use(
      http.get('*/api/v1/auth/me', () => {
        requests += 1
        return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Invalid session.' }, { status: 401 })
      }),
    )

    await expect(createEventApi(session).identity()).rejects.toMatchObject({
      status: 401,
      body: { code: 'AUTH_TOKEN_INVALID' },
    })
    expect(requests).toBe(2)
    expect(session.refresh).toHaveBeenCalledTimes(1)
    expect(session.signOut).toHaveBeenCalledTimes(1)
  })

  test('signs out without retry when refresh cannot produce a token', async () => {
    const session = sessionPort({ refresh: vi.fn(async () => null) })
    let requests = 0
    server.use(
      http.get('*/api/v1/auth/me', () => {
        requests += 1
        return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })
      }),
    )

    await expect(createEventApi(session).identity()).rejects.toBeInstanceOf(ApiFailure)
    expect(requests).toBe(1)
    expect(session.refresh).toHaveBeenCalledTimes(1)
    expect(session.signOut).toHaveBeenCalledTimes(1)
  })
})

describe('concurrent 401s', () => {
  test('refreshes ONCE for simultaneous 401s across separate clients', async () => {
    // The real shape of the bug: four screens poll on timers, hold tokens minted
    // at the same moment, and therefore all see their first 401 together. Each
    // used to call session.refresh() on its own — and Supabase ROTATES the
    // refresh token, so the first call invalidated the token the others were
    // still holding. They got "invalid refresh token" back, read it as a dead
    // session, and signed a coordinator out mid-event.
    //
    // Two separate API clients on purpose: the racing callers are different
    // clients built from the same session, so a per-client guard would not have
    // caught this.
    let refreshes = 0
    const session = sessionPort({
      refresh: vi.fn(async () => {
        refreshes += 1
        // A real refresh is a network call. Without the await there is no window
        // for the race, and the test would pass with the guard removed.
        await new Promise((resolve) => setTimeout(resolve, 10))
        return 'fresh-token'
      }),
    })
    server.use(
      http.get('*/api/v1/auth/me', ({ request }) => {
        if ((request.headers.get('authorization') ?? '') === 'Bearer expired-token') {
          return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })
        }
        return HttpResponse.json({ subject: 'admin-subject', email: 'admin@amrita.edu', role: 'admin' })
      }),
    )

    const results = await Promise.all([
      createEventApi(session).identity(),
      createEventApi(session).identity(),
      createEventApi(session).identity(),
      createEventApi(session).identity(),
    ])

    expect(refreshes).toBe(1)
    expect(session.signOut).not.toHaveBeenCalled()
    for (const identity of results) {
      expect(identity).toMatchObject({ subject: 'admin-subject' })
    }
  })

  test('a later 401 starts a NEW refresh once the first has settled', async () => {
    // The shared flight must be a flight, not a latch: clearing it when it
    // settles is what keeps the next genuine expiry refreshable.
    const session = sessionPort()
    server.use(
      http.get('*/api/v1/auth/me', ({ request }) => {
        if ((request.headers.get('authorization') ?? '') === 'Bearer expired-token') {
          return HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })
        }
        return HttpResponse.json({ subject: 'admin-subject', email: 'admin@amrita.edu', role: 'admin' })
      }),
    )

    await createEventApi(session).identity()
    await createEventApi(session).identity()

    expect(session.refresh).toHaveBeenCalledTimes(2)
  })
})

describe('failure correlation', () => {
  test('carries the server request id off a 500', async () => {
    // The whole reason the id exists. An admin reporting "Internal server error"
    // could give us nothing to search for; the API logs the id it puts in this
    // header, so carrying it here is what makes the report findable.
    const session = sessionPort({ accessToken: vi.fn(async () => 'fresh-token') })
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json(
          { code: 'INTERNAL', message: 'Internal server error.' },
          { status: 500, headers: { 'X-Request-Id': '01M1B7MN2H272C84WNN7E06110-fra' } },
        ),
      ),
    )

    await expect(createEventApi(session).identity()).rejects.toMatchObject({
      status: 500,
      requestId: '01M1B7MN2H272C84WNN7E06110-fra',
    })
  })

  test('is empty rather than undefined when the header is absent', async () => {
    // A failure that never reached the API — a proxy, a tunnel, an unmatched
    // route — has no id, and every reader of this field renders it directly.
    const session = sessionPort({ accessToken: vi.fn(async () => 'fresh-token') })
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ code: 'INTERNAL', message: 'Internal server error.' }, { status: 500 }),
      ),
    )

    await expect(createEventApi(session).identity()).rejects.toMatchObject({ requestId: '' })
  })
})
