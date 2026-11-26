/**
 * A token refresh must not cost an /auth/me round trip.
 *
 * Supabase fires an auth-state change on every token refresh and every time a
 * phone returns from the background, and AuthGate used to re-resolve the
 * identity on all of them. On the server that endpoint is the most expensive one
 * there is: it updated a row, wrote an audit entry and opened two transactions
 * per call. Production measured 2,157 of them.
 *
 * These tests pin the transitions that DO still resolve, so the saving cannot be
 * bought by breaking sign-in or sign-out.
 */
import { describe, expect, test, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { MemoryRouter } from 'react-router-dom'
import { server } from '../../test/server'
import type { SessionPort } from './session'
import { AuthProvider } from './AuthGate'
import { useAuth } from './context'

function Identity() {
  const { identity, loading } = useAuth()
  if (loading) return <p>loading</p>
  return <p data-testid="role">{identity ? identity.role : 'signed-out'}</p>
}

/**
 * A port whose `subscribe` hands the listener a TOKEN, exactly as
 * `browserSession.subscribe` does. That argument is the whole subject of these
 * tests: a listener invoked without one cannot tell a refresh from a sign-in.
 */
function tokenAwarePort() {
  const listeners: Array<(token: string | null) => void> = []
  let current: string | null = 'token-1'

  const port: SessionPort = {
    accessToken: vi.fn(async () => current),
    refresh: vi.fn(async () => current),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn((listener: (token: string | null) => void) => {
      listeners.push(listener)
      return () => undefined
    }),
  }

  return {
    port,
    /** Supabase rotates the access token; the session is otherwise unchanged. */
    refreshToken(next = 'token-2') {
      current = next
      listeners.forEach((listener) => listener(next))
    },
    /** The session ends. */
    signOut() {
      current = null
      listeners.forEach((listener) => listener(null))
    },
    /** A fresh sign-in after a signed-out state. */
    signIn(next = 'token-3') {
      current = next
      listeners.forEach((listener) => listener(next))
    },
  }
}

function countingIdentityRoute() {
  let calls = 0
  server.use(
    http.get('*/api/v1/auth/me', () => {
      calls += 1
      return HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: 'admin@amrita.edu',
        name: 'Portal Admin',
        role: 'admin',
      })
    }),
  )
  return () => calls
}

function renderGate(port: SessionPort) {
  render(
    <MemoryRouter>
      <AuthProvider session={port}>
        <Identity />
      </AuthProvider>
    </MemoryRouter>,
  )
}

describe('identity resolution cadence', () => {
  test('[P0] a token refresh while signed in does not re-resolve the identity', async () => {
    const calls = countingIdentityRoute()
    const session = tokenAwarePort()

    renderGate(session.port)
    await screen.findByTestId('role')
    expect(calls()).toBe(1)

    // Several refreshes, as a long working session produces.
    session.refreshToken('token-2')
    session.refreshToken('token-3')
    session.refreshToken('token-4')

    // Give any stray resolution a chance to land before asserting it did not.
    await new Promise((resolve) => setTimeout(resolve, 20))

    expect(calls()).toBe(1)
    expect(screen.getByTestId('role')).toHaveTextContent('admin')
  })

  test('[P0] losing the token still re-resolves, so a sign-out clears the identity', async () => {
    countingIdentityRoute()
    const session = tokenAwarePort()

    renderGate(session.port)
    await screen.findByTestId('role')

    session.signOut()

    await waitFor(() => expect(screen.getByTestId('role')).toHaveTextContent('signed-out'))
  })

  test('[P0] signing in from a signed-out state resolves the new identity', async () => {
    const calls = countingIdentityRoute()
    const session = tokenAwarePort()

    renderGate(session.port)
    await screen.findByTestId('role')
    const afterMount = calls()

    session.signOut()
    await waitFor(() => expect(screen.getByTestId('role')).toHaveTextContent('signed-out'))

    session.signIn('token-3')
    await waitFor(() => expect(screen.getByTestId('role')).toHaveTextContent('admin'))

    // Mount, sign-out and sign-in each resolve; the refreshes in between do not.
    expect(calls()).toBeGreaterThan(afterMount)
  })
})
