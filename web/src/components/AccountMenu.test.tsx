import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../test/server'
import { AuthProvider } from '../features/auth/AuthGate'
import type { SessionPort } from '../features/auth/session'
import { AccountMenu } from './AccountMenu'
import { initialsFor } from './initials'

/**
 * The account control.
 *
 * The address used to sit in the bar and was hidden entirely below 360px, so a
 * coordinator on a phone could see neither which account they were signed in as
 * nor, on the marking screen, any way to sign out. It is now one tap away on
 * every width, which is what these tests hold.
 */

function sessionPort(signOut = vi.fn(async () => undefined)): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut,
    subscribe: vi.fn(() => () => undefined),
  }
}

function identity(overrides: { name?: string | null; email?: string } = {}) {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: overrides.email ?? 'cb.sc.u4cse99712@cb.students.amrita.edu',
        name: overrides.name === undefined ? 'Ananya Rao' : overrides.name,
        role: 'coordinator',
      }),
    ),
  )
}

function renderMenu(session: SessionPort = sessionPort()) {
  return render(
    <AuthProvider session={session}>
      <AccountMenu />
    </AuthProvider>,
  )
}

describe('account menu', () => {
  test('[P0] shows only the avatar until it is opened', async () => {
    identity()
    renderMenu()

    const trigger = await screen.findByRole('button', { name: /account: ananya rao/i })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(
      screen.queryByText(/cb\.sc\.u4cse99712@/i),
      'the address belongs in the menu, not in the bar',
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /sign out/i })).not.toBeInTheDocument()
  })

  test('[P0] opening it reveals the email and a sign out', async () => {
    const user = userEvent.setup()
    identity()
    renderMenu()

    await user.click(await screen.findByRole('button', { name: /account:/i }))

    expect(screen.getByText('cb.sc.u4cse99712@cb.students.amrita.edu')).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /sign out/i })).toBeInTheDocument()
  })

  test('[P0] sign out is actually wired to the session', async () => {
    const user = userEvent.setup()
    const signOut = vi.fn(async () => undefined)
    identity()
    renderMenu(sessionPort(signOut))

    await user.click(await screen.findByRole('button', { name: /account:/i }))
    await user.click(screen.getByRole('menuitem', { name: /sign out/i }))

    await waitFor(() => expect(signOut).toHaveBeenCalledOnce())
  })

  test('[P0] Escape closes it and puts focus back on the trigger', async () => {
    const user = userEvent.setup()
    identity()
    renderMenu()

    const trigger = await screen.findByRole('button', { name: /account:/i })
    await user.click(trigger)
    expect(screen.getByRole('menuitem', { name: /sign out/i })).toBeInTheDocument()

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /sign out/i })).not.toBeInTheDocument()
    })
    // Dropping focus to the document would send the next Tab to the top of the
    // page, which on the portal is past the whole navigation.
    expect(trigger).toHaveFocus()
  })

  test('[P0] a click outside closes it', async () => {
    const user = userEvent.setup()
    identity()
    render(
      <AuthProvider session={sessionPort()}>
        <AccountMenu />
        <button type="button">Somewhere else</button>
      </AuthProvider>,
    )

    await user.click(await screen.findByRole('button', { name: /account:/i }))
    expect(screen.getByRole('menuitem', { name: /sign out/i })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /somewhere else/i }))

    await waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: /sign out/i })).not.toBeInTheDocument()
    })
  })

  test('[P0] renders nothing at all when nobody is signed in', async () => {
    const port = { ...sessionPort(), accessToken: vi.fn(async () => null) }
    render(
      <AuthProvider session={port}>
        <AccountMenu />
      </AuthProvider>,
    )

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /account:/i })).not.toBeInTheDocument()
    })
  })
})

describe('avatar initials', () => {
  test('[P0] a two-word name gives one letter from each', () => {
    expect(initialsFor('Ananya Rao', 'a@b.com')).toBe('AR')
  })

  test('[P0] a roll-number account still gets two distinguishable characters', () => {
    // The real shape of a student account: `name` is often the roll number, so
    // the dots have to count as word breaks — otherwise the whole roll number is
    // one "word" and the avatar is a single character for every student.
    //
    // Both spellings agree, which is the point: whether the name is absent and
    // the address is used, or the name IS the roll number, one account gets one
    // avatar rather than two depending on which field happened to be populated.
    expect(initialsFor(null, 'cb.sc.u4cse99712@cb.students.amrita.edu')).toBe('CS')
    expect(initialsFor('cb.sc.u4cse99712', 'cb.sc.u4cse99712@cb.students.amrita.edu')).toBe('CS')
  })

  test('[P0] never returns an empty avatar', () => {
    expect(initialsFor('', 'x@amrita.edu')).toBe('X')
    expect(initialsFor('   ', 'y@amrita.edu')).toBe('Y')
  })
})
