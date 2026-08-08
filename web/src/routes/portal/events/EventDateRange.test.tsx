/**
 * Red-phase component tests for the event's date RANGE on the admin surface
 * (AC 1, 2, 3).
 *
 * Rendered through the REAL Application — QueryClientProvider -> AppBackdrop ->
 * AuthProvider -> RouterProvider — matching EventsPortal.test.tsx next door,
 * because the create form and the list column are the same screen and a test that
 * mounted the form alone would skip whether the range ever reaches the list.
 *
 * NOT gated behind an env flag or a build tag. Story 0.9 shipped a suite behind
 * ATDD_ACTIVE_09 that nothing ever set, and four Go files in this repo currently
 * sit behind `//go:build atdd` with nothing passing `-tags atdd`.
 *
 * WHAT IS DELIBERATELY NOT HERE: the 90-day bound. T5 makes it SERVICE policy
 * that will be tuned, and pinning the number in a browser test means re-recording
 * a journey every time it moves. It is asserted once, at 90 and at 91, in
 * api/internal/service/event/date_range_test.go. What IS here is that the form
 * surfaces the server's refusal on the right control.
 */
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import {
  createEventFixture,
  createMultiDayEventFixture,
  testBranches,
  type EventFixture,
} from '../../../../tests/support/factories/event'

type Submitted = Record<string, unknown>

function renderEvents({
  events = [createEventFixture({ name: 'Innovation Day' })],
  createStatus = 201,
  createBody,
}: {
  events?: EventFixture[]
  createStatus?: number
  createBody?: Record<string, unknown>
} = {}) {
  const submissions: Submitted[] = []
  const session: SessionPort = {
    accessToken: vi.fn(async () => 'atdd-access-token'),
    refresh: vi.fn(async () => 'atdd-access-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({ subject: 'atdd-admin', email: 'admin@amrita.edu', name: 'ATDD Admin', role: 'admin' }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: testBranches })),
    http.get('*/api/v1/admin/events', () =>
      HttpResponse.json({ data: events, page: 1, page_size: 20, total: events.length }),
    ),
    http.post('*/api/v1/admin/events', async ({ request }) => {
      const body = (await request.json()) as Submitted
      submissions.push(body)
      if (createStatus !== 201) {
        return HttpResponse.json(createBody, { status: createStatus })
      }
      return HttpResponse.json(createEventFixture({ name: String(body.name) }), { status: 201 })
    }),
  )

  render(<Application session={session} router={createPortalMemoryRouter(['/portal/events'])} />)
  return { submissions }
}

/**
 * Opens the create dialog and returns its controls.
 *
 * The form lives behind a `New event` button in a `Dialog`, not inline on the
 * page — asserting on the inputs without opening it reports "no such label",
 * which reads identically to "the end-date field was never added".
 *
 * The branch checkbox's accessible name is the branch CODE, not its name: the
 * label renders `{branch.code}` and keeps `branch.name` in the `title`.
 */
async function openCreateDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /new event/i }))
  return {
    name: await screen.findByLabelText(/event name/i),
    start: await screen.findByLabelText(/event date|start date/i),
    end: await screen.findByLabelText(/end date/i),
    branch: screen.getByRole('checkbox', { name: new RegExp(testBranches[0].code, 'i') }),
    submit: screen.getByRole('button', { name: /create|save/i }),
  }
}

describe('the event date range on the admin surface', () => {
  // 0.12a-WEB-001 — the create form offers an END date, and it defaults to the
  // start date so the single-day flow is unchanged.
  //
  // AC 2 at the UI. Today's form has one `Event date` input; adding a second
  // required control would make every single-day event two clicks more expensive
  // than it is now, for the overwhelmingly common case.
  test('0.12a-WEB-001 the create form has an end date that follows the start date', async () => {
    renderEvents()
    const user = userEvent.setup()
    const form = await openCreateDialog(user)

    await user.type(form.start, '2026-08-10')

    await waitFor(() => {
      expect(form.end).toHaveValue('2026-08-10')
    })
  })

  // 0.12a-WEB-002 — the submission carries `end_date`, and omitting it is not the
  // same as sending the start date twice.
  //
  // The assertion is on the REQUEST BODY, because that is the only place the
  // difference is visible. A form that renders an end-date box but never puts it
  // in the payload looks completely correct on screen.
  test('0.12a-WEB-002 a range the admin chose reaches the server as end_date', async () => {
    const { submissions } = renderEvents()
    const user = userEvent.setup()
    const form = await openCreateDialog(user)

    await user.type(form.name, 'Amritotsavam')
    await user.type(form.start, '2026-08-10')
    await user.clear(form.end)
    await user.type(form.end, '2026-09-08')
    await user.click(form.branch)
    await user.click(form.submit)

    await waitFor(() => {
      expect(submissions).toHaveLength(1)
    })
    expect(submissions[0]).toMatchObject({ event_date: '2026-08-10', end_date: '2026-09-08' })
  })

  // 0.12a-WEB-003 — a server refusal on `end_date` marks the END DATE control.
  //
  // The service returns VALIDATION_FAILED with `details[0].field = "end_date"`
  // for a range over 90 days. A form that renders every field error against the
  // start date, or against a form-level banner, tells the admin the wrong control
  // is wrong — and the one they need to change looks fine.
  test('0.12a-WEB-003 an over-long range marks the end date field, not the start', async () => {
    renderEvents({
      createStatus: 400,
      createBody: {
        code: 'VALIDATION_FAILED',
        message: 'Event validation failed.',
        details: [
          {
            field: 'end_date',
            code: 'range_too_long',
            message: 'An event may not span more than 90 days.',
          },
        ],
      },
    })
    const user = userEvent.setup()
    const form = await openCreateDialog(user)

    await user.type(form.name, 'Very long festival')
    await user.type(form.start, '2026-08-10')
    await user.clear(form.end)
    await user.type(form.end, '2027-08-09')
    await user.click(form.branch)
    await user.click(form.submit)

    await waitFor(() => {
      expect(form.end).toHaveAttribute('aria-invalid', 'true')
    })
    expect(await screen.findByText(/90 days/i)).toBeInTheDocument()
    expect(form.start).not.toHaveAttribute('aria-invalid', 'true')
  })

  // 0.12a-WEB-004 — the list renders a RANGE for a multi-day event and a SINGLE
  // date for a one-day one.
  //
  // AC 3. Rendering "10 Aug 2026 – 10 Aug 2026" for every ordinary event is a
  // visible regression on the surface admins use most, so `formatDateRange` has
  // to collapse the equal case. The single-day row is asserted in the same test
  // because a formatter that ALWAYS collapses would satisfy the multi-day
  // assertion if the two dates were ever rendered separately.
  test('0.12a-WEB-004 the list shows a range for a festival and one date for a single-day event', async () => {
    renderEvents({
      events: [
        createMultiDayEventFixture(),
        createEventFixture({ name: 'Innovation Day', event_date: '2026-08-15' }),
      ],
    })

    const festivalRow = (await screen.findByRole('row', { name: /Amritotsavam/i }))
    expect(within(festivalRow).getByText(/10 Aug 2026/i)).toBeInTheDocument()
    // `Sept?` because Intl's en-GB short month for September is "Sept", not
    // "Sep" — the one month where the three-letter assumption is wrong, and
    // invisible in every other fixture because they all use August. The
    // assertion still fails if the end date is absent, which is the property
    // under test.
    expect(within(festivalRow).getByText(/08 Sept? 2026/i)).toBeInTheDocument()

    const singleRow = await screen.findByRole('row', { name: /Innovation Day/i })
    // Exactly ONE rendering of the date — not "15 Aug 2026 – 15 Aug 2026".
    expect(within(singleRow).getAllByText(/15 Aug 2026/i)).toHaveLength(1)
  })
})
