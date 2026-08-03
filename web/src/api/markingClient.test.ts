import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { createEventApi } from './client'
import type { SessionPort } from '../features/auth/session'
import { server } from '../test/server'
import {
  collection,
  dataOnly,
  markCategoryID,
  markEventID,
  markStatusOpen,
  markStudent,
  markableCategory,
  markableEvent,
} from '../../tests/support/factories/marking'

/**
 * The typed client methods for the coordinator marking surface.
 *
 * `web/src/api/client.ts` owns bearer injection, the single 401-refresh retry,
 * the ngrok header and `ApiFailure`. Every new call must go through it — a
 * component that reaches for `fetch` directly loses all four.
 *
 * THE RULE THESE PIN: the coordinator surface calls its OWN scoped endpoints and
 * never an `/admin/` one. `/api/v1/admin/students` and
 * `/api/v1/admin/card-tokens/resolve` are admin-gated by design; calling them
 * from `/mark` produces a 403 that looks like a bug in the marking screen.
 */

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

function markingApi(session: SessionPort = sessionPort()) {
  return createEventApi(session)
}

describe('scoped marking client methods', () => {
  test('[P0] the scoped event and category reads hit the non-admin paths', async () => {
    const requested: string[] = []
    server.use(
      http.get('*/api/v1/events', ({ request }) => {
        requested.push(new URL(request.url).pathname)
        return HttpResponse.json(collection([markableEvent()]))
      }),
      http.get('*/api/v1/events/:eventID/categories', ({ request }) => {
        requested.push(new URL(request.url).pathname)
        return HttpResponse.json(dataOnly([markableCategory()]))
      }),
    )

    const api = markingApi()
    const events = await api.markableEvents()
    const categories = await api.markableCategories(markEventID)

    expect(events.data).toHaveLength(1)
    expect(categories.data).toHaveLength(1)
    for (const path of requested) {
      expect(path, 'the coordinator surface must never call an /admin/ endpoint').not.toContain('/admin/')
    }
  })

  test('[P0] search sends the trimmed query and the server bounds the rest', async () => {
    let seen: URL | null = null
    server.use(
      http.get('*/api/v1/students/search', ({ request }) => {
        seen = new URL(request.url)
        return HttpResponse.json(dataOnly([markStudent(1)]))
      }),
    )

    const api = markingApi()
    const result = await api.searchStudents('  CB.SC.U4CSE24  ')

    expect(result.data).toHaveLength(1)
    expect(seen!.pathname).toBe('/api/v1/students/search')
    expect(seen!.searchParams.get('q')).toBe('CB.SC.U4CSE24')
    expect(seen!.pathname).not.toContain('/admin/')
  })

  test('[P0] the search response carries no email or token field', async () => {
    server.use(
      http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([markStudent(1)]))),
    )

    const result = await markingApi().searchStudents('CB.SC.U4CSE24')
    const serialized = JSON.stringify(result)

    // `students` has no email column and card tokens are stored as fingerprints
    // only. Both facts must survive this new read path (AR20/NFR16).
    expect(serialized).not.toContain('email')
    expect(serialized).not.toContain('token')
    expect(serialized).not.toContain('fingerprint')
  })

  test('[P0] mark status returns the server clock, window and both counters', async () => {
    const status = markStatusOpen()
    server.use(http.get('*/api/v1/mark/status', () => HttpResponse.json(status)))

    const result = await markingApi().markStatus(markEventID, markCategoryID)

    expect(result.server_time).toBe(status.server_time)
    expect(result.slot?.slot).toBe(2)
    expect(result.counters.mine).toBe(4)
    expect(result.counters.category_total).toBe(17)
  })

  test('[P0] a null slot survives the client untouched', async () => {
    server.use(http.get('*/api/v1/mark/status', () =>
      HttpResponse.json({ server_time: new Date().toISOString(), slot: null, counters: { mine: 0, category_total: 0 } }),
    ))

    const result = await markingApi().markStatus(markEventID, markCategoryID)

    // `slot: null` is the explicit no-slot-open state. A client that coerces it
    // to 0 or {} would render a countdown for a slot that does not exist.
    expect(result.slot).toBeNull()
  })

  test('[P0] the marking calls inherit the single 401 refresh retry', async () => {
    let attempts = 0
    server.use(
      http.get('*/api/v1/mark/status', () => {
        attempts += 1
        if (attempts === 1) return HttpResponse.json({ code: 'UNAUTHENTICATED', message: 'expired' }, { status: 401 })
        return HttpResponse.json(markStatusOpen())
      }),
    )

    const refresh = vi.fn(async () => 'refreshed-token')
    const result = await markingApi(sessionPort({ refresh })).markStatus(markEventID, markCategoryID)

    expect(refresh).toHaveBeenCalledTimes(1)
    expect(attempts).toBe(2)
    expect(result.counters.mine).toBe(4)
  })

  test('[P0] a network failure rejects — it is never reported as a queued success', async () => {
    server.use(http.post('*/api/v1/mark', () => HttpResponse.error()))

    // Epic 0 has no queue. A timeout is red, never amber.
    await expect(
      markingApi().mark({
        event_id: markEventID,
        category_id: markCategoryID,
        capture_method: 'nfc',
        card: { version: 'v1', token: 'QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI' },
      }),
    ).rejects.toBeTruthy()
  })
})
