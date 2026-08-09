import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { Application } from '../../Application'
import { createPortalMemoryRouter } from '../../router'
import type { SessionPort } from './session'

/**
 * What a guarded route does when there is no session.
 *
 * It sends the visitor to the front door — it does NOT render a sign-in card at
 * the guarded URL. These tests are written against the REAL router rather than a
 * stubbed `/` on purpose: the whole defect was that "signed out" and "on the
 * front door" were two different places, and only the real route table proves
 * that `/` is the page with both choices on it.
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

function installAdmin() {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: 'admin@amrita.edu',
        name: 'Portal Admin',
        role: 'admin',
      }),
    ),
    http.get('*/api/v1/admin/events', () =>
      HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [] })),
  )
}

function openPortal(session: SessionPort) {
  return render(
    <Application session={session} router={createPortalMemoryRouter(['/portal/events'])} />,
  )
}

/** Both of the things the front door offers — the pair IS the assertion. */
async function expectFrontDoor() {
  expect(
    await screen.findByRole('button', { name: /sign in with microsoft/i }),
  ).toBeInTheDocument()
  expect(
    screen.getByRole('link', { name: /public dashboard/i }),
    'landing without the public dashboard is the sign-in card the front door replaced',
  ).toBeInTheDocument()
}

describe('authentication boundary', () => {
  test('returns an expired session to the front door after the single refresh attempt', async () => {
    const session = sessionPort({ accessToken: vi.fn(async () => 'expired-token'), refresh: vi.fn(async () => null) })
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })),
    )

    openPortal(session)

    await waitFor(() => expect(session.signOut).toHaveBeenCalledTimes(1))
    await expectFrontDoor()
    expect(screen.queryByRole('heading', { name: 'Events' })).not.toBeInTheDocument()
    expect(session.refresh).toHaveBeenCalledTimes(1)
  })

  // The reported defect, end to end.
  //
  // Signing out clears the identity but NOT the address bar, so a coordinator
  // stayed on the guarded URL they were already on and was handed a card whose
  // only offer was to sign back in — the one thing they had just declined to be.
  test('[P0] signing out leaves the guarded route and lands on the front door', async () => {
    const user = userEvent.setup()
    installAdmin()
    openPortal(sessionPort())

    await user.click(await screen.findByRole('button', { name: /account: portal admin/i }))
    await user.click(await screen.findByRole('menuitem', { name: /sign out/i }))

    await expectFrontDoor()
    expect(
      screen.queryByRole('heading', { name: 'Events' }),
      'the guarded screen must not survive the sign-out',
    ).not.toBeInTheDocument()
  })

  // And arriving at a guarded URL with no session at all — a bookmark, a shared
  // link — is the same journey, not a different one.
  test('[P0] a guarded URL opened without a session shows the front door, and issues no scoped read', async () => {
    let scopedReads = 0
    installAdmin()
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'No session.' }, { status: 401 })),
      http.get('*/api/v1/admin/events', () => {
        scopedReads += 1
        return HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })
      }),
    )

    openPortal(sessionPort({ accessToken: vi.fn(async () => null) }))

    await expectFrontDoor()
    expect(scopedReads, 'the boundary must refuse before the screen reads anything').toBe(0)
  })
})
