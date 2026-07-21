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
