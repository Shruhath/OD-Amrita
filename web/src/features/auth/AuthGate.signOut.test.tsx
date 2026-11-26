import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { Application } from '../../Application'
import { AccountMenu } from '../../components/AccountMenu'
import { createPortalMemoryRouter } from '../../router'
import { PERSISTENCE_PREFIX } from '../persistence/sessionState'
import { AuthProvider } from './AuthGate'
import { useAuth } from './context'
import { SignOutError, type SessionPort } from './session'

/**
 * Signing out on a phone that is passed between people.
 *
 * The screen, the stored selections and the query cache must all be gone after
 * a sign-out, WHETHER OR NOT the sign-out request reached the server — and when
 * it did not, the person holding the phone must be told.
 */

function sessionPort(signOut: SessionPort['signOut']): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(signOut),
    subscribe: vi.fn(() => () => undefined),
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
    http.get('*/api/v1/admin/events', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [] })),
  )
}

function SignedInAs() {
  const { identity } = useAuth()
  return <p>{identity ? `Signed in as ${identity.email}` : 'Nobody signed in'}</p>
}

function renderProvider(session: SessionPort, onSignOut = vi.fn()) {
  render(
    <AuthProvider session={session} onSignOut={onSignOut}>
      <SignedInAs />
      <AccountMenu />
    </AuthProvider>,
  )
  return onSignOut
}

async function signOutThroughMenu() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole('button', { name: /account:/i }))
  await user.click(await screen.findByRole('menuitem', { name: /sign out/i }))
}

describe('sign-out on a shared device', () => {
  test('[P0] a failed sign-out still drops the identity, the stored state and the cache', async () => {
    installAdmin()
    window.sessionStorage.setItem(`${PERSISTENCE_PREFIX}mark.selection.v1`, '"event-1"')
    const session = sessionPort(async () => {
      throw new SignOutError(new TypeError('Failed to fetch'), true)
    })
    const onSignOut = renderProvider(session)

    expect(await screen.findByText('Signed in as admin@amrita.edu')).toBeInTheDocument()
    await signOutThroughMenu()

    expect(await screen.findByText('Nobody signed in')).toBeInTheDocument()
    expect(onSignOut, 'the query cache must be cleared even when the request failed').toHaveBeenCalledTimes(1)
    expect(window.sessionStorage.getItem(`${PERSISTENCE_PREFIX}mark.selection.v1`)).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent(/signed out on this device, but the server could not be reached/i)
  })

  test('[P0] when the device itself could not be cleared, the message says to close the tab', async () => {
    installAdmin()
    renderProvider(sessionPort(async () => {
      throw new Error('storage unavailable')
    }))

    await screen.findByText('Signed in as admin@amrita.edu')
    await signOutThroughMenu()

    expect(await screen.findByRole('alert')).toHaveTextContent(/close this browser tab/i)
    expect(screen.getByText('Nobody signed in')).toBeInTheDocument()
  })

  test('[P1] a clean sign-out shows no message', async () => {
    installAdmin()
    const onSignOut = renderProvider(sessionPort(async () => undefined))

    await screen.findByText('Signed in as admin@amrita.edu')
    await signOutThroughMenu()

    expect(await screen.findByText('Nobody signed in')).toBeInTheDocument()
    expect(onSignOut).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('[P1] the message can be dismissed', async () => {
    const user = userEvent.setup()
    installAdmin()
    renderProvider(sessionPort(async () => {
      throw new SignOutError(new TypeError('Failed to fetch'), true)
    }))

    await screen.findByText('Signed in as admin@amrita.edu')
    await signOutThroughMenu()
    await user.click(await screen.findByRole('button', { name: 'Dismiss' }))

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  test('[P0] through the real app, a failed sign-out lands on the front door with the message', async () => {
    installAdmin()
    const session = sessionPort(async () => {
      throw new SignOutError(new TypeError('Failed to fetch'), true)
    })
    render(<Application session={session} router={createPortalMemoryRouter(['/portal/events'])} />)

    await signOutThroughMenu()

    expect(await screen.findByRole('button', { name: /sign in with microsoft/i })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Events' })).not.toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(/signed out on this device/i)
  })
})
