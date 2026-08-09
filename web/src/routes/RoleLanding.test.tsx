import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi, afterEach } from 'vitest'
import { server } from '../test/server'
import { AuthProvider } from '../features/auth/AuthGate'
import type { SessionPort } from '../features/auth/session'
import RoleLanding from './RoleLanding'

/**
 * Where sign-in puts each role.
 *
 * The sign-in redirect names ONE url for everybody — it has to, because the
 * browser leaves for Microsoft before the app has ever seen a role. It used to
 * name `/portal/events`, so every coordinator finished signing in and was shown
 * "Access denied" by an admin-gated screen. RoleLanding is what replaced that,
 * and these tests are what stop it regressing.
 */

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function signedOut(): SessionPort {
  return { ...sessionPort(), accessToken: vi.fn(async () => null) }
}

function identityOfRole(role: string) {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `Landing ${role}`,
        role,
      }),
    ),
  )
}

/**
 * Renders RoleLanding at `/` alongside a marker for every destination it can
 * choose, so the assertion is "the visitor ARRIVED at X" rather than "some
 * function returned the string X" — a redirect that names a route the router
 * does not serve would pass the second and fail the first.
 */
function renderLanding(session: SessionPort) {
  return render(
    <AuthProvider session={session}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<RoleLanding />} />
          <Route path="/portal/events" element={<p>Events workspace</p>} />
          <Route path="/portal/mark" element={<p>Teacher marking</p>} />
          <Route path="/portal/od-records" element={<p>Record management</p>} />
          <Route path="/mark" element={<p>Coordinator marking</p>} />
          <Route path="/attendance" element={<p>Public dashboard</p>} />
        </Routes>
      </MemoryRouter>
    </AuthProvider>,
  )
}

afterEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('role landing after sign-in', () => {
  test.each([
    ['admin', 'Events workspace'],
    ['teacher', 'Record management'],
    ['coordinator', 'Coordinator marking'],
    ['student', 'Public dashboard'],
  ])('[P0] a %s lands on the screen their own role admits', async (role, destination) => {
    identityOfRole(role)

    renderLanding(sessionPort())

    expect(await screen.findByText(destination)).toBeInTheDocument()
  })

  test('[P0] a coordinator never lands on an admin screen', async () => {
    // The regression this whole file exists for. Asserting the ABSENCE as well
    // as the arrival, because a landing that rendered both would still be wrong.
    identityOfRole('coordinator')

    renderLanding(sessionPort())

    expect(await screen.findByText('Coordinator marking')).toBeInTheDocument()
    expect(
      screen.queryByText('Events workspace'),
      'sign-in used to send every role to the admin events list, which then refused them',
    ).not.toBeInTheDocument()
  })

  test('[P0] a role this bundle does not know falls back to the public dashboard', async () => {
    // Roles are assigned in the database, not shipped in this bundle, so an
    // unknown one is reachable. It must not default to an admin screen.
    identityOfRole('registrar')

    renderLanding(sessionPort())

    expect(await screen.findByText('Public dashboard')).toBeInTheDocument()
  })

  test('[P0] a signed-out visitor is offered sign-in in place, not bounced to a portal url', async () => {
    renderLanding(signedOut())

    expect(await screen.findByRole('button', { name: /sign in with microsoft/i })).toBeInTheDocument()
    expect(screen.queryByText('Events workspace')).not.toBeInTheDocument()
  })

  test('[P0] the sign-in card is not flashed while the provider callback is still being exchanged', async () => {
    // Immediately after Microsoft redirects back, there is genuinely no session
    // yet — the code has not been exchanged. Reading that as "signed out" shows
    // a sign-in button to someone who has just finished signing in.
    window.history.replaceState({}, '', '/?code=pkce-authorization-code')

    renderLanding(signedOut())

    expect(await screen.findByRole('status')).toHaveTextContent(/./)
    expect(
      screen.queryByRole('button', { name: /sign in with microsoft/i }),
      'the pending callback was read as a signed-out visitor',
    ).not.toBeInTheDocument()
  })
})
