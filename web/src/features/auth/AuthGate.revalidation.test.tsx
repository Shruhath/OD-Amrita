import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AuthProvider, SessionBoundary } from './AuthGate'
import HomeLanding from '../../routes/HomeLanding'
import type { SessionPort } from './session'

/**
 * Returning to the app must not rebuild the screen.
 *
 * THE DEFECT THIS FILE EXISTS FOR: Supabase fires an auth state change whenever
 * it refreshes a token, which includes the moment a phone comes back to the
 * browser after the operator switched apps. Every one of those set `loading`
 * true, SessionBoundary swapped the screen for the loading state, and the whole
 * route below it UNMOUNTED — so a loaded roster, a half-filled form and a chosen
 * event were all destroyed by glancing at a notification.
 *
 * These tests assert against component STATE rather than against the presence of
 * markup, because that is the thing that was actually being lost: a remount
 * restores identical markup with the state reset to its initial value, which
 * looks correct in a snapshot and is exactly the bug.
 */

/** A child that would visibly forget if it were ever remounted. */
function StatefulScreen() {
  const [typed, setTyped] = useState('')
  const [mountID] = useState(() => Math.random().toString(36).slice(2))
  return (
    <div>
      <label htmlFor="draft">Draft</label>
      <input id="draft" value={typed} onChange={(event) => setTyped(event.target.value)} />
      <p data-testid="mount-id">{mountID}</p>
    </div>
  )
}

/**
 * The guarded screen, at a guarded URL, inside a router.
 *
 * The router is not decoration. Losing the session no longer swaps a sign-in
 * card in beneath the same URL — it REDIRECTS to `/`, so a bare render would
 * throw rather than fail. `/` is wired to the real HomeLanding rather than a
 * placeholder so "was the operator actually signed out?" is still asked of the
 * page they land on, which is where the sign-in offer now lives.
 */
function renderGuarded(port: SessionPort) {
  return render(
    <AuthProvider session={port}>
      <MemoryRouter initialEntries={['/portal/events']}>
        <Routes>
          <Route path="/" element={<HomeLanding />} />
          <Route
            path="/portal/events"
            element={<SessionBoundary><StatefulScreen /></SessionBoundary>}
          />
        </Routes>
      </MemoryRouter>
    </AuthProvider>,
  )
}

/**
 * A token read the test can HOLD OPEN.
 *
 * This is what makes these tests able to fail. The defect is a flash of the
 * loading state, and with an instantly-resolving port the whole revalidation
 * completes inside one microtask — React coalesces the `loading` true and false
 * into a single render, nothing is ever unmounted, and the test passes just as
 * happily against the broken code. Holding the revalidation open puts the
 * component in the exact intermediate state the bug lives in.
 */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function sessionPort(overrides: Partial<SessionPort> = {}) {
  const listeners: Array<() => void> = []
  let held: { promise: Promise<string | null>; resolve: (value: string | null) => void } | null = null

  const port: SessionPort = {
    accessToken: vi.fn(async () => (held ? held.promise : 'fresh-token')),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn((listener: () => void) => {
      listeners.push(listener)
      return () => undefined
    }),
    ...overrides,
  }

  /** Stands in for Supabase announcing a token refresh on return to the tab. */
  const returnToApp = () => listeners.forEach((listener) => listener())

  /** Returns to the app and leaves the revalidation in flight. */
  const returnToAppSlowly = () => {
    held = deferred<string | null>()
    returnToApp()
    return {
      finish: (token: string | null = 'fresh-token') => {
        const pending = held!
        held = null
        pending.resolve(token)
      },
    }
  }

  return { port, returnToApp, returnToAppSlowly }
}

function identityResponds(role = 'admin') {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: 'admin@amrita.edu',
        name: 'Portal Admin',
        role,
      }),
    ),
  )
}

describe('returning to a backgrounded app', () => {
  test('[P0] the screen keeps its state when the session is revalidated', async () => {
    const user = userEvent.setup()
    identityResponds()
    const { port, returnToAppSlowly } = sessionPort()

    renderGuarded(port)

    const field = await screen.findByLabelText('Draft')
    await user.type(field, 'CSE')
    const mountID = screen.getByTestId('mount-id').textContent

    // The phone comes back to the browser and Supabase refreshes the token.
    const inFlight = returnToAppSlowly()

    // WHILE THE REVALIDATION IS STILL IN FLIGHT — the moment the defect lived
    // in — the work is still there, and it is the SAME mount rather than an
    // identical screen rebuilt with empty state.
    await waitFor(() => {
      expect(screen.getByTestId('mount-id')).toHaveTextContent(mountID!)
    })
    expect(screen.getByLabelText('Draft')).toHaveValue('CSE')

    inFlight.finish()
    await waitFor(() => expect(screen.getByLabelText('Draft')).toHaveValue('CSE'))
    expect(screen.getByTestId('mount-id')).toHaveTextContent(mountID!)
  })

  test('[P0] the loading screen never appears on a revalidation', async () => {
    identityResponds()
    const { port, returnToAppSlowly } = sessionPort()

    renderGuarded(port)
    await screen.findByLabelText('Draft')

    const inFlight = returnToAppSlowly()

    // Flipping to the loading state is precisely what unmounts the route, so
    // its absence WHILE THE CHECK IS RUNNING is the mechanism under test, not a
    // cosmetic preference.
    await waitFor(() => expect(screen.getByLabelText('Draft')).toBeInTheDocument())
    expect(screen.queryByRole('status')).not.toBeInTheDocument()

    inFlight.finish()
  })

  test('[P0] a dropped revalidation request does not sign the operator out', async () => {
    const user = userEvent.setup()
    identityResponds()
    const { port, returnToApp } = sessionPort()

    renderGuarded(port)
    await user.type(await screen.findByLabelText('Draft'), 'CSE')

    // A phone moving between wifi and mobile data. The credential is still good;
    // the request simply never arrived.
    server.use(http.get('*/api/v1/auth/me', () => HttpResponse.error()))
    returnToApp()

    await waitFor(() => expect(screen.getByLabelText('Draft')).toHaveValue('CSE'))
    expect(
      screen.queryByRole('button', { name: /sign in with microsoft/i }),
      'a network blip is not a sign-out, and treating it as one throws away the operator\'s work',
    ).not.toBeInTheDocument()
  })

  test('[P0] a revoked session still signs the operator out', async () => {
    // The other side of the guard above: a 401 is the server actively refusing
    // the credential, and it must still tear the screen down. A rule that kept
    // someone on screen after a real revocation would be a security hole.
    identityResponds()
    const { port, returnToApp } = sessionPort()

    renderGuarded(port)
    await screen.findByLabelText('Draft')

    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Authentication token is invalid.' }, { status: 401 }),
      ),
    )
    returnToApp()

    expect(await screen.findByRole('button', { name: /sign in with microsoft/i })).toBeInTheDocument()
    expect(screen.queryByLabelText('Draft')).not.toBeInTheDocument()
  })

  test('[P0] signing out elsewhere still clears the screen', async () => {
    identityResponds()
    const token = { current: 'fresh-token' as string | null }
    const { port, returnToApp } = sessionPort({ accessToken: vi.fn(async () => token.current) })

    renderGuarded(port)
    await screen.findByLabelText('Draft')

    token.current = null
    returnToApp()

    expect(await screen.findByRole('button', { name: /sign in with microsoft/i })).toBeInTheDocument()
  })

  test('[P0] the first resolution still shows the loading state', async () => {
    // The revalidation fix must not silently remove the initial wait, or a cold
    // start would flash the sign-in card before the identity arrives.
    identityResponds()
    const { port } = sessionPort()

    renderGuarded(port)

    expect(screen.getByRole('status')).toBeInTheDocument()
    await screen.findByLabelText('Draft')
  })
})
