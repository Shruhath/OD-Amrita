import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { campusToday } from './campusDay'

/**
 * The live view of a day's marks.
 *
 * A teacher could mark students and then had no way to see the result: the
 * attendance read was admin-only at BOTH the route and the service. This screen
 * is that gap closed, and the tests below are mostly about what it must not do —
 * ask for the wrong day, or filter in the browser.
 */

const EVENT_ID = '93000000-0000-4000-8000-000000000001'

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function record(overrides: Record<string, unknown> = {}) {
  return {
    record_id: '66000000-0000-4000-8000-000000000001',
    student: {
      student_id: '95000000-0000-4000-8000-000000000101',
      roll_no: 'CB.SC.U4CSE99101',
      display_name: 'Ananya Rao',
      branch: 'CSE',
      section: 'A',
      batch: '2024',
    },
    event_id: EVENT_ID,
    category: {
      id: '94000000-0000-4000-8000-000000000001',
      event_id: EVENT_ID,
      name: 'Procession',
      created_at: '2026-08-01T00:00:00Z',
    },
    slot: 4,
    od_date: campusToday(),
    capture_method: 'search',
    marked_by: {
      user_id: '90000000-0000-4000-8000-000000000003',
      email: 'marker@amrita.edu',
      name: 'Portal Teacher',
    },
    actor_role: 'teacher',
    server_received_at: '2026-08-09T05:30:00Z',
    ...overrides,
  }
}

/** Captures the query string the screen actually asked for. */
function installBackend(rows: ReturnType<typeof record>[] = [record()]) {
  const requested: URL[] = []
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '90000000-0000-4000-8000-000000000003',
        email: 'teacher@amrita.edu',
        name: 'Portal Teacher',
        role: 'teacher',
      }),
    ),
    http.get('*/api/v1/portal/mark/events', () =>
      HttpResponse.json({
        data: [{ id: EVENT_ID, name: 'Onam Celebrations', event_date: campusToday(), end_date: campusToday(), status: 'active', branches: [] }],
      }),
    ),
    http.get('*/api/v1/portal/mark/events/:event_id/attendance', ({ request }) => {
      requested.push(new URL(request.url))
      return HttpResponse.json({ data: rows, page: 1, page_size: 50, total: rows.length })
    }),
  )
  return requested
}

function renderLive() {
  return render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/live'])} />)
}

afterEach(() => {
  window.sessionStorage.clear()
})

describe('live attendance', () => {
  test('[P0] a teacher sees the marks for today', async () => {
    installBackend()
    renderLive()

    const table = await screen.findByRole('table', { name: /live attendance/i })
    const row = within(table).getByRole('row', { name: /CB\.SC\.U4CSE99101/ })
    expect(row).toHaveTextContent('Ananya Rao')
    expect(row).toHaveTextContent('Procession')
    expect(row).toHaveTextContent('4')
  })

  test('[P0] it asks the server for the CAMPUS day, not the device day', async () => {
    // od_date is a campus-local day the server decides. A device on another
    // timezone — or simply the half hour either side of midnight IST — would
    // otherwise request a day the marks were never filed under, and the screen
    // would read as empty during the event.
    const requested = installBackend()
    renderLive()

    await screen.findByRole('table', { name: /live attendance/i })
    await waitFor(() => expect(requested.length).toBeGreaterThan(0))
    expect(requested[0].searchParams.get('od_date')).toBe(campusToday())
  })

  test('[P0] it calls the teacher route, never the admin one', async () => {
    // The admin attendance route 403s a teacher. Calling it here would make the
    // screen work in development, as an admin, and fail for every teacher.
    const requested = installBackend()
    renderLive()

    await screen.findByRole('table', { name: /live attendance/i })
    await waitFor(() => expect(requested.length).toBeGreaterThan(0))
    expect(requested[0].pathname).toContain('/api/v1/portal/mark/events/')
    expect(requested[0].pathname).not.toContain('/admin/')
  })

  test('[P0] a colleague\'s email is never rendered', async () => {
    // marked_by carries the actor's address. This table is read across an event
    // and there is no reason for a colleague's email to be on it.
    installBackend()
    renderLive()

    const table = await screen.findByRole('table', { name: /live attendance/i })
    expect(table).toHaveTextContent('Portal Teacher')
    expect(table).not.toHaveTextContent('marker@amrita.edu')
  })

  test('[P0] an empty day says so rather than showing a bare table', async () => {
    installBackend([])
    renderLive()

    expect(await screen.findByRole('heading', { name: /nothing marked yet/i })).toBeInTheDocument()
    expect(screen.queryByRole('table', { name: /live attendance/i })).not.toBeInTheDocument()
  })

  test('[P0] a refused read is explained and retryable', async () => {
    installBackend()
    server.use(
      http.get('*/api/v1/portal/mark/events/:event_id/attendance', () =>
        HttpResponse.json({ code: 'RBAC_FORBIDDEN', message: 'Teacher or administrator access is required for attendance review.' }, { status: 403 }),
      ),
    )
    renderLive()

    expect(await screen.findByRole('alert')).toHaveTextContent(/teacher or administrator access/i)
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument()
  })
})
