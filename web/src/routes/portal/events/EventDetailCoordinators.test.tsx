import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { createEventFixture, testBranches } from '../../../../tests/support/factories/event'
import { createCategoryFixture, testEventID } from '../../../../tests/support/factories/category'
import {
  coordinatorArchivedEventError,
  createCoordinatorAssignmentFixture,
  duplicateAssignmentError,
  roleAlreadyAssignedError,
  serverOrderedCoordinatorAssignments,
  userNotFoundError,
  type CoordinatorAssignmentFixture,
} from '../../../../tests/support/factories/coordinator'

type CoordinatorScenario = {
  status?: 'active' | 'archived'
  coordinators?: CoordinatorAssignmentFixture[]
  createResponse?: 'ok' | 'duplicate' | 'missing-user' | 'ineligible' | 'archived'
  archiveOnCreate?: boolean
  createGate?: Promise<void>
}

const eventName = 'Onam Celebrations'
const adminEventsCollectionPath = /\/api\/v1\/admin\/events(?:\?.*)?$/
const adminEventItemPath = /\/api\/v1\/admin\/events\/[^/?]+$/
const adminEventCategoriesPath = /\/api\/v1\/admin\/events\/[^/?]+\/categories$/
const adminEventCoordinatorsPath = /\/api\/v1\/admin\/events\/[^/?]+\/coordinators$/
const adminEventCoordinatorCategoriesPath = /\/api\/v1\/admin\/events\/[^/?]+\/coordinators\/[^/?]+\/categories$/
const adminEventAttendancePath = /\/api\/v1\/admin\/events\/[^/?]+\/attendance(?:\?.*)?$/

function renderCoordinatorPortal({
  status = 'active',
  coordinators = [],
  createResponse = 'ok',
  archiveOnCreate = false,
  createGate,
}: CoordinatorScenario = {}) {
  let event = createEventFixture({ id: testEventID, name: eventName, status })
  let serverCoordinators = serverOrderedCoordinatorAssignments(coordinators)
  const category = createCategoryFixture({ name: 'Procession' })
  const campusCategory = createCategoryFixture({ name: 'Campus Decoration' })
  const calls: string[] = []

  const session: SessionPort = {
    accessToken: vi.fn(async () => 'coordinator-assignment-access-token'),
    refresh: vi.fn(async () => 'coordinator-assignment-access-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        subject: 'assignment-admin',
        email: 'admin@amrita.edu',
        name: 'Assignment admin',
        role: 'admin',
      }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: testBranches })),
    http.get(adminEventsCollectionPath, () =>
      HttpResponse.json({ data: [event], page: 1, page_size: 20, total: 1 }),
    ),
    http.get(adminEventItemPath, ({ request }) => {
      if (lastPathSegment(request.url) !== testEventID) {
        return HttpResponse.json({ code: 'EVENT_NOT_FOUND', message: 'Event not found.' }, { status: 404 })
      }
      return HttpResponse.json(event)
    }),
    http.get(adminEventCategoriesPath, () =>
      HttpResponse.json({ data: [category, campusCategory] }),
    ),
    http.get(adminEventAttendancePath, () =>
      HttpResponse.json({ data: [], page: 1, page_size: 20, total: 0 }),
    ),
    http.get(adminEventCoordinatorsPath, () => {
      calls.push('list-coordinators')
      return HttpResponse.json({ data: serverCoordinators })
    }),
    http.post(adminEventCoordinatorsPath, async ({ request }) => {
      calls.push('create-coordinator')
      if (createGate) await createGate
      if (createResponse === 'duplicate') {
        return HttpResponse.json(duplicateAssignmentError(), { status: 409 })
      }
      if (createResponse === 'missing-user') {
        return HttpResponse.json(userNotFoundError(), { status: 404 })
      }
      if (createResponse === 'ineligible') {
        return HttpResponse.json(roleAlreadyAssignedError(), { status: 409 })
      }
      if (createResponse === 'archived') {
        if (archiveOnCreate) {
          event = { ...event, status: 'archived' }
        }
        return HttpResponse.json(coordinatorArchivedEventError(), { status: 409 })
      }
      const input = (await request.json()) as { email: string; category_ids: string[] }
      const created = createCoordinatorAssignmentFixture({
        email: input.email.trim().toLowerCase(),
        event_id: testEventID,
        categories: input.category_ids.includes(category.id) ? [{ id: category.id, name: category.name }] : [],
      })
      serverCoordinators = serverOrderedCoordinatorAssignments([...serverCoordinators, created])
      return HttpResponse.json(created, { status: 201 })
    }),
    http.put(adminEventCoordinatorCategoriesPath, async ({ request }) => {
      calls.push('update-coordinator')
      const userID = lastPathSegment(new URL(request.url).pathname.replace('/categories', ''))
      const input = (await request.json()) as { category_ids: string[] }
      const found = serverCoordinators.find((coordinator) => coordinator.user_id === userID)
      if (!found) return HttpResponse.json(userNotFoundError(), { status: 404 })
      const updated = {
        ...found,
        categories: [category, campusCategory]
          .filter((item) => input.category_ids.includes(item.id))
          .map((item) => ({ id: item.id, name: item.name })),
      }
      serverCoordinators = serverOrderedCoordinatorAssignments(serverCoordinators.map((coordinator) => (
        coordinator.user_id === userID ? updated : coordinator
      )))
      return HttpResponse.json(updated)
    }),
  )

  render(<Application session={session} router={createPortalMemoryRouter()} />)
  return { calls, session }
}

function lastPathSegment(rawURL: string) {
  const segments = new URL(rawURL, 'http://localhost').pathname.split('/').filter(Boolean)
  return segments[segments.length - 1] ?? ''
}

async function openEventDetail(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('link', { name: eventName }, { timeout: 5_000 }))
  return screen.findByRole('heading', { level: 1, name: eventName })
}

describe('Admin event detail coordinator assignments', () => {
  test('[P0] lists assigned coordinators in server order and keeps categories intact', async () => {
    const user = userEvent.setup()
    renderCoordinatorPortal({
      coordinators: [
        createCoordinatorAssignmentFixture({ email: 'second@amrita.edu', created_at: '2026-08-03T03:05:00Z' }),
        createCoordinatorAssignmentFixture({ email: 'first@amrita.edu', created_at: '2026-08-03T03:00:00Z' }),
      ],
    })

    await openEventDetail(user)

    const coordinators = await screen.findByRole('list', { name: 'Coordinators' })
    expect(within(coordinators).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      expect.stringContaining('first@amrita.edu'),
      expect.stringContaining('second@amrita.edu'),
    ])
    expect(await screen.findByRole('list', { name: 'Categories' })).toBeInTheDocument()
  })

  test('[P0] assigns a provisioned student by email and shows the row without a reload', async () => {
    const user = userEvent.setup()
    const { calls } = renderCoordinatorPortal()

    await openEventDetail(user)
    await user.type(screen.getByLabelText('Coordinator email'), 'candidate@amrita.edu')
    await user.click(screen.getByRole('checkbox', { name: /Procession/ }))
    await user.click(screen.getByRole('button', { name: 'Assign coordinator' }))

    const coordinators = await screen.findByRole('list', { name: 'Coordinators' })
    expect(await within(coordinators).findByText('candidate@amrita.edu')).toBeInTheDocument()
    expect(calls).toContain('create-coordinator')
  })

  test('[P0] edits an existing coordinator category access without recreating the assignment', async () => {
    const user = userEvent.setup()
    const existing = createCoordinatorAssignmentFixture({
      email: 'existing@amrita.edu',
      categories: [{ id: createCategoryFixture({ name: 'Procession' }).id, name: 'Procession' }],
    })
    const { calls } = renderCoordinatorPortal({ coordinators: [existing] })

    await openEventDetail(user)
    const coordinators = await screen.findByRole('list', { name: 'Coordinators' })
    await user.click(within(coordinators).getByRole('button', { name: 'Edit' }))
    await user.click(within(coordinators).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(coordinators).getByRole('button', { name: 'Save access' }))

    expect(await within(coordinators).findByText(/Campus Decoration/)).toBeInTheDocument()
    expect(calls).toContain('update-coordinator')
    expect(calls).not.toContain('create-coordinator')
  })

  test.each(['duplicate', 'missing-user', 'ineligible'] as const)(
    '[P0] maps %s coordinator assignment rejection to a distinct message',
    async (createResponse) => {
      const user = userEvent.setup()
      renderCoordinatorPortal({ createResponse })
      await openEventDetail(user)
      fireEvent.change(screen.getByLabelText('Coordinator email'), { target: { value: `${createResponse}@amrita.edu` } })
      await user.click(screen.getByRole('checkbox', { name: /Procession/ }))
      await user.click(screen.getByRole('button', { name: 'Assign coordinator' }))
      expect(await screen.findByRole('alert')).toBeVisible()
    },
  )

  test('[P1] clears stale form errors when the email changes', async () => {
    const user = userEvent.setup()
    renderCoordinatorPortal({ createResponse: 'missing-user' })

    await openEventDetail(user)
    const input = screen.getByLabelText('Coordinator email')
    await user.type(input, 'missing@amrita.edu')
    await user.click(screen.getByRole('checkbox', { name: /Procession/ }))
    await user.click(screen.getByRole('button', { name: 'Assign coordinator' }))
    expect(await screen.findByRole('alert')).toBeVisible()
    await user.type(input, '.fixed')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  test('[P1] removes the add affordance after a stale archived-event rejection', async () => {
    const user = userEvent.setup()
    renderCoordinatorPortal({ createResponse: 'archived', archiveOnCreate: true })

    await openEventDetail(user)
    await user.type(screen.getByLabelText('Coordinator email'), 'late@amrita.edu')
    await user.click(screen.getByRole('checkbox', { name: /Procession/ }))
    await user.click(screen.getByRole('button', { name: 'Assign coordinator' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/archived/i)
    expect(screen.queryByRole('button', { name: 'Assign coordinator' })).not.toBeInTheDocument()
  })

  test('[P1] blocks blank and in-flight duplicate submits', async () => {
    let release = () => {}
    const user = userEvent.setup()
    renderCoordinatorPortal({
      createGate: new Promise<void>((resolve) => {
        release = resolve
      }),
    })

    await openEventDetail(user)
    const submit = screen.getByRole('button', { name: 'Assign coordinator' })
    expect(submit).toBeDisabled()
    await user.type(screen.getByLabelText('Coordinator email'), 'candidate@amrita.edu')
    await user.click(screen.getByRole('checkbox', { name: /Procession/ }))
    await user.click(submit)
    expect(submit).toBeDisabled()
    release()
  })

  test('[P1] shows archived events as read-only while keeping coordinator rows readable', async () => {
    const user = userEvent.setup()
    renderCoordinatorPortal({
      status: 'archived',
      coordinators: [createCoordinatorAssignmentFixture({ email: 'kept@amrita.edu' })],
    })

    await openEventDetail(user)

    expect(await screen.findByText('kept@amrita.edu')).toBeInTheDocument()
    expect(screen.queryByLabelText('Coordinator email')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Assign coordinator' })).not.toBeInTheDocument()
  })
})
