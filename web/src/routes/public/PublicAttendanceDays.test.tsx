/**
 * Red-phase component tests for the public dashboard's DAY grouping and the
 * dispute form's widened day picker (AC 11, 12, 15).
 *
 * Rendered through the REAL Application with a SIGNED-OUT session, matching
 * PublicDisputeForm.test.tsx: the session port FAILS the test if refresh() or
 * signOut() is ever called, because a public page built on the authorized client
 * can sign a signed-in admin out of another tab and that is invisible unless the
 * port treats those calls as errors.
 *
 * NOT gated behind an env flag or a build tag.
 */
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../Application'
import type { SessionPort } from '../../features/auth/session'
import { createPortalMemoryRouter } from '../../router'
import { server } from '../../test/server'
import {
  publicAttendanceRoute,
  publicMultiDayCollection,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
} from '../../../tests/support/factories/public-attendance'
import {
  multiDayDisputeDayCount,
  multiDayDisputeEnd,
  multiDayDisputeStart,
  multiDayPublicEvent,
  publicEvent,
  publicEventCollection,
} from '../../../tests/support/factories/student-disputes'

function anonymousSession(): SessionPort {
  return {
    accessToken: vi.fn(async () => null),
    refresh: vi.fn(async () => {
      throw new Error('the public dashboard must never refresh a session')
    }),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => {
      throw new Error('the public dashboard must never sign a visitor out')
    }),
    subscribe: vi.fn(() => () => undefined),
  }
}

function renderDashboard(collection = publicMultiDayCollection(), events = publicEventCollection([multiDayPublicEvent()])) {
  server.use(
    http.get('*/api/v1/public/students/search', () => HttpResponse.json(publicSearchResults())),
    http.get('*/api/v1/public/students/*/od-records', () => HttpResponse.json(collection)),
    http.get('*/api/v1/public/events', () => HttpResponse.json(events)),
  )
  render(
    <Application
      session={anonymousSession()}
      router={createPortalMemoryRouter([`${publicAttendanceRoute}?student=${publicStudentID}`])}
    />,
  )
}

describe('the public dashboard across a multi-day event', () => {
  // 0.12a-WEB-005 — three days of ONE event render as THREE day groups.
  //
  // Every record in the fixture carries the same `event_date` (the festival's
  // first day, 2026-08-10) and a different `od_date`. Grouping or keying on
  // `event_date` collapses all three onto one heading — which reads to the
  // student as two days of missing attendance on days they were marked.
  test('0.12a-WEB-005 each day of a festival gets its own heading, keyed on od_date', async () => {
    renderDashboard()

    // The PUBLIC screen has its own `formatDate` (PublicAttendanceScreen.tsx:734)
    // rendering "Wednesday, 19 August 2026" — NOT the portal's short
    // "19 Aug 2026". Asserting the portal's format here would fail on the
    // wording rather than on the grouping, which is the failure this suite must
    // never report.
    for (const heading of [/19 August 2026/i, /12 August 2026/i, /10 August 2026/i]) {
      expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument()
    }

    // Newest first, as 0.10 established and this story must not disturb.
    const headings = await screen.findAllByRole('heading', { name: /\d{1,2} \w+ 2026/i })
    expect(headings.map((node) => node.textContent)).toEqual([
      expect.stringMatching(/19 August 2026/i),
      expect.stringMatching(/12 August 2026/i),
      expect.stringMatching(/10 August 2026/i),
    ])
  })

  // 0.12a-WEB-006 — the dispute affordance reports the day the student CLICKED.
  //
  // 0.11's review found the dispute form filing against the wrong event and day
  // when a student had records on two dates. With thirty days on screen that is
  // no longer an edge case: every day-scoped control must take its event and its
  // day from the row that opened it, never from the first record in the history.
  test('0.12a-WEB-006 reporting a missed slot on day one pre-selects day one', async () => {
    renderDashboard()
    const user = userEvent.setup()

    const dayOne = (await screen.findByRole('heading', { name: /10 August 2026/i })).closest('section')
    expect(dayOne).not.toBeNull()

    await user.click(within(dayOne as HTMLElement).getAllByRole('button', { name: /report|missed|dispute/i })[0])

    const dayControl = await screen.findByRole('combobox', { name: /day|date/i })
    await waitFor(() => {
      expect(dayControl).toHaveValue('2026-08-10')
    })
  })

  // 0.12a-WEB-007 — the day picker enumerates the WHOLE range, and stays a
  // bounded <select>.
  //
  // T20/T23. 10 Aug -> 8 Sep inclusive is thirty options; a picker that renders
  // one, or 29, or 31, is a different bug each time and the count distinguishes
  // them. It must NOT become an `<input type="date">`: an unbounded date input on
  // an ANONYMOUS write path lets anyone file a dispute dated 1970, and the
  // database guard would then be the only thing standing between the teacher
  // inbox and rows no event ever covered.
  test('0.12a-WEB-007 the day picker offers every day in the range and remains bounded', async () => {
    renderDashboard()
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /report|missed|dispute/i }))[0])

    const dayControl = await screen.findByRole('combobox', { name: /day|date/i })
    expect(dayControl.tagName).toBe('SELECT')
    expect(dayControl).not.toHaveAttribute('type', 'date')

    const options = within(dayControl as HTMLElement).getAllByRole('option')
    expect(options).toHaveLength(multiDayDisputeDayCount)
    expect(options[0]).toHaveValue(multiDayDisputeStart)
    expect(options[options.length - 1]).toHaveValue(multiDayDisputeEnd)
  })

  // 0.12a-WEB-008 — a single-day event still offers exactly ONE day.
  //
  // The compatibility control for WEB-007. An enumeration written as a loop with
  // the wrong terminator produces zero options for a one-day event, and the form
  // then submits an empty `od_date` that the server refuses — breaking the flow
  // that works today for the overwhelmingly common case.
  test('0.12a-WEB-008 a one-day event offers exactly one day', async () => {
    renderDashboard(publicRecordCollection(), publicEventCollection())
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /report|missed|dispute/i }))[0])

    const dayControl = await screen.findByRole('combobox', { name: /day|date/i })
    const options = within(dayControl as HTMLElement).getAllByRole('option')
    expect(options).toHaveLength(1)
    expect(options[0]).toHaveValue('2026-07-28')
  })

  // 0.12a-WEB-009 — the day options RE-SCOPE when the event changes.
  //
  // T23's original requirement, now with teeth: a student who has records under
  // two events must not be able to submit a day from festival A against event B.
  // With one-day events both lists had one entry and any implementation passed;
  // with a thirty-day range the two lists are visibly different.
  test('0.12a-WEB-009 changing the event replaces the day options', async () => {
    // The single-day event has to be a DIFFERENT event, not just a different
    // row: both options were previously named Amritotsavam, so the lookup below
    // for "the option that is not Amritotsavam" could never find one and the
    // test could not reach its own assertion.
    renderDashboard(publicMultiDayCollection(), publicEventCollection([multiDayPublicEvent(), publicEvent()]))
    const user = userEvent.setup()

    await user.click((await screen.findAllByRole('button', { name: /report|missed|dispute/i }))[0])

    const eventControl = await screen.findByRole('combobox', { name: /event/i })
    const dayControl = await screen.findByRole('combobox', { name: /day|date/i })

    expect(within(dayControl as HTMLElement).getAllByRole('option')).toHaveLength(multiDayDisputeDayCount)

    // Switch to the single-day event the record history contributes.
    const otherOption = within(eventControl as HTMLElement)
      .getAllByRole('option')
      .find((option) => !/Amritotsavam/i.test(option.textContent ?? ''))
    expect(otherOption).toBeDefined()
    await user.selectOptions(eventControl, (otherOption as HTMLOptionElement).value)

    await waitFor(() => {
      expect(within(dayControl as HTMLElement).getAllByRole('option').length).toBeLessThan(multiDayDisputeDayCount)
    })
  })
})
