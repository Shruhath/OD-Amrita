import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../Application'
import type { SessionPort } from '../features/auth/session'
import { createPortalMemoryRouter } from '../router'
import { sectionsFor } from '../routes/access'
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

  /**
   * THE PHONE'S ONLY WAY BETWEEN SCREENS.
   *
   * Below 768px the section rail and the account menu are both display:none and
   * everything they offered lives behind one toggle. A toggle that does not open
   * therefore does not degrade the chrome — it removes navigation from the phone
   * entirely, on the surface most likely to be used from one. The media query is
   * not evaluated here, so these assert the mechanism rather than the breakpoint:
   * the sheet opens, it carries the same role-filtered sections the rail does,
   * and it closes again.
   */
  test('[P0] the phone toggle opens a sheet carrying the sections and the account', async () => {
    const user = userEvent.setup()
    installTeacher()
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/live'])} />)

    const toggle = await screen.findByRole('button', { name: /open navigation/i })
    expect(screen.queryByRole('dialog', { name: /navigation menu/i })).toBeNull()

    await user.click(toggle)

    const sheet = await screen.findByRole('dialog', { name: /navigation menu/i })
    // The same declaration the rail reads, so the sheet cannot offer a teacher a
    // screen that answers "Access denied" — or omit the one they can use.
    for (const section of sectionsFor('teacher')) {
      expect(
        within(sheet).getByRole('link', { name: section.label }),
        `the phone sheet does not offer ${section.label}, which this role can open`,
      ).toBeInTheDocument()
    }
    expect(
      within(sheet).getByRole('button', { name: /account|sign out|portal teacher/i }),
      'the account is unreachable on a phone: the desktop menu is hidden and the sheet does not carry it',
    ).toBeInTheDocument()
  })

  test('[P0] Escape closes the phone sheet', async () => {
    const user = userEvent.setup()
    installTeacher()
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/live'])} />)

    await user.click(await screen.findByRole('button', { name: /open navigation/i }))
    expect(await screen.findByRole('dialog', { name: /navigation menu/i })).toBeInTheDocument()

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: /navigation menu/i }),
        'the sheet stays up after Escape, so it covers the workspace with no keyboard way out',
      ).toBeNull()
    })
  })
})
