import { openedOptionValues, selectByLabel, selectByValue } from '../../test/select'
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
import { enumerateDays as portalEnumerateDays } from '../portal/events/format'
import { campusToday as portalCampusToday } from '../portal/live/campusDay'
import { campusToday as publicCampusToday, enumerateDays as publicEnumerateDays } from './days'
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

/**
 * Formats a YYYY-MM-DD exactly as PublicAttendanceScreen does.
 *
 * The screen renders dates in the VISITOR's locale — `toLocaleDateString(undefined, …)` —
 * which is correct for the product and makes any hard-coded English wording a test that
 * depends on the runner. "19 August 2026" on an en-GB machine is "Wednesday, August 19,
 * 2026" on the en-US CI runner, which is exactly how these assertions passed locally and
 * failed in CI. Playwright was pinned to en-GB for this reason; the unit suite never was.
 *
 * Deriving the expected string from the same formatter keeps the assertion on what this
 * suite is actually about — WHICH day each group is keyed on, and in what order — which is
 * what the comment on 0.12a-WEB-005 already says it must not trade away for wording.
 */
function dayHeading(iso: string): RegExp {
  const formatted = new Date(`${iso}T00:00:00`).toLocaleDateString(undefined, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  })
  return new RegExp(formatted.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'i')
}

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
  // 0.12a-WEB-005 — the dashboard shows ONE day, and the filter reaches all of
  // them.
  //
  // Every record in the fixture carries the same `event_date` (the festival's
  // first day, 2026-08-10) and a different `od_date`. Keying on `event_date`
  // collapses all three onto one heading — which reads to the student as two
  // days of missing attendance on days they were marked. That is still what this
  // asserts; what changed is that the three days are now reached one at a time
  // through the filter instead of being stacked down the page.
  test('0.12a-WEB-005 the day filter reaches every day of a festival, keyed on od_date', async () => {
    renderDashboard()
    const user = userEvent.setup()

    const filter = await screen.findByRole('combobox', { name: /^day$/i })
    // The filter offers every day the student HAS records on. Today is unioned
    // in as well, so this is a containment check rather than an exact set: the
    // point is that no day of the festival is unreachable.
    const offered = await openedOptionValues(user, filter)
    for (const day of ['2026-08-19', '2026-08-12', '2026-08-10']) {
      expect(offered, `${day} cannot be reached from the day filter`).toContain(day)
    }
    await user.keyboard('{Escape}')

    // ONE day is rendered at a time — not all of them stacked. Selecting each in
    // turn shows that day's heading and only that day's.
    //
    // The PUBLIC screen has its own `formatDate` rendering "Wednesday, 19 August
    // 2026" — NOT the portal's short "19 Aug 2026". Asserting the portal's
    // format here would fail on the wording rather than on the grouping, which
    // is the failure this suite must never report.
    for (const day of ['2026-08-19', '2026-08-12', '2026-08-10']) {
      await selectByValue(user, screen.getByRole('combobox', { name: /^day$/i }), day)
      const headings = await screen.findAllByRole('heading', { name: /2026/ })
      expect(headings.map((node) => node.textContent)).toEqual([
        expect.stringMatching(dayHeading(day)),
      ])
    }
  })

  // 0.12a-WEB-006 — the report form is filed against the day ON SCREEN.
  //
  // 0.11's review found the dispute form filing against the wrong event and day
  // when a student had records on two dates. The per-slot buttons are gone, so
  // the rule now attaches to the day filter: whichever day the student is
  // looking at is the day — and the event — the form opens on. Taking it from
  // the first record in the history instead is the same defect wearing a
  // different hat.
  test('0.12a-WEB-006 reporting from day one of the festival pre-selects day one', async () => {
    renderDashboard()
    const user = userEvent.setup()

    await selectByValue(user, await screen.findByRole('combobox', { name: /^day$/i }), '2026-08-10')
    await screen.findByRole('heading', { name: dayHeading('2026-08-10') })

    await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))

    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })
    const dayControl = within(form).getByRole('combobox', { name: /day|date/i })
    // The chosen day is what the control DISPLAYS. Its label carries the iso
    // date alongside the long form (see PublicAttendanceScreen), so asserting on
    // it still pins the exact day rather than merely "some day in August".
    await waitFor(() => {
      expect(dayControl).toHaveTextContent('2026-08-10')
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

    await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))

    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })
    // Scoped to the FORM. The dashboard now carries a day filter of its own, so
    // a page-wide "the day control" would match whichever one the query reached
    // first — and the two are bounded by different things on purpose.
    const dayControl = within(form).getByRole('combobox', { name: /day|date/i })
    // The control must remain a BOUNDED PICKER. It is no longer a native
    // `<select>` — the app draws its own dropdown — so the tag name is not the
    // property worth asserting; what matters is that the day can only be chosen
    // from an enumerated list. A free `<input type="date">` on this anonymous
    // write path would let anyone file a dispute dated 1970, leaving the
    // database guard as the only thing between the teacher inbox and rows no
    // event ever covered.
    expect(dayControl.tagName).not.toBe('INPUT')
    expect(dayControl).not.toHaveAttribute('type', 'date')

    const offered = await openedOptionValues(user, dayControl)
    expect(offered).toHaveLength(multiDayDisputeDayCount)
    expect(offered[0]).toBe(multiDayDisputeStart)
    expect(offered[offered.length - 1]).toBe(multiDayDisputeEnd)
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

    await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))

    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })
    const dayControl = within(form).getByRole('combobox', { name: /day|date/i })
    const offered = await openedOptionValues(user, dayControl)
    expect(offered).toEqual(['2026-07-28'])
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

    await user.click(await screen.findByRole('button', { name: /report a missed slot/i }))

    const form = await screen.findByRole('form', { name: /report a missed slot|dispute/i })
    const eventControl = within(form).getByRole('combobox', { name: /event/i })
    const dayControl = within(form).getByRole('combobox', { name: /day|date/i })

    // Only one dropdown is open at a time, so each list is read in turn.
    expect(await openedOptionValues(user, dayControl)).toHaveLength(multiDayDisputeDayCount)

    // Switch to the single-day event the record history contributes, named as
    // "the one that is not the festival". If a third event ever appears here
    // this throws on an ambiguous match rather than silently picking one, which
    // is the right failure — the test would no longer mean what it says.
    await selectByLabel(user, eventControl, /^(?!.*Amritotsavam).*$/i)

    await waitFor(async () => {
      expect((await openedOptionValues(user, dayControl)).length).toBeLessThan(multiDayDisputeDayCount)
    })
  })
})

/**
 * The two `enumerateDays` implementations must not drift.
 *
 * The public bundle keeps its own copy on purpose — it is served to the open
 * internet and deliberately imports nothing from the authenticated portal, the
 * same way it keeps its own `formatDate`. Both copies carry a comment saying
 * "change both together", and a comment is not a mechanism: this codebase's own
 * T8 reasoning is that a string literal kept in sync by prose eventually is not.
 *
 * These two functions bound two different controls — an admin's attendance day
 * filter and an ANONYMOUS public write path — so a divergence would show up as
 * a student being offered a day the database then refuses.
 */
describe('the two enumerateDays implementations agree', () => {
  test('agree across single days, ranges, boundaries and degenerate input', () => {
    const cases: Array<[string, string]> = [
      ['2026-08-15', '2026-08-15'],
      ['2026-08-15', '2026-08-16'],
      ['2026-08-10', '2026-09-08'],
      ['2026-02-27', '2026-03-02'],
      ['2024-02-27', '2024-03-02'],
      ['2026-12-30', '2027-01-02'],
      ['2026-08-15', ''],
      ['2026-08-15', '2026-08-14'],
      ['', '2026-08-15'],
    ]

    for (const [start, end] of cases) {
      expect(
        publicEnumerateDays(start, end),
        `enumerateDays drifted between the public and portal bundles for ${start}..${end}`,
      ).toEqual(portalEnumerateDays(start, end))
    }
  })

  // campusToday is duplicated for the same reason and needs the same guard. The
  // two bound different things — the portal's live screen and the public
  // dashboard's day filter — and a divergence would put a student and the
  // teacher who marked them on different days at the same moment.
  test('campusToday agrees across the public and portal bundles, including across midnight IST', () => {
    const instants = [
      new Date('2026-08-09T00:00:00Z'), // 05:30 IST, same day
      new Date('2026-08-09T18:29:59Z'), // 23:59:59 IST, still the 9th
      new Date('2026-08-09T18:30:00Z'), // 00:00 IST, now the 10th
      new Date('2026-12-31T18:30:00Z'), // a year boundary crossed by the offset
      new Date('2026-02-28T20:00:00Z'),
    ]
    for (const instant of instants) {
      expect(
        publicCampusToday(instant),
        `campusToday drifted between the public and portal bundles at ${instant.toISOString()}`,
      ).toBe(portalCampusToday(instant))
    }
    // And it is genuinely campus-local, not the runner's timezone: 18:30Z is
    // already tomorrow on campus.
    expect(publicCampusToday(new Date('2026-08-09T18:30:00Z'))).toBe('2026-08-10')
  })

  test('a full-length event enumerates every day of the range', () => {
    const days = publicEnumerateDays('2026-08-10', '2026-11-07')
    expect(days).toHaveLength(90)
    expect(days[0]).toBe('2026-08-10')
    expect(days[days.length - 1]).toBe('2026-11-07')
    expect(days).toEqual(portalEnumerateDays('2026-08-10', '2026-11-07'))
  })
})
