import { render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { AuthProvider, SessionBoundary } from './AuthGate'
import type { SessionPort } from './session'

describe('authentication boundary', () => {
  test('returns an expired session to sign-in after the single refresh attempt', async () => {
    const session: SessionPort = {
      accessToken: vi.fn(async () => 'expired-token'),
      refresh: vi.fn(async () => null),
      signIn: vi.fn(async () => undefined),
      signOut: vi.fn(async () => undefined),
      subscribe: vi.fn(() => () => undefined),
    }
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ code: 'AUTH_TOKEN_INVALID', message: 'Expired.' }, { status: 401 })),
    )

    render(
      <AuthProvider session={session}>
        <SessionBoundary><p>Protected content</p></SessionBoundary>
      </AuthProvider>,
    )

    expect(await screen.findByRole('heading', { name: 'Sign in to OD-Amrita' })).toBeInTheDocument()
    expect(screen.queryByText('Protected content')).not.toBeInTheDocument()
    expect(session.refresh).toHaveBeenCalledTimes(1)
    expect(session.signOut).toHaveBeenCalledTimes(1)
  })
})
