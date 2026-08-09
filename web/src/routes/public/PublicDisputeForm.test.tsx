import { openOptions, selectByLabel, selectByValue } from '../../test/select'
/**
 * Red-phase component tests for Story 0.11's PUBLIC dispute form (AC 1, 2, 15,
 * 16, 17).
 *
 * Rendered through the REAL Application — QueryClientProvider -> AppBackdrop ->
 * AuthProvider -> RouterProvider — with a SIGNED-OUT session, for the same reason
 * 0.10's suite does: rendering the form component in isolation would skip the two
 * things most likely to break it, namely whether the affordance is reachable from
 * a "No record" slot at all, and whether an anonymous visitor is left alone.
 *
 * The session port FAILS the test if refresh() or signOut() is ever called. This
 * story adds a POST that can return 400, 409 and 429, and `createAuthorizedFetch`
 * retries once on 401 and signs out if the refresh fails — so a public form built
 * on the wrong client could sign a signed-in admin out of another tab. That is
 * invisible unless the port treats those calls as errors rather than as no-ops.
 *
 * NOT gated behind an env flag or a build tag. 0.9 shipped a suite behind
 * ATDD_ACTIVE_09 that nothing ever set.
 */
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../Application'
import { createPortalMemoryRouter } from '../../router'
import type { SessionPort } from '../../features/auth/session'
import { server } from '../../test/server'
import {
  publicAttendanceRoute,
  publicDay,
  publicRecord,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
  publicStudentRoll,
} from '../../../tests/support/factories/public-attendance'
import {
  disputeConfirmationCopy,
  disputeEmail,
  disputeError,
  disputeODDate,
  disputeReference,
  identityUnverified,
  publicDisputeReceipt,
  publicEventCollection,
} from '../../../tests/support/factories/student-disputes'

function anonymousSession(): SessionPort {
  return {
    accessToken: vi.fn(async () => null),
    refresh: vi.fn(async () => {
      throw new Error('the public dispute form must never refresh a session')
    }),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => {
      throw new Error('the public dispute form must never sign a visitor out')
    }),
    subscribe: vi.fn(() => () => undefined),
  }
}

type Installed = {
  authorizationHeaders: (string | null)[]
  submissions: Record<string, unknown>[]
  submitCalls: number
}

function installDisputeBackend(options: { submitStatus?: number; submitBody?: unknown } = {}): Installed {
  const installed: Installed = { authorizationHeaders: [], submissions: [], submitCalls: 0 }

  server.use(
    http.get('*/api/v1/public/students/search', ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      return HttpResponse.json(publicSearchResults())
    }),
    http.get('*/api/v1/public/students/:studentID/od-records', ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      return HttpResponse.json(publicRecordCollection())
    }),
    http.get('*/api/v1/public/events', ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      return HttpResponse.json(publicEventCollection())
    }),
    http.post('*/api/v1/public/disputes', async ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      installed.submitCalls += 1
      installed.submissions.push((await request.json()) as Record<string, unknown>)
      const status = options.submitStatus ?? 201
      if (status !== 201) {
        return HttpResponse.json(options.submitBody ?? disputeError('INTERNAL', 'Server problem.'), { status })
      }
      return HttpResponse.json(publicDisputeReceipt(), { status: 201 })
    }),
  )

  return installed
}

function renderDashboard(path = `${publicAttendanceRoute}?student=${publicStudentID}`) {
  const router = createPortalMemoryRouter([path])
  const session = anonymousSession()
  const result = render(<Application session={session} router={router} />)
  return { ...result, router, session }
}

/**
 * Opens the dispute form and reports SLOT 3.
 *
 * Slot 3 is the gap in the 0.10 fixture day — PRD Journey 3 verbatim. It used to
 * be clicked on its own "No record" row; the dashboard no longer renders the
 * slots a student was not marked for, so the slot is now stated INSIDE the form,
 * which is the one place it can be said for a day the student has no rows on at
 * all. The assertions that follow are unchanged: what is filed must be the slot
 * the student named.
 */
async function openDisputeFormForSlotThree(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))
  const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })
  await selectByValue(user, within(form).getByRole('combobox', { name: /slot/i }), '3')
  return form
}

describe('public dispute form', () => {
  // 0.11-WEB-001 — the affordance is always reachable, and the form opens with
  // personal fields READ-ONLY from the already-loaded records response (AC 1, T4).
  //
  // "Read-only" is asserted structurally, not by looking for a disabled-looking
  // style: a text input the visitor can retype is a form that files disputes for
  // other people, and it looks identical in a screenshot.
  test('[P0] opens from the report button with read-only personal fields sourced from the server', async () => {
    const user = userEvent.setup()
    installDisputeBackend()

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)

    const form = await openDisputeFormForSlotThree(user)

    // Every personal field is present and none of them is editable.
    for (const value of [publicStudentRoll, 'Ananya Rao', 'CSE', 'A', '2024']) {
      expect(
        within(form).getByText(value),
        `the form does not show ${value}; FR42 requires roll, name, branch, section and batch to be shown`,
      ).toBeInTheDocument()
    }

    // No editable personal field anywhere. The email box is the ONE thing the
    // visitor types, and the slot/event/category selects are choices, not
    // identity.
    const editablePersonal = within(form)
      .queryAllByRole('textbox')
      .filter((element) => /roll|name|branch|section|batch/i.test(element.getAttribute('aria-label') ?? element.id ?? ''))
    expect(
      editablePersonal,
      'a personal field is editable. FR42 auto-populates these FROM THE DATABASE precisely so a visitor cannot ' +
        'type somebody else in; an editable box makes the read-only claim cosmetic',
    ).toHaveLength(0)

    // The slot the student named is on the form, and so is the DAY, which is
    // pre-filled from the day the dashboard was showing. A dispute without a day
    // is ambiguous across the thirty an event can span, and the column exists
    // from creation precisely so the day is never guessed.
    //
    // Read off the combobox rather than by free text: the field is a Radix
    // listbox now, so its selected value lives in the trigger's own content and
    // a document-wide text match would also hit the option list behind it.
    expect(within(form).getByRole('combobox', { name: /slot/i })).toHaveTextContent(/slot\s*3\b/i)
    // THE DAY IT SHOWS, not merely that it shows one. A day control that exists
    // but is pre-filled with the wrong date files the dispute against a day the
    // student never named, and reads on screen exactly like a correct one.
    expect(
      within(form).getByRole('combobox', { name: /day/i }),
      'the form does not show which DAY is being disputed. Under 0.12a an event spans a date range, so a ' +
        'dispute without a day is ambiguous across thirty of them (amended 2026-08-06)',
    ).toHaveTextContent(/28 July 2026|2026-07-28|July 28/i)
  })

  // 0.11-WEB-002 — a successful submission shows NFR50's EXACT copy plus the
  // reference, within the same interaction, and cannot be resubmitted (AC 2,
  // AC 15).
  //
  // The copy is asserted verbatim rather than by regex: NFR50 fixes the sentence,
  // and "submitted successfully!" is a different promise from "a teacher will
  // review it".
  test('[P0] a successful submission announces the fixed confirmation copy with the reference', async () => {
    const user = userEvent.setup()
    const installed = installDisputeBackend()

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    const form = await openDisputeFormForSlotThree(user)

    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit|send|report/i }))

    // Scoped to the receipt panel. This assertion previously used a bare
    // findByRole('status'), which collided with 0.10's "As of" live region — and
    // that collision was resolved by deleting 0.10's role rather than by
    // narrowing the locator here.
    const receipt = await screen.findByRole('region', { name: /dispute receipt/i })
    const status = within(receipt).getByRole('status')
    expect(status).toHaveTextContent(disputeConfirmationCopy)
    expect(
      status,
      'the confirmation does not quote the reference. It is the only handle a student has on a dispute they ' +
        'can never look up again — there is no notification and no status endpoint (FR59 is dropped, T19)',
    ).toHaveTextContent(new RegExp(disputeReference, 'i'))

    // The body carries BOTH the typed email and the on-screen student id (T5).
    expect(installed.submissions).toHaveLength(1)
    expect(installed.submissions[0]).toMatchObject({
      submitted_email: disputeEmail,
      student_id: publicStudentID,
      slot: 3,
      // The day the OD belongs to (amended 2026-08-06). Today it is the event's
      // only date; under 0.12a it is one of up to thirty in the range, which is
      // why it is sent explicitly rather than inferred server-side from the
      // event — inferring it would file every dispute in a thirty-day event
      // against day one.
      od_date: disputeODDate,
    })

    // Not resubmittable. A student who taps twice must not file twice — the
    // server's 409 is the backstop, not the first line of defence.
    // The real button's accessible name is "Submit dispute". The previous
    // anchored /^submit$|^send$/ matched NEITHER, so queryByRole returned null
    // whether or not the form was still on screen.
    expect(screen.queryByRole('button', { name: /submit dispute/i })).not.toBeInTheDocument()
    expect(installed.submitCalls).toBe(1)
  })

  // 0.11-WEB-003 — the submit button is disabled while the request is in flight
  // (AC 15).
  //
  // Without this, a slow connection — the normal case on campus wifi at the end
  // of an event — produces two POSTs, and the second one comes back 409 on top of
  // the first one's success. The student sees a failure for a dispute that was
  // filed.
  test('[P0] the submit button is disabled in flight so a double tap cannot double-submit', async () => {
    const user = userEvent.setup()
    let release: (() => void) | undefined
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    const installed = installDisputeBackend()
    server.use(
      http.post('*/api/v1/public/disputes', async () => {
        installed.submitCalls += 1
        await gate
        return HttpResponse.json(publicDisputeReceipt(), { status: 201 })
      }),
    )

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    await openDisputeFormForSlotThree(user)
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    const submit = within(form).getByRole('button', { name: /submit|send|report/i })
    await user.click(submit)

    await waitFor(() => expect(submit).toBeDisabled())
    await user.click(submit)
    expect(installed.submitCalls).toBe(1)

    release?.()
    const receiptPanel = await screen.findByRole('region', { name: /dispute receipt/i })
    expect(within(receiptPanel).getByRole('status')).toHaveTextContent(disputeConfirmationCopy)
  })

  // 0.11-WEB-004 — every failure names its OWN reason and none of them can be
  // read as an attendance answer (AC 16).
  //
  // Five distinct branches. The identity refusal is the delicate one: it must not
  // confirm whether the address exists (T3) AND must not sound like "you were not
  // marked", which is the single most damaging thing this surface can say.
  test.each([
    {
      name: 'identity unverified (400)',
      status: 400,
      body: identityUnverified,
      expect: /could not verify that email|university address/i,
    },
    {
      name: 'already reported (409)',
      status: 409,
      body: disputeError('DISPUTE_ALREADY_OPEN', `You already reported this slot. Reference ${disputeReference}.`),
      expect: /already reported|already open|already submitted/i,
    },
    {
      name: 'rate limited (429)',
      status: 429,
      body: disputeError('RATE_LIMITED', 'Too many requests.'),
      expect: /too many|slow down|try again in a moment/i,
    },
    {
      name: 'server problem (500)',
      status: 500,
      body: disputeError('INTERNAL', 'Server problem.'),
      expect: /our problem|server|try again/i,
    },
  ])('[P0] $name is named distinctly and never reads as an attendance answer', async ({ status, body, expect: pattern }) => {
    const user = userEvent.setup()
    installDisputeBackend({ submitStatus: status, submitBody: body })

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    await openDisputeFormForSlotThree(user)
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit|send|report/i }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(pattern)

    // Never a silent success, and never text that implies the slot was credited.
    expect(screen.queryByText(disputeConfirmationCopy)).not.toBeInTheDocument()
    expect(alert).not.toHaveTextContent(/marked|credited|present|attendance recorded/i)
  })

  // 0.11-WEB-005 — the identity refusal cannot be read as a directory answer
  // (T3, AC 3's UI half).
  //
  // The server makes the three refusals byte-identical. The UI can still undo
  // that by rendering "we could not find that email" for one path — so the copy
  // is asserted NOT to contain any wording that confirms or denies existence.
  test('[P0] the identity refusal never confirms or denies whether the address exists', async () => {
    const user = userEvent.setup()
    installDisputeBackend({ submitStatus: 400, submitBody: identityUnverified })

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    await openDisputeFormForSlotThree(user)
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    await user.type(within(form).getByRole('textbox', { name: /email/i }), 'someone@gmail.com')
    await user.click(within(form).getByRole('button', { name: /submit|send|report/i }))

    const alert = await screen.findByRole('alert')
    for (const oracle of [
      /no such (email|address|student)/i,
      /not (registered|on the roster|found)/i,
      /does not exist/i,
      /unknown (email|address)/i,
      /wrong domain/i,
    ]) {
      expect(
        alert.textContent ?? '',
        `the refusal copy matches ${oracle} — that phrasing answers "does this address exist", which is the ` +
          'directory oracle T3 spends a whole locked decision closing. Say what to DO ("check that you used ' +
          'your university address"), never what was found',
      ).not.toMatch(oracle)
    }
  })

  // 0.11-WEB-006 — the dispute POST carries NO Authorization header, nothing is
  // persisted, and no email reaches the URL (AC 15, AC 17, NFR18).
  //
  // `createPublicApi()` takes no SessionPort BY CONSTRUCTION. This test is what
  // notices if the write was added to `createEventApi` instead — the tempting
  // move, because that is where every other POST in the app lives.
  test('[P0] submits with no Authorization header, persists nothing and keeps the email out of the URL', async () => {
    localStorage.clear()
    sessionStorage.clear()

    const user = userEvent.setup()
    const installed = installDisputeBackend()

    const { router } = renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    await openDisputeFormForSlotThree(user)
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit|send|report/i }))
    await screen.findByRole('region', { name: /dispute receipt/i })

    expect(installed.authorizationHeaders.length).toBeGreaterThan(0)
    for (const header of installed.authorizationHeaders) {
      expect(header).toBeNull()
    }

    // NFR18: `?student=<uuid>` stays the only query key. An email in a URL ends
    // up in browser history, proxy logs and every screenshot the student shares.
    const params = new URLSearchParams(router.state.location.search)
    expect([...params.keys()]).toEqual(['student'])
    expect(router.state.location.search).not.toContain('@')

    expect(sessionStorage.length).toBe(0)
    expect(localStorage.getItem('dispute')).toBeNull()
  })

  // 0.11-WEB-007 — a 4xx from the dispute POST never refreshes or signs anyone
  // out (AC 17).
  //
  // Asserted on the PORT, not inferred from an alert: a reused authorized fetch
  // would call refresh(), the throw would surface as a mutation error, and the
  // error alert would render — so waiting for the alert alone goes green in
  // precisely the scenario this test exists to catch.
  test('[P0] a refused submission never triggers a session refresh or a sign-out', async () => {
    const user = userEvent.setup()
    installDisputeBackend({ submitStatus: 400, submitBody: identityUnverified })

    const { session } = renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)
    await openDisputeFormForSlotThree(user)
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit|send|report/i }))

    await screen.findByRole('alert')
    expect(session.refresh).not.toHaveBeenCalled()
    expect(session.signOut).not.toHaveBeenCalled()
  })

  // 0.11-WEB-008 — a student with NO records at all can still open the form, and
  // the event list comes from GET /api/v1/public/events (T7, AC 7).
  //
  // This is the person the whole story is for. They have no slot row to click, so
  // the affordance cannot live only on a record — and they have no event to
  // derive, so the fallback read has to exist and has to be wired.
  test('[P0] a never-marked student can still file, using the public event list', async () => {
    const user = userEvent.setup()
    const installed = installDisputeBackend()
    server.use(
      http.get('*/api/v1/public/students/:studentID/od-records', () =>
        HttpResponse.json(publicRecordCollection({ days: [] })),
      ),
    )

    renderDashboard()
    expect(await screen.findByText(/no OD records/i)).toBeInTheDocument()

    await user.click(await screen.findByRole('button', { name: /report|missed|dispute/i }))
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    // The event options came from the public event read, including the archived
    // one (T8) — a student notices a missing slot after the event, which is
    // usually after it has been archived.
    const eventField = within(form).getByRole('combobox', { name: /event/i })
    const eventList = await openOptions(user, eventField)
    expect(within(eventList).getByRole('option', { name: /Onam Celebrations/i })).toBeInTheDocument()
    expect(
      within(eventList).getByRole('option', { name: /Sports Day Relay/i }),
      'the archived event is missing from the picker. Archiving is an administrative act, not a retraction, and ' +
        'a dispute is filed precisely after the fact (T8)',
    ).toBeInTheDocument()
    // Close it again so the day control below can open — only one list is open
    // at a time, exactly as a visitor experiences it.
    await user.keyboard('{Escape}')

    // THE DAY MUST BE A BOUNDED CHOICE, NOT A HIDDEN DERIVATION.
    //
    // `docs/multi-day-events-plan.md` §2.5 + Step 7: the dashboard groups by day,
    // and a student with records derives the disputed day from the group they
    // clicked. This student has NO records — the empty-day case — so there is no
    // group to derive from, and the day has to come from the event's own range.
    //
    // Today an event is one day, so `event_date` is the only valid answer and the
    // field can be pre-filled from it. Under 0.12a the event spans up to thirty
    // days and this becomes a picker bounded by [event_date, end_date]. Asserting
    // the field EXISTS and is bound to the event now is what stops the form being
    // built with the day silently derived from `event_date` — which works today
    // and has to be torn out the moment the range lands.
    const dayField = within(form).getByRole('combobox', { name: /day|date/i })
    expect(
      dayField,
      'the form offers no way to say WHICH DAY was missed. A student with no records has no day-group to ' +
        'derive it from (plan §7 + Step 7), so the day must come from the event range — a value derived ' +
        'invisibly from event_date works today and breaks the moment 0.12a widens the event',
    ).toBeInTheDocument()
    expect(
      within(await openOptions(user, dayField)).getByRole('option', { name: /2026-07-28|28 July|July 28/i }),
    ).toBeInTheDocument()
    await user.keyboard('{Escape}')

    expect(installed.authorizationHeaders.length).toBeGreaterThan(0)
  })

  // 0.11-WEB-009 — the day offered is constrained to the selected EVENT, not to
  // an open date input (plan §7).
  //
  // An unbounded `<input type="date">` lets an anonymous submitter file a dispute
  // dated 1970 against a one-day event. The database guard
  // (0.11-INT-002c) is the real defence; this is the half that stops a student
  // being told "invalid" by a server after typing something the form invited.
  test('[P0] the disputed day is bounded by the event, never a free date entry', async () => {
    const user = userEvent.setup()
    installDisputeBackend()
    server.use(
      http.get('*/api/v1/public/students/:studentID/od-records', () =>
        HttpResponse.json(publicRecordCollection({ days: [] })),
      ),
    )

    renderDashboard()
    await screen.findByText(/no OD records/i)
    await user.click(await screen.findByRole('button', { name: /report|missed|dispute/i }))
    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })

    // No unbounded free-text/date entry for the day. The event owns the range.
    const freeDateInputs = Array.from(form.querySelectorAll('input')).filter(
      (input) => input.type === 'date' && !input.min && !input.max,
    )
    expect(
      freeDateInputs,
      'the day is an unbounded date input. Whatever control is used, it must be bounded by the selected ' +
        "event's range — today that is the single event_date, under 0.12a it is [event_date, end_date]",
    ).toHaveLength(0)

    // Selecting the OTHER (archived, differently-dated) event must move the day
    // options with it. A day list that does not track the event is a form that
    // submits a date the event never covered.
    await selectByLabel(user, within(form).getByRole('combobox', { name: /event/i }), /Sports Day Relay/i)
    const dayField = within(form).getByRole('combobox', { name: /day|date/i })
    const dayList = await openOptions(user, dayField)
    expect(
      within(dayList).getByRole('option', { name: /2026-07-20|20 July|July 20/i }),
      'changing the event did not change the day options; the day must be scoped to the selected event',
    ).toBeInTheDocument()
    expect(within(dayList).queryByRole('option', { name: /2026-07-28|28 July|July 28/i })).not.toBeInTheDocument()
  })
})

// ---------------------------------------------------------------------------
// Multiple events / multiple days.
// ---------------------------------------------------------------------------
//
// Every fixture above holds ONE event on ONE day, which is why the defects below
// were invisible: with a single option, "default to the first event" and "use the
// event the visitor clicked" are indistinguishable.
describe('public dispute form across several events', () => {
  const secondEventID = '93000000-0000-4000-8000-00000000000f'
  const secondCategoryID = '94000000-0000-4000-8000-00000000000f'

  /** Two days, two events. The EARLIER one is listed first, as the API orders it. */
  function twoEventRecords() {
    return publicRecordCollection({
      days: [
        publicDay({
          od_date: '2026-07-20',
          records: [publicRecord(1, {
            event_id: secondEventID,
            event_name: 'Sports Day Relay',
            event_date: '2026-07-20',
            category_id: secondCategoryID,
            category_name: 'Athletics',
          })],
        }),
        publicDay(),
      ],
    })
  }

  function installTwoEventBackend(): Installed {
    const installed: Installed = { authorizationHeaders: [], submissions: [], submitCalls: 0 }
    server.use(
      http.get('*/api/v1/public/students/search', () => HttpResponse.json(publicSearchResults())),
      http.get('*/api/v1/public/students/:studentID/od-records', () => HttpResponse.json(twoEventRecords())),
      http.get('*/api/v1/public/events', () => HttpResponse.json(publicEventCollection())),
      http.post('*/api/v1/public/disputes', async ({ request }) => {
        installed.submitCalls += 1
        installed.submissions.push((await request.json()) as Record<string, unknown>)
        return HttpResponse.json(publicDisputeReceipt(), { status: 201 })
      }),
    )
    return installed
  }

  // 0.11-WEB-011 — the dispute is filed against the event and day the dashboard
  // is SHOWING (Task 6; the "which event am I disputing" rule 0.10's same-day
  // slot-collision bug established).
  //
  // The fixture lists 2026-07-20 (Sports Day Relay) FIRST and 2026-07-28 (Onam)
  // second, so `eventOptions[0]` is the relay. The dashboard opens on the later
  // day, which means a form that derived its event from the first option — the
  // defect 0.11's review found — files this dispute against the wrong event and,
  // via the effect that follows the event, the wrong day.
  test('[P0] the report is filed against the day on screen, not the first in the history', async () => {
    const user = userEvent.setup()
    const installed = installTwoEventBackend()

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)

    const form = await openDisputeFormForSlotThree(user)
    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit dispute/i }))

    await screen.findByRole('region', { name: /dispute receipt/i })
    expect(installed.submissions).toHaveLength(1)
    const submission = installed.submissions[0]
    expect(
      submission.od_date,
      'the dispute was filed against a DIFFERENT day than the one on screen. The form derived its event from ' +
        'eventOptions[0] — the first event across all days — and an effect then rewrote od_date to that ' +
        "event's date, so a student reporting the day they were looking at silently reported another",
    ).toBe(disputeODDate)
    expect(submission.event_id).toBe('93000000-0000-4000-8000-000000000001')
    expect(submission.slot).toBe(3)
  })

  // 0.11-WEB-012 — stepping the day filter moves the report with it.
  //
  // The day is no longer implied by a row that was clicked; it is the day the
  // dashboard is showing. So the control that changes the day has to change what
  // the form opens on, or a student who navigated back to Sports Day would file
  // their report against Onam without anything on screen saying so.
  test('[P0] reporting after stepping back a day files against that earlier day', async () => {
    const user = userEvent.setup()
    const installed = installTwoEventBackend()

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)

    await user.click(screen.getByRole('button', { name: /previous day/i }))
    await screen.findByText(/Sports Day Relay/i)

    const form = await openDisputeFormForSlotThree(user)
    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit dispute/i }))

    await screen.findByRole('region', { name: /dispute receipt/i })
    const submission = installed.submissions[0]
    expect(
      submission.od_date,
      'the day filter moved but the report did not follow it. The two are one piece of state precisely so they ' +
        'cannot disagree',
    ).toBe('2026-07-20')
    expect(submission.event_id).toBe(secondEventID)
    expect(submission.slot).toBe(3)
  })

  // 0.11-WEB-013 — a student who missed two slots can report both.
  test('[P0] the receipt offers a way back so a second slot can be reported', async () => {
    const user = userEvent.setup()
    const installed = installTwoEventBackend()

    renderDashboard()
    await screen.findAllByText(/Onam Celebrations/i)

    const form = await openDisputeFormForSlotThree(user)
    await user.type(within(form).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(form).getByRole('button', { name: /submit dispute/i }))

    const receipt = await screen.findByRole('region', { name: /dispute receipt/i })
    expect(receipt).toHaveTextContent(disputeConfirmationCopy)

    // The receipt panel had no exit at all, and `receipt` lives in the form's own
    // state — so every later report click kept rendering this same success and a
    // student who missed two slots could only ever report one.
    const again = within(receipt).getByRole('button', { name: /report another|close/i })
    await user.click(again)

    expect(screen.queryByRole('region', { name: /dispute receipt/i })).not.toBeInTheDocument()

    // The second report is a fresh form, not the first one's leftovers: a
    // different slot, filed as that slot.
    await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))
    const second = await screen.findByRole('form', { name: /report a missed slot/i })
    await selectByValue(user, within(second).getByRole('combobox', { name: /slot/i }), '5')
    await user.type(within(second).getByRole('textbox', { name: /email/i }), disputeEmail)
    await user.click(within(second).getByRole('button', { name: /submit dispute/i }))

    await screen.findByRole('region', { name: /dispute receipt/i })
    expect(installed.submissions.map((entry) => entry.slot)).toEqual([3, 5])
  })
})
