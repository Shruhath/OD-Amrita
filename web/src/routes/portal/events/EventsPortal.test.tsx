import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import {
  createEventFixture,
  testBranches,
  type EventFixture,
} from '../../../../tests/support/factories/event'

type PortalScenario = {
  role?: 'admin' | 'teacher'
  branches?: typeof testBranches | []
  events?: EventFixture[]
  eventsStatus?: number
}

function renderPortal({
  role = 'admin',
  branches = testBranches,
  events = [createEventFixture({ name: 'Innovation Day' })],
  eventsStatus = 200,
}: PortalScenario = {}) {
  const adminRequest = vi.fn()
  let serverEvents = [...events]
  const session: SessionPort = {
    accessToken: vi.fn(async () => 'atdd-access-token'),
    refresh: vi.fn(async () => 'atdd-access-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        subject: `atdd-${role}`,
        email: `${role}@amrita.edu`,
        name: `ATDD ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/admin/branches', () => {
      adminRequest('branches')
      return HttpResponse.json({ data: branches })
    }),
    http.get('*/api/v1/admin/events', ({ request }) => {
      adminRequest('events')
      if (eventsStatus !== 200) {
        return HttpResponse.json(
          { code: 'INTERNAL', message: 'Events could not be loaded' },
          { status: eventsStatus },
        )
      }
      const url = new URL(request.url)
      const page = Number(url.searchParams.get('page') ?? '1')
      const pageSize = Number(url.searchParams.get('page_size') ?? '20')
      const ordered = orderEvents(serverEvents)
      const start = (page - 1) * pageSize
      return HttpResponse.json({ data: ordered.slice(start, start + pageSize), page, page_size: pageSize, total: ordered.length })
    }),
    http.post('*/api/v1/admin/events', async ({ request }) => {
      adminRequest('create')
      const input = (await request.json()) as {
        name: string
        event_date: string
        branch_ids: string[]
      }
      const created = createEventFixture({
          name: input.name.trim(),
          event_date: input.event_date,
          branches: testBranches.filter((branch) => input.branch_ids.includes(branch.id)),
        })
      serverEvents = orderEvents([created, ...serverEvents])
      return HttpResponse.json(created, { status: 201 })
    }),
    http.post('*/api/v1/admin/events/:eventId/archive', async ({ params }) => {
      adminRequest('archive')
      await delay(50)
      const current = serverEvents.find((event) => event.id === params.eventId)
      if (!current) return HttpResponse.json({ code: 'EVENT_NOT_FOUND', message: 'Event not found' }, { status: 404 })
      const archived = { ...current, status: 'archived' as const, archived_at: '2026-07-20T04:10:00Z' }
      serverEvents = serverEvents.map((event) => event.id === archived.id ? archived : event)
      return HttpResponse.json(archived)
    }),
  )

  const view = render(<Application session={session} router={createPortalMemoryRouter()} />)
  return { adminRequest, unmount: view.unmount }
}

describe('Admin events portal composition (ATDD red phase)', () => {
  test('[P0] allows an Admin to load active and archived events', async () => {
    const archived = createEventFixture({ name: 'Research Symposium', status: 'archived' })
    renderPortal({ events: [createEventFixture({ name: 'Innovation Day' }), archived] })

    expect(await screen.findByRole('heading', { name: 'Events' })).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /Innovation Day/ })).toHaveTextContent('Active')
    expect(await screen.findByRole('row', { name: /Research Symposium/ })).toHaveTextContent('Archived')
  })

  test('[P0] denies a non-Admin without issuing an Admin event request', async () => {
    const { adminRequest } = renderPortal({ role: 'teacher' })

    expect(await screen.findByRole('heading', { name: 'Access denied' })).toBeInTheDocument()
    expect(adminRequest).not.toHaveBeenCalled()
  })

  test('[P1] validates required fields and supports keyboard multi-branch selection', async () => {
    const user = userEvent.setup()
    renderPortal()
    await user.click(await screen.findByRole('button', { name: 'New event' }))
    const dialog = screen.getByRole('dialog', { name: 'New event' })

    const submit = within(dialog).getByRole('button', { name: 'Create event' })
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Event name'), 'Keyboard Event')
    await user.type(within(dialog).getByLabelText('Event date'), '2026-10-12')

    const firstBranch = within(dialog).getByRole('checkbox', { name: /Computer Science/ })
    firstBranch.focus()
    fireEvent.keyDown(firstBranch, { key: ' ' })
    expect(firstBranch).toBeChecked()
    expect(firstBranch).toHaveFocus()
    expect(submit).toBeEnabled()
  })

  test('[P1] creates a multi-branch event and refreshes from the server result', async () => {
    const user = userEvent.setup()
    const { adminRequest } = renderPortal({ events: [] })
    await user.click(await screen.findByRole('button', { name: 'New event' }))
    const dialog = screen.getByRole('dialog', { name: 'New event' })
    await user.type(within(dialog).getByLabelText('Event name'), 'Community Outreach Day')
    await user.type(within(dialog).getByLabelText('Event date'), '2026-09-05')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Electronics/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create event' }))

    expect(await screen.findByRole('row', { name: /Community Outreach Day/ })).toHaveTextContent('Active')
    expect(adminRequest).toHaveBeenCalledWith('create')
  })

  test('[P1] keeps page one server-ordered and bounded after creation', async () => {
    const user = userEvent.setup()
    const existing = Array.from({ length: 20 }, (_, index) => createEventFixture({
      id: `00000000-0000-4000-8000-${String(index).padStart(12, '0')}`,
      name: `Existing Event ${index + 1}`,
      event_date: '2026-08-15',
      created_at: `2026-07-${String(index + 1).padStart(2, '0')}T03:00:00Z`,
    }))
    renderPortal({ events: existing })
    await user.click(await screen.findByRole('button', { name: 'New event' }))
    const dialog = screen.getByRole('dialog', { name: 'New event' })
    await user.type(within(dialog).getByLabelText('Event name'), 'Newest Event')
    await user.type(within(dialog).getByLabelText('Event date'), '2026-09-05')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create event' }))

    const table = await screen.findByRole('table')
    const rows = within(table).getAllByRole('row')
    expect(rows).toHaveLength(21)
    expect(rows[1]).toHaveTextContent('Newest Event')
  })

  test('[P1] pages through the complete active and archived event collection', async () => {
    const user = userEvent.setup()
    const paged = Array.from({ length: 21 }, (_, index) => createEventFixture({
      id: `10000000-0000-4000-8000-${String(index).padStart(12, '0')}`,
      name: `Paged Event ${index + 1}`,
      event_date: index === 20 ? '2026-01-01' : '2026-08-15',
    }))
    renderPortal({ events: paged })

    expect(await screen.findByText('Page 1 of 2')).toBeInTheDocument()
    expect(screen.queryByRole('row', { name: /Paged Event 21/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Next' }))
    expect(await screen.findByText('Page 2 of 2')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /Paged Event 21/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
  })

  test('[P1] confirms archive, prevents duplicate submission, and makes the row read-only', async () => {
    const user = userEvent.setup()
    renderPortal({ events: [createEventFixture({ name: 'Innovation Day' })] })
    const row = await screen.findByRole('row', { name: /Innovation Day/ })
    await user.click(within(row).getByRole('button', { name: 'Archive' }))
    const dialog = screen.getByRole('dialog', { name: 'Archive Innovation Day' })
    const confirm = within(dialog).getByRole('button', { name: 'Archive event' })
    await user.dblClick(confirm)

    expect(confirm).toBeDisabled()
    expect(await screen.findByRole('row', { name: /Innovation Day.*Archived/ })).not.toHaveTextContent(/Edit|Delete|Archive event/)
  })

  test('[P1] maps typed server validation details to their fields and announces the error', async () => {
    const user = userEvent.setup()
    renderPortal()
    server.use(
      http.post('*/api/v1/admin/events', () =>
        HttpResponse.json(
          {
            code: 'VALIDATION_FAILED',
            message: 'Event validation failed',
            details: [{ field: 'name', code: 'duplicate', message: 'Name already exists' }],
          },
          { status: 400 },
        ),
      ),
    )
    await user.click(await screen.findByRole('button', { name: 'New event' }))
    const dialog = screen.getByRole('dialog', { name: 'New event' })
    await user.type(within(dialog).getByLabelText('Event name'), 'Innovation Day')
    await user.type(within(dialog).getByLabelText('Event date'), '2026-08-15')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create event' }))

    expect(await within(dialog).findByText('Name already exists')).toBeInTheDocument()
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Event validation failed')
  })

  test('[P2] exposes honest empty-branch and empty-event states', async () => {
    const { unmount } = renderPortal({ branches: [], events: [] })
    expect(await screen.findByRole('heading', { name: 'No branches available' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New event' })).toBeDisabled()
    unmount()

    renderPortal({ events: [] })
    expect(await screen.findByRole('heading', { name: 'No events yet' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'New event' })).toBeEnabled()
  })

  test('[P2] renders a retryable list error without an indefinite spinner', async () => {
    renderPortal({ eventsStatus: 500 })

    expect(await screen.findByRole('alert')).toHaveTextContent('Events could not be loaded')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeEnabled()
    expect(screen.queryByRole('status', { name: 'Loading events' })).not.toBeInTheDocument()
  })
})

function orderEvents(events: EventFixture[]) {
  return [...events].sort((left, right) =>
    right.event_date.localeCompare(left.event_date)
    || right.created_at.localeCompare(left.created_at)
    || right.id.localeCompare(left.id))
}
