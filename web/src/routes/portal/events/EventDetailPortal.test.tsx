import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { createEventFixture, testBranches } from '../../../../tests/support/factories/event'
import { attendanceCollection } from '../../../../tests/support/factories/teacher-admin-marking'
import {
  archivedEventError,
  createCategoryFixture,
  duplicateNameError,
  serverOrdered,
  testEventID,
  type CategoryFixture,
} from '../../../../tests/support/factories/category'

// Story 2.2 component coverage, AC 1-4 and 7.
//
// Invoked by: web-deploy -> build job (`pnpm --filter od-amrita-web test`).
//
// The detail component is reached by CLICKING THROUGH from the events list
// rather than by importing it, which makes the navigation itself part of what
// AC 7 proves.

type DetailScenario = {
  status?: 'active' | 'archived'
  categories?: CategoryFixture[]
  categoriesStatus?: number
  createResponse?: 'ok' | 'duplicate' | 'archived'
  /**
   * Makes the event ITSELF flip to archived once a create has been rejected with
   * EVENT_ARCHIVED — i.e. somebody archived it while this page was open. Without
   * this the 409 and the event read would disagree forever, which is exactly the
   * stale-cache state the component now refetches out of.
   */
  archiveOnCreate?: boolean
  createGate?: Promise<void>
  /**
   * Blocks every list read AFTER the first one. Without it a refetch re-sorts
   * the rows server-side and hides whatever order the client wrote into the
   * cache — which is precisely how Story 2.1's cache-ordering defect stayed
   * invisible to its tests.
   */
  listRefetchGate?: Promise<void>
}

const eventName = 'Onam Celebrations'
const adminEventsCollectionPath = /\/api\/v1\/admin\/events(?:\?.*)?$/
const adminEventItemPath = /\/api\/v1\/admin\/events\/[^/?]+$/
const adminEventCategoriesPath = /\/api\/v1\/admin\/events\/[^/?]+\/categories$/
const adminEventCoordinatorsPath = /\/api\/v1\/admin\/events\/[^/?]+\/coordinators$/

function renderPortal({
  status = 'active',
  categories = [],
  categoriesStatus = 200,
  createResponse = 'ok',
  createGate,
  listRefetchGate,
  archiveOnCreate = false,
}: DetailScenario = {}) {
  let event = createEventFixture({ id: testEventID, name: eventName, status })
  let serverCategories = serverOrdered(categories)
  const calls: string[] = []

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
        subject: 'atdd-admin',
        email: 'admin@amrita.edu',
        name: 'ATDD admin',
        role: 'admin',
      }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: testBranches })),
    http.get(adminEventsCollectionPath, () =>
      HttpResponse.json({ data: [event], page: 1, page_size: 20, total: 1 }),
    ),
    // The detail page reads its event by ID. It used to scan page 1 of the
    // collection above and filter locally, which reported "Event not found" for
    // any event past the first page — so the item endpoint is what the detail
    // scenarios must serve, and an unknown id must answer a real 404.
    http.get(adminEventItemPath, ({ request }) => {
      const eventID = lastPathSegment(request.url)
      if (eventID !== testEventID) {
        return HttpResponse.json(
          { code: 'EVENT_NOT_FOUND', message: 'Event not found.' },
          { status: 404 },
        )
      }
      return HttpResponse.json(event)
    }),
    http.get(adminEventCategoriesPath, async ({ request }) => {
      const eventID = categoryEventID(request.url)
      const isRefetch = calls.includes('list')
      calls.push('list')
      if (listRefetchGate && isRefetch) await listRefetchGate
      if (categoriesStatus !== 200) {
        return HttpResponse.json(
          { code: 'INTERNAL', message: 'Categories could not be loaded' },
          { status: categoriesStatus },
        )
      }
      // Scoping is server-side: a request for another event must never see
      // this event's rows.
      const scoped = serverCategories.filter((category) => category.event_id === eventID)
      return HttpResponse.json({ data: scoped })
    }),
    http.get(adminEventCoordinatorsPath, () => HttpResponse.json({ data: [] })),
    http.get('*/api/v1/admin/events/:eventID/attendance', () => HttpResponse.json(attendanceCollection([]))),
    http.post(adminEventCategoriesPath, async ({ request }) => {
      const eventID = categoryEventID(request.url)
      calls.push('create')
      if (createGate) await createGate
      if (createResponse === 'duplicate') {
        return HttpResponse.json(duplicateNameError(), { status: 400 })
      }
      if (createResponse === 'archived') {
        if (archiveOnCreate) {
          event = { ...event, status: 'archived' }
        }
        return HttpResponse.json(archivedEventError(), { status: 409 })
      }
      const input = (await request.json()) as { name: string }
      const created = createCategoryFixture({
        event_id: eventID,
        name: input.name.trim(),
      })
      serverCategories = serverOrdered([...serverCategories, created])
      return HttpResponse.json(created, { status: 201 })
    }),
  )

  return { calls, event, session }
}

function lastPathSegment(rawURL: string) {
  const segments = new URL(rawURL).pathname.split('/').filter(Boolean)
  return segments[segments.length - 1] ?? ''
}

function categoryEventID(rawURL: string) {
  const segments = new URL(rawURL).pathname.split('/').filter(Boolean)
  return segments[segments.length - 2] ?? ''
}

function renderAt(scenario: DetailScenario = {}, path?: string) {
  const context = renderPortal(scenario)
  render(
    <Application
      session={context.session}
      router={createPortalMemoryRouter(path ? [path] : undefined)}
    />,
  )
  return context
}

async function openEventDetail(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('link', { name: eventName }, { timeout: 5_000 }))
  return screen.findByRole('heading', { level: 1, name: eventName })
}

describe('Admin event detail and categories', () => {
  test('[P1] opens an event from the list and shows the categories it already has', async () => {
    const user = userEvent.setup()
    renderAt({
      categories: [
        createCategoryFixture({ name: 'Stage Setup' }),
        createCategoryFixture({ name: 'Procession' }),
      ],
    })

    await openEventDetail(user)

    // The server's order is the promise; the client renders it, never re-sorts.
    const list = await screen.findByRole('list', { name: 'Categories' })
    const items = within(list).getAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual(['Procession', 'Stage Setup'])
  })

  test('[P1] adds a category and shows it without a manual reload', async () => {
    const user = userEvent.setup()
    const { calls } = renderAt()

    await openEventDetail(user)
    await user.type(screen.getByLabelText('Category name'), 'Procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(await within(list).findByText('Procession')).toBeInTheDocument()
    // The new row must come from the server result or a refetch, not from local
    // component state that a reload would discard.
    expect(calls).toContain('create')
  })

  test('[P1] keeps the form usable so several categories can be added in sequence', async () => {
    const user = userEvent.setup()
    renderAt()

    await openEventDetail(user)
    const input = screen.getByLabelText('Category name')
    const submit = screen.getByRole('button', { name: 'Add category' })

    for (const name of ['Procession', 'Stage Setup', 'Registration']) {
      await user.type(input, name)
      await user.click(submit)
      const list = await screen.findByRole('list', { name: 'Categories' })
      expect(await within(list).findByText(name)).toBeInTheDocument()
    }

    // Journey 5 adds three categories in a row, so the field must clear itself
    // and stay focused rather than forcing the admin to reopen anything.
    expect(input).toHaveValue('')
    expect(input).toHaveFocus()
  })

  test('[P0] places a newly added category in the server\u2019s order, not submission order', async () => {
    const user = userEvent.setup()
    let releaseRefetch = () => {}
    const listRefetchGate = new Promise<void>((resolve) => {
      releaseRefetch = resolve
    })
    renderAt({
      categories: [createCategoryFixture({ name: 'Registration' })],
      listRefetchGate,
    })

    await openEventDetail(user)
    const list = await screen.findByRole('list', { name: 'Categories' })

    // "Procession" sorts BEFORE the row already on screen, so appending it
    // would put it in the wrong place. With the refetch blocked, the rendered
    // order can only have come from the client's own cache write.
    await user.type(screen.getByLabelText('Category name'), 'Procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    await within(list).findByText('Procession')
    expect(within(list).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'Procession',
      'Registration',
    ])
    releaseRefetch()
  })

  test('[P0] maps the typed duplicate detail onto the name field', async () => {
    const user = userEvent.setup()
    renderAt({
      categories: [createCategoryFixture({ name: 'Procession' })],
      createResponse: 'duplicate',
    })

    await openEventDetail(user)
    const input = screen.getByLabelText('Category name')
    await user.type(input, 'procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    // The server is authoritative on duplicates: it compares case-insensitively
    // after trimming, which the client cannot reliably reproduce.
    const message = await screen.findByText('A category with this name already exists.')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription(/already exists/)
    expect(message).toBeInTheDocument()

    // A rejected create adds nothing.
    const list = screen.getByRole('list', { name: 'Categories' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(1)
  })

  test('[P0] announces a non-field rejection in a live region', async () => {
    const user = userEvent.setup()
    renderAt({ createResponse: 'archived' })

    await openEventDetail(user)
    await user.type(screen.getByLabelText('Category name'), 'Procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    // EVENT_ARCHIVED and EVENT_NOT_FOUND are not about the name field, so
    // attaching them to the input would be a lie to a screen reader.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('This event is archived and can no longer be changed.')
    expect(screen.getByLabelText('Category name')).not.toHaveAttribute('aria-invalid', 'true')
  })

  test('[P1] shows an archived event read-only with no add affordance at all', async () => {
    const user = userEvent.setup()
    renderAt({
      status: 'archived',
      categories: [createCategoryFixture({ name: 'Procession' })],
    })

    await openEventDetail(user)
    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(await within(list).findByText('Procession')).toBeInTheDocument()
    // Not merely disabled — absent. A disabled control still advertises an
    // action the aggregate can never accept again.
    expect(screen.queryByLabelText('Category name')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add category' })).not.toBeInTheDocument()
    // Status is conveyed as text, not colour alone.
    expect(screen.getByText('Archived')).toBeInTheDocument()
  })

  test('[P2] gates submission while the name is blank and while the create is in flight', async () => {
    const user = userEvent.setup()
    let release = () => {}
    const createGate = new Promise<void>((resolve) => {
      release = resolve
    })
    renderAt({ createGate })

    await openEventDetail(user)
    const submit = screen.getByRole('button', { name: 'Add category' })
    expect(submit).toBeDisabled()

    // Whitespace is not a name; the server would reject it, so the client must
    // not present it as submittable.
    await user.type(screen.getByLabelText('Category name'), '   ')
    expect(submit).toBeDisabled()

    await user.type(screen.getByLabelText('Category name'), 'Procession')
    expect(submit).toBeEnabled()

    await user.click(submit)
    expect(submit).toBeDisabled()
    release()
    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(await within(list).findByText('Procession')).toBeInTheDocument()
  })

  test('[P2] shows an honest empty state rather than an indefinite spinner', async () => {
    const user = userEvent.setup()
    renderAt({ categories: [] })

    await openEventDetail(user)

    expect(await screen.findByText('No categories yet')).toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Loading categories' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add category' })).toBeInTheDocument()
  })

  test('[P2] renders a retryable error when the categories cannot be loaded', async () => {
    const user = userEvent.setup()
    renderAt({ categoriesStatus: 500 })

    await openEventDetail(user)

    expect(await screen.findByRole('alert')).toHaveTextContent('Categories could not be loaded')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeEnabled()
    expect(screen.queryByRole('status', { name: 'Loading categories' })).not.toBeInTheDocument()
  })

  test('[P2] is fully operable from the keyboard', async () => {
    const user = userEvent.setup()
    renderAt()

    await user.tab()
    // Reaching the detail view without a pointer is the baseline; a row that is
    // only clickable is not a link.
    const link = await screen.findByRole('link', { name: eventName })
    link.focus()
    await user.keyboard('{Enter}')

    const input = await screen.findByLabelText('Category name')
    input.focus()
    await user.keyboard('Procession{Enter}')

    // Enter in a single-field form submits it: the form element must be real,
    // not a div with a click handler.
    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(await within(list).findByText('Procession')).toBeInTheDocument()
  })

  test('[P1] shows the event context the admin needs to know where they are', async () => {
    const user = userEvent.setup()
    renderAt({ categories: [createCategoryFixture({ name: 'Procession' })] })

    await openEventDetail(user)

    // The calendar date must not shift: the API sends a date-only string and
    // the existing formatDate helper renders it without a UTC round trip.
    expect(screen.getByText('15 Aug 2026')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
    for (const branch of testBranches) {
      expect(screen.getByText(branch.code)).toBeInTheDocument()
    }
  })

  test('[P0] opens the detail view directly from its own URL', async () => {
    // The browser-integration harness restores this route after a reload, and a
    // real admin can bookmark it. Reaching the view must not depend on having
    // walked through the events list in the same session.
    renderAt(
      { categories: [createCategoryFixture({ name: 'Procession' })] },
      `/portal/events/${testEventID}`,
    )

    await screen.findByRole('heading', { level: 1, name: eventName })
    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(within(list).getByText('Procession')).toBeInTheDocument()
  })

  test('[P0] requests only the opened event’s categories', async () => {
    const user = userEvent.setup()
    const other = createCategoryFixture({
      event_id: '20000000-0000-4000-8000-00000000000f',
      name: 'Another Event Category',
    })
    renderAt({ categories: [createCategoryFixture({ name: 'Procession' }), other] })

    const heading = await openEventDetail(user)
    expect(heading).toBeInTheDocument()

    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(within(list).getByText('Procession')).toBeInTheDocument()
    // AC 3 is a server guarantee, but a client that reads a shared cache key
    // across events would leak rows anyway.
    expect(within(list).queryByText('Another Event Category')).not.toBeInTheDocument()
  })

  test('[P0] resolves an event the events list has not loaded', async () => {
    // The detail view used to find its event by scanning page 1 of the admin
    // events collection and filtering locally, so an event outside that window
    // rendered "Event not found" for an event that exists and is active. This
    // pins the item read: the collection here deliberately returns NOTHING.
    server.use(
      http.get(adminEventsCollectionPath, () =>
        HttpResponse.json({ data: [], page: 1, page_size: 20, total: 0 }),
      ),
    )
    renderAt(
      { categories: [createCategoryFixture({ name: 'Procession' })] },
      `/portal/events/${testEventID}`,
    )

    await screen.findByRole('heading', { level: 1, name: eventName })
    expect(screen.queryByText('Event not found')).not.toBeInTheDocument()
    const list = await screen.findByRole('list', { name: 'Categories' })
    expect(within(list).getByText('Procession')).toBeInTheDocument()
  })

  test('[P0] does not let a successful create hide a failed category list', async () => {
    // setQueryData marks a query SUCCESSFUL. Writing the created row into a list
    // whose own fetch failed would clear the error and render a ONE-ITEM list as
    // though it were the event's complete set — a wrong count stated with full
    // confidence, which is worse than the error it replaced.
    const user = userEvent.setup()
    renderAt({ categoriesStatus: 500 })

    await openEventDetail(user)
    expect(await screen.findByRole('alert')).toHaveTextContent('Categories could not be loaded')

    await user.type(await screen.findByLabelText('Category name'), 'Procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    // The list never claims to know the categories while its read is broken.
    expect(await screen.findByRole('alert')).toHaveTextContent('Categories could not be loaded')
    expect(screen.queryByRole('list', { name: 'Categories' })).not.toBeInTheDocument()
  })

  test('[P1] retracts a field rejection once the admin edits the name', async () => {
    // The rejection described the PREVIOUS value. Leaving it wired to the input
    // through aria-describedby means a screen reader announces "a category with
    // this name already exists" over a name the admin has since changed.
    const user = userEvent.setup()
    renderAt({ categories: [createCategoryFixture({ name: 'Procession' })], createResponse: 'duplicate' })

    await openEventDetail(user)
    const input = await screen.findByLabelText('Category name')
    await user.type(input, 'Procession')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    expect(await screen.findByText('A category with this name already exists.')).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')

    await user.clear(input)
    await user.type(input, 'Stage Setup')

    expect(screen.queryByText('A category with this name already exists.')).not.toBeInTheDocument()
    expect(input).not.toHaveAttribute('aria-invalid')
  })

  test('[P0] removes the add affordance when the event was archived elsewhere', async () => {
    // Somebody archived this event while the page was open. Announcing the 409
    // but leaving the form on screen invites the admin to retry forever, so the
    // event is refetched and the UI comes back into agreement with the server.
    const user = userEvent.setup()
    renderAt({
      categories: [createCategoryFixture({ name: 'Procession' })],
      createResponse: 'archived',
      archiveOnCreate: true,
    })

    await openEventDetail(user)
    await user.type(await screen.findByLabelText('Category name'), 'Stage Setup')
    await user.click(screen.getByRole('button', { name: 'Add category' }))

    expect(
      await screen.findByText('This event is archived. Its categories are read-only.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add category' })).not.toBeInTheDocument()
  })
})
