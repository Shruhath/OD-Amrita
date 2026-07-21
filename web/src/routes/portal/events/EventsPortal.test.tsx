import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import App from '../../../App'
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
  window.history.pushState({}, '', '/portal/events')
  window.localStorage.setItem('od-amrita-atdd-session', 'authenticated')

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
    http.get('*/api/v1/admin/events', () => {
      adminRequest('events')
      if (eventsStatus !== 200) {
        return HttpResponse.json(
          { code: 'INTERNAL', message: 'Events could not be loaded' },
          { status: eventsStatus },
        )
      }
      return HttpResponse.json({ data: serverEvents, page: 1, page_size: 20, total: serverEvents.length })
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
      serverEvents = [created, ...serverEvents]
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

  const view = render(<App />)
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

    await user.click(within(dialog).getByRole('button', { name: 'Create event' }))
    expect(within(dialog).getByLabelText('Event name')).toHaveAttribute('aria-invalid', 'true')
    expect(within(dialog).getByText('Select at least one branch')).toBeInTheDocument()

    const firstBranch = within(dialog).getByRole('checkbox', { name: /Computer Science/ })
    firstBranch.focus()
    fireEvent.keyDown(firstBranch, { key: ' ' })
    expect(firstBranch).toBeChecked()
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
