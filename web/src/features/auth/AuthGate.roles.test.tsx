import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { AuthProvider, SessionBoundary } from './AuthGate'
import type { SessionPort } from './session'

/**
 * `SessionBoundary`'s allowed-roles parameter.
 *
 * The default is `['admin']`, so all four portal routes keep their behaviour
 * without being edited. A change that removed the role check, or flipped the
 * default to "any signed-in user", would make every admin screen reachable by
 * every student — which is why the default is asserted here as hard as the new
 * capability itself.
 */

const RoleBoundary = SessionBoundary

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function identityOfRole(role: string) {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        role,
      }),
    ),
  )
}

describe('role-parameterised session boundary', () => {
  test('[P0] the default stays admin-only so the four portal routes are untouched', async () => {
    identityOfRole('coordinator')

    render(
      <AuthProvider session={sessionPort()}>
        {/* No allowedRoles: this is exactly how the portal routes call it. */}
        <SessionBoundary><p>Admin content</p></SessionBoundary>
      </AuthProvider>,
    )

    expect(await screen.findByRole('heading', { name: 'Access denied' })).toBeInTheDocument()
    expect(
      screen.queryByText('Admin content'),
      'defaulting to anything other than admin would expose every portal screen to every signed-in user',
    ).not.toBeInTheDocument()
  })

  test('[P0] a coordinator passes a boundary that allows coordinators', async () => {
    identityOfRole('coordinator')

    render(
      <AuthProvider session={sessionPort()}>
        <RoleBoundary allowedRoles={['coordinator']}><p>Marking surface</p></RoleBoundary>
      </AuthProvider>,
    )

    expect(await screen.findByText('Marking surface')).toBeInTheDocument()
  })

  test('[P0] admin, teacher and student are all refused at the marking boundary', async () => {
    // C2: teacher/admin marking has its own screen and needs an explicit slot picker
    // this screen deliberately does not have. Admitting an admin "because admins
    // can do anything" would let them mark with no slot control at all.
    for (const role of ['admin', 'teacher', 'student']) {
      identityOfRole(role)

      const view = render(
        <AuthProvider session={sessionPort()}>
          <RoleBoundary allowedRoles={['coordinator']}><p>Marking surface</p></RoleBoundary>
        </AuthProvider>,
      )

      expect(await screen.findByRole('heading', { name: /access|denied/i })).toBeInTheDocument()
      expect(screen.queryByText('Marking surface')).not.toBeInTheDocument()
      view.unmount()
    }
  })

  test('[P0] the refusal is an explicit state, never a blank screen', async () => {
    identityOfRole('student')

    render(
      <AuthProvider session={sessionPort()}>
        <RoleBoundary allowedRoles={['coordinator']}><p>Marking surface</p></RoleBoundary>
      </AuthProvider>,
    )

    // AC 2: "an explicit, non-blank access state". A coordinator who was never
    // assigned, or a student who followed a shared link, must be told why.
    const heading = await screen.findByRole('heading')
    expect(heading.textContent?.trim()).not.toBe('')
    expect(screen.getByRole('button', { name: /sign out/i })).toBeInTheDocument()
  })
})
