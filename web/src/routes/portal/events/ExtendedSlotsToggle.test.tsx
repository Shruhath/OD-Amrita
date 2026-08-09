import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'

/**
 * The evening-slots switch.
 *
 * It changes what every coordinator's device will accept, so the assertions
 * here are about the two ways a switch lies: showing a position the server did
 * not take, and reporting a ceiling it does not hold.
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

type Installed = { writes: unknown[] }

function installAdmin(setting = { enabled: false, max_slot: 12, updated_at: null as string | null }, writeStatus = 200): Installed {
  const installed: Installed = { writes: [] }
  let current = { ...setting }
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
    http.get('*/api/v1/admin/settings/extended-slots', () => HttpResponse.json(current)),
    http.put('*/api/v1/admin/settings/extended-slots', async ({ request }) => {
      const body = (await request.json()) as { enabled: boolean }
      installed.writes.push(body)
      if (writeStatus !== 200) {
        return HttpResponse.json({ code: 'INTERNAL', message: 'Could not save.' }, { status: writeStatus })
      }
      // The SERVER decides the ceiling. Echoing it back is what the screen must
      // render, rather than a number of its own.
      current = { enabled: body.enabled, max_slot: body.enabled ? 20 : 12, updated_at: '2026-08-09T18:00:00Z' }
      return HttpResponse.json(current)
    }),
  )
  return installed
}

function renderEvents() {
  return render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/events'])} />)
}

describe('the evening-slots switch', () => {
  // SLOTS-WEB-001 — it shows the state IN FORCE, and names the ceiling.
  //
  // The difference between 12 and 20 is invisible anywhere else until a
  // coordinator is refused, so the screen has to say it out loud.
  test('[P0] shows the ceiling currently in force', async () => {
    installAdmin()
    renderEvents()

    const toggle = await screen.findByRole('switch', { name: /slots 13/i })
    expect(toggle).not.toBeChecked()
    expect(await screen.findByText(/slots 1–12 are currently accepted/i)).toBeInTheDocument()
  })

  // SLOTS-WEB-002 — turning it on sends the write and reports the new ceiling.
  test('[P0] turning it on writes the setting and reports 20', async () => {
    const user = userEvent.setup()
    const installed = installAdmin()
    renderEvents()

    await user.click(await screen.findByRole('switch', { name: /slots 13/i }))

    await waitFor(() => expect(installed.writes).toEqual([{ enabled: true }]))
    expect(await screen.findByText(/slots 1–20 are currently accepted/i)).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /slots 13/i })).toBeChecked()
  })

  // SLOTS-WEB-003 — and it turns back OFF.
  //
  // A switch that cannot be turned off is not a switch. This one exists so an
  // evening rehearsal can be undone before the real event, which makes the
  // return trip the more important direction.
  test('[P0] turning it back off restores the teaching day', async () => {
    const user = userEvent.setup()
    const installed = installAdmin({ enabled: true, max_slot: 20, updated_at: null })
    renderEvents()

    const toggle = await screen.findByRole('switch', { name: /slots 13/i })
    await waitFor(() => expect(toggle).toBeChecked())

    await user.click(toggle)

    await waitFor(() => expect(installed.writes).toEqual([{ enabled: false }]))
    expect(await screen.findByText(/slots 1–12 are currently accepted/i)).toBeInTheDocument()
  })

  // SLOTS-WEB-004 — THE CEILING COMES FROM THE SERVER.
  //
  // The screen must not hold its own copy of what "on" means. A server that
  // answered with a different ceiling — because the number changed, or because
  // this build is older than the API — has to win, or the picker offers slots
  // the API refuses and a coordinator reads that as the app being broken.
  test('[P0] renders the server\'s ceiling, not a number of its own', async () => {
    installAdmin({ enabled: true, max_slot: 16, updated_at: null })
    renderEvents()

    expect(
      await screen.findByText(/slots 1–16 are currently accepted/i),
      'the screen rendered its own idea of the ceiling instead of the one the server reported',
    ).toBeInTheDocument()
  })

  // SLOTS-WEB-005 — a failed write is reported, and does not leave the switch
  // showing a position the server never took.
  test('[P0] a refused write is explained', async () => {
    const user = userEvent.setup()
    installAdmin(undefined, 500)
    renderEvents()

    await user.click(await screen.findByRole('switch', { name: /slots 13/i }))

    expect(await screen.findByText(/could not save/i)).toBeInTheDocument()
    // Still 12: the ceiling shown is the one the server last confirmed.
    expect(screen.getByText(/slots 1–12 are currently accepted/i)).toBeInTheDocument()
  })
})
