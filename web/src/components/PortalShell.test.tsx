import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../Application'
import type { SessionPort } from '../features/auth/session'
import { createPortalMemoryRouter } from '../router'
import { server } from '../test/server'

/**
 * The way off a portal screen.
 *
 * THE GAP THIS CLOSES. A screen like the live view is somewhere an operator sits
 * for an hour, and the only exits it offered were the other section links and
 * "Sign out" — which is a heavy thing to press when all you wanted was to get
 * back to the start. The brand in the top left is the affordance everybody
 * already reaches for, and it was inert markup.
 *
 * `/` routes by role, so this is a HOME link rather than a sign-in link: a
 * signed-in operator lands on their own start screen, and a signed-out visitor
 * lands on the front door where signing in lives. RoleLanding owns that map and
 * its own tests hold it — what matters here is that the brand navigates at all.
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

function installTeacher() {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: 'teacher@amrita.edu',
        name: 'Portal Teacher',
        role: 'teacher',
      }),
    ),
    http.get('*/api/v1/portal/mark/events', () => HttpResponse.json({ data: [] })),
    http.get('*/api/v1/admin/events', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [] })),
  )
}

describe('the portal chrome', () => {
  test('[P0] the brand is a link home, so the live view is not a dead end', async () => {
    const user = userEvent.setup()
    installTeacher()
    const router = createPortalMemoryRouter(['/portal/live'])
    render(<Application session={sessionPort()} router={router} />)

    const brand = await screen.findByRole('link', { name: /od-amrita home/i })
    // A real href, so it can be opened in a new tab and middle-clicked — the
    // things a click handler on a div cannot do.
    expect(brand).toHaveAttribute('href', '/')

    await user.click(brand)

    expect(
      router.state.location.pathname,
      'the brand did not navigate, so a teacher parked on the live view has no way back that is not signing out',
    ).not.toBe('/portal/live')
  })

  test('[P0] the brand names itself for a screen reader', async () => {
    installTeacher()
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/live'])} />)

    // The visible content is a decorative seal plus the wordmark, so the
    // accessible name has to say what the link DOES rather than spell "OD".
    const brand = await screen.findByRole('link', { name: /od-amrita home/i })
    expect(brand).toBeInTheDocument()
  })
})
