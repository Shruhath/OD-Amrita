/**
 * Red-phase component tests for the Story 0.10 public attendance dashboard
 * (AC 9-13).
 *
 * These render the REAL Application — QueryClientProvider -> AppBackdrop ->
 * AuthProvider -> RouterProvider — with a SIGNED-OUT session, because that is
 * the only state this page will ever be seen in. Rendering the screen component
 * in isolation would skip the two things most likely to break it: whether
 * `/attendance` is reachable at all before the `*` catch-all, and whether
 * AuthProvider leaves an anonymous visitor alone.
 *
 * The session port returns null from accessToken() and FAILS the test if
 * refresh() or signOut() is ever called. A public 401/429 that signs somebody
 * out is the exact failure T12 exists to prevent, and it is invisible unless the
 * test treats those calls as errors rather than as no-ops.
 *
 * NOT gated behind an env flag. 0.9 shipped a suite behind ATDD_ACTIVE_09 that
 * nothing ever set.
 */
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../Application'
import { createPortalMemoryRouter } from '../../router'
import type { SessionPort } from '../../features/auth/session'
import { THEME_STORAGE_KEY } from '../../features/theme/theme'
import { server } from '../../test/server'
import {
  publicAttendanceRoute,
  publicDay,
  publicEmptyRecordCollection,
  publicError,
  publicRecord,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
  publicStudentRoll,
} from '../../../tests/support/factories/public-attendance'
import { selectByValue } from '../../test/select'
import { campusToday } from './days'

/**
 * A signed-out visitor. refresh/signOut are wired to fail the test rather than
 * to no-op: "the public page never signs anyone out" is only actually asserted
 * if calling them is an error.
 */
function anonymousSession(): SessionPort {
  return {
    accessToken: vi.fn(async () => null),
    refresh: vi.fn(async () => {
      throw new Error('the public page must never refresh a session')
    }),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => {
      throw new Error('the public page must never sign a visitor out')
    }),
    subscribe: vi.fn(() => () => undefined),
  }
}

type Installed = {
  authorizationHeaders: (string | null)[]
  searchQueries: string[]
}

function installPublicBackend(
  options: { records?: unknown; recordsStatus?: number; searchStatus?: number } = {},
): Installed {
  const installed: Installed = { authorizationHeaders: [], searchQueries: [] }

  server.use(
    http.get('*/api/v1/public/students/search', ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      installed.searchQueries.push(new URL(request.url).searchParams.get('q') ?? '')
      if (options.searchStatus && options.searchStatus !== 200) {
        return HttpResponse.json(publicError('RATE_LIMITED', 'Too many searches.'), { status: options.searchStatus })
      }
      return HttpResponse.json(publicSearchResults())
    }),
    http.get('*/api/v1/public/students/:studentID/od-records', ({ request }) => {
      installed.authorizationHeaders.push(request.headers.get('Authorization'))
      if (options.recordsStatus && options.recordsStatus !== 200) {
        return HttpResponse.json(publicError('STUDENT_NOT_FOUND', 'No such student.'), { status: options.recordsStatus })
      }
      return HttpResponse.json(options.records ?? publicRecordCollection())
    }),
  )

  return installed
}

/**
 * The keys actually STORED, via the Storage API.
 *
 * Object.keys(localStorage) is not that list under jsdom — it returns the
 * interface's own methods (getItem, setItem, …) alongside any stored key, so an
 * assertion built on it compares against noise.
 */
function storageKeys(storage: Storage): string[] {
  const keys: string[] = []
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i)
    if (key !== null) keys.push(key)
  }
  return keys.sort()
}

/**
 * Renders the app on a memory router and hands the ROUTER back.
 *
 * window.location never changes under a memory router, so an assertion about
 * `?student=` has to read the router's own location — checking window.location
 * would pass no matter what the page did, which is worse than no assertion.
 */
function renderPublicRoute(path = publicAttendanceRoute) {
  const router = createPortalMemoryRouter([path])
  // The session is handed back so a test can assert on the PORT rather than on
  // a symptom. "refresh() throws, so a test that finishes must not have called
  // it" is not sound — the throw becomes a rejected promise the client renders
  // as an error alert, which is exactly what a passing assertion waits for.
  const session = anonymousSession()
  const result = render(<Application session={session} router={router} />)
  return { ...result, router, session }
}

describe('public attendance dashboard', () => {
  // 0.10-WEB-001 — the page renders for a visitor with no session at all, and
  // shows no sign-in affordance (AC 9).
  //
  // SessionBoundary defaults allowedRoles to ['admin'], so a public route that
  // accidentally sits inside it renders "Access denied" to every student. This
  // is 0.9's flagship defect in its web form.
  test('[P0] renders for a signed-out visitor with no sign-in prompt and no blocking session check', async () => {
    installPublicBackend()

    renderPublicRoute()

    expect(await screen.findByRole('searchbox', { name: /roll number|name|student/i })).toBeInTheDocument()
    // No sign-in WALL. Scoped to a button on purpose: that is the shape
    // SessionBoundary's card takes, and a card here means the route was wrapped
    // and every student is refused. A LINK away to the home page is a different
    // thing entirely — it gates nothing, and the test below requires one.
    expect(screen.queryByRole('button', { name: /sign in|log in|continue with microsoft/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/access denied/i)).not.toBeInTheDocument()
  })

  test('[P1] jumps to the student search when a visitor wants to find their name', async () => {
    const scrollIntoView = vi.fn()
    const user = userEvent.setup()
    installPublicBackend()
    renderPublicRoute()

    const searchBox = await screen.findByRole('searchbox', { name: /roll number|name|student/i })
    const searchSection = screen.getByRole('region', { name: /find a student/i })
    Object.defineProperty(searchSection, 'scrollIntoView', { configurable: true, value: scrollIntoView })

    await user.click(screen.getByRole('button', { name: /find my name/i }))

    expect(scrollIntoView).toHaveBeenCalledTimes(1)
    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    expect(searchBox).toHaveFocus()
  })

  test('[P1] avoids animated scrolling when a visitor prefers reduced motion', async () => {
    const scrollIntoView = vi.fn()
    vi.stubGlobal('matchMedia', vi.fn().mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }))

    try {
      const user = userEvent.setup()
      installPublicBackend()
      renderPublicRoute()

      const searchSection = await screen.findByRole('region', { name: /find a student/i })
      Object.defineProperty(searchSection, 'scrollIntoView', { configurable: true, value: scrollIntoView })

      await user.click(screen.getByRole('button', { name: /find my name/i }))

      expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'start' })
    } finally {
      vi.unstubAllGlobals()
    }
  })

  // 0.10-WEB-002 — the minimum-length gate is a HINT before the user has typed
  // enough, not an error, and no request is sent under 3 characters (AC 10).
  //
  // An error state on an input the visitor has barely started filling reads as
  // "you did something wrong" when they have simply not finished.
  test('[P0] does not search under three characters and shows a hint rather than an error', async () => {
    const user = userEvent.setup()
    const installed = installPublicBackend()

    renderPublicRoute()

    const searchbox = await screen.findByRole('searchbox', { name: /roll number|name|student/i })
    await user.type(searchbox, 'CB')

    expect(installed.searchQueries).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText(/at least 3|3 characters|keep typing/i)).toBeInTheDocument()

    await user.type(searchbox, '.SC')
    await waitFor(() => expect(installed.searchQueries.length).toBeGreaterThan(0))
  })

  // 0.10-WEB-003 — selecting a result loads that student's records, and the
  // selected student is reflected in the URL so a shared link reopens the view
  // (AC 10).
  test('[P0] selecting a result loads the records and puts the student id in the URL', async () => {
    const user = userEvent.setup()
    installPublicBackend()

    const { router } = renderPublicRoute()

    const searchbox = await screen.findByRole('searchbox', { name: /roll number|name|student/i })
    await user.type(searchbox, publicStudentRoll)

    await user.click(await screen.findByRole('option', { name: new RegExp(publicStudentRoll.replace(/\./g, '\\.'), 'i') }))

    expect(await screen.findAllByText(/Onam Celebrations/i)).not.toHaveLength(0)

    // The selected student must land in the URL, or a refresh drops the visitor
    // back to an empty search box and a shared link opens nothing (AC 10).
    await waitFor(() => {
      expect(new URLSearchParams(router.state.location.search).get('student')).toBe(publicStudentID)
    })

    // NFR18: the query string carries a uuid and nothing else. An email or a
    // roll number in a URL ends up in browser history, proxy logs and every
    // screenshot the student shares.
    const params = new URLSearchParams(router.state.location.search)
    expect([...params.keys()]).toEqual(['student'])
  })

  // 0.10-WEB-004 — a deep link with ?student=<uuid> opens straight onto that
  // student's records (AC 10). This is the half of URL state that a refresh and
  // a shared WhatsApp link actually exercise.
  test('[P0] a ?student= deep link reopens the same student without searching again', async () => {
    const installed = installPublicBackend()

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    expect(await screen.findAllByText(/Onam Celebrations/i)).not.toHaveLength(0)
    expect(installed.searchQueries).toHaveLength(0)
  })

  // 0.10-WEB-005 — ONLY the marked slots are rendered, and there is NO amber
  // pending state (AC 11, T14).
  //
  // The fixture day is PRD Journey 3: slots 1, 2 and 4 present, slot 3 missing.
  // The gap is still the information — a student reads 1, 2 and 4 and sees that
  // 3 is not among them — but it is now conveyed by the three rows that exist
  // rather than by nine rows saying "No record", which is what the twelve-slot
  // grid produced for every day of a student's history at once.
  test('[P0] renders only the marked slots, with no no-record filler and no pending state', async () => {
    installPublicBackend()

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    await screen.findAllByText(/Onam Celebrations/i)

    for (const slot of [1, 2, 4]) {
      expect(
        screen.getByText(new RegExp(`slot\\s*${slot}\\b`, 'i')),
        `slot ${slot} was marked and is not on screen`,
      ).toBeInTheDocument()
    }

    // The unmarked slots are ABSENT, not rendered as filler. Slot 3 is PRD
    // Journey 3's gap; 5..12 are the rest of the day the student was never
    // marked for and never asked about.
    for (const slot of [3, 5, 6, 7, 8, 9, 10, 11, 12]) {
      expect(
        screen.queryByText(new RegExp(`slot\\s*${slot}\\b`, 'i')),
        `slot ${slot} holds no record and must not be rendered`,
      ).not.toBeInTheDocument()
    }
    expect(screen.queryByText(/no record/i)).not.toBeInTheDocument()

    // Epic 0 has no async path at all — AR8/AR9 are deferred and there is no
    // queue. A "pending/syncing" state would tell a student to wait for a sync
    // that does not exist.
    expect(screen.queryByText(/pending|syncing|not yet synced|processing|queued/i)).not.toBeInTheDocument()

    // The marker is a name and a role, never an email or a user id.
    expect(screen.getAllByText(/Portal Coordinator/i)).not.toHaveLength(0)
    expect(screen.queryByText(/@amrita\.edu/i)).not.toBeInTheDocument()
  })

  // 0.10-WEB-005c — the day filter is the only way more than one day is on
  // screen, and it defaults to a day that has marks.
  //
  // The screen used to stack every day of a student's history down the page.
  // With a thirty-day festival that is a wall of mostly-empty rows, so the day
  // became a filter — which makes "which day am I looking at" a thing the page
  // must state and a thing the student must be able to change.
  test('[P0] shows one day at a time and steps through the rest with the arrows', async () => {
    const user = userEvent.setup()
    installPublicBackend({
      records: publicRecordCollection({
        days: [
          publicDay({ od_date: '2026-07-28', records: [publicRecord(1, { od_date: '2026-07-28' })] }),
          publicDay({
            od_date: '2026-07-20',
            records: [publicRecord(5, {
              od_date: '2026-07-20',
              record_id: '66000000-0000-4000-8000-000000000720',
              event_name: 'Sports Day Relay',
            })],
          }),
        ],
      }),
    })

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    // Opens on the most recent day that HAS marks. Opening on an empty today
    // would render "nothing marked" as the first thing a student sees, and this
    // page must never imply an absence it was not asked about.
    await screen.findByText(/Onam Celebrations/i)
    expect(screen.queryByText(/Sports Day Relay/i)).not.toBeInTheDocument()

    // ‹ steps BACKWARDS in time, which is the direction the arrow points.
    await user.click(screen.getByRole('button', { name: /previous day/i }))

    await waitFor(() => expect(screen.getByText(/Sports Day Relay/i)).toBeInTheDocument())
    expect(screen.queryByText(/Onam Celebrations/i)).not.toBeInTheDocument()

    // And back again.
    await user.click(screen.getByRole('button', { name: /next day/i }))
    await waitFor(() => expect(screen.getByText(/Onam Celebrations/i)).toBeInTheDocument())
  })

  // 0.10-WEB-005d — a day with no marks says so, in words that cannot be read as
  // "this student has no records".
  //
  // Today is always reachable from the filter even when it is empty, which is
  // exactly the state a student checks during an event. Rendering that as the
  // same sentence the never-marked student sees would answer a question nobody
  // asked.
  test('[P0] an empty day is worded differently from a student with no records at all', async () => {
    const user = userEvent.setup()
    installPublicBackend()

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)
    await screen.findAllByText(/Onam Celebrations/i)

    await selectByValue(user, screen.getByRole('combobox', { name: /^day$/i }), campusToday())

    expect(await screen.findByText(/no OD was marked on this day/i)).toBeInTheDocument()
    expect(screen.queryByText(/no OD records found/i)).not.toBeInTheDocument()
  })

  // 0.10-WEB-005b — two events on the SAME DAY may each hold the same slot, and
  // both marks must be shown (AC 4, AC 11).
  //
  // Uniqueness in the database is (student_id, event_id, slot) — per EVENT —
  // while a "day" here is a calendar date, so a festival day with two events can
  // legitimately produce two slot-4 records for one student. Keying the render
  // on the slot number alone keeps only the last, and the mark that loses reads
  // as "No record": the page telling a student they were not marked when they
  // were. Every other fixture avoids the collision by construction, so without
  // this case nothing in the suite exercises it.
  test('[P0] renders both marks when one day holds two records in the same slot', async () => {
    installPublicBackend({
      records: publicRecordCollection({
        days: [publicDay({
          records: [
            publicRecord(4),
            publicRecord(4, {
              record_id: '66000000-0000-4000-8000-000000000404',
              event_id: '93000000-0000-4000-8000-000000000002',
              event_name: 'Sports Day Relay',
              category_id: '94000000-0000-4000-8000-000000000002',
              category_name: 'Athletics',
              marked_by_name: 'Second Coordinator',
            }),
          ],
        })],
      }),
    })

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    const slotFour = (await screen.findAllByRole('listitem')).find((item) =>
      /slot\s*4:/i.test(item.textContent ?? ''),
    )
    expect(slotFour).toBeDefined()
    expect(slotFour).toHaveTextContent(/Onam Celebrations/i)
    expect(slotFour).toHaveTextContent(/Sports Day Relay/i)
    expect(slotFour).not.toHaveTextContent(/No record/i)
  })

  // 0.10-WEB-006 — an empty result is "no OD records found", rendered ONLY after
  // a successful empty response, and a 404 says something different (AC 12).
  //
  // Collapsing these is the most damaging wrong answer this page can give: a
  // student who mistyped a roll number would be told they were not marked.
  test('[P0] an empty read and a 404 are different messages', async () => {
    installPublicBackend({ records: publicEmptyRecordCollection() })

    const { unmount } = renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)
    expect(await screen.findByText(/no OD records/i)).toBeInTheDocument()
    unmount()

    installPublicBackend({ recordsStatus: 404 })
    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/no such student|not found|check the roll/i)
    expect(alert).not.toHaveTextContent(/no OD records/i)
  })

  // 0.10-WEB-007 — a 429 names the reason and offers a retry, and NEVER a blank
  // page or a silent empty result (AC 12).
  test('[P0] a rate-limited search explains itself and offers a retry', async () => {
    const user = userEvent.setup()
    installPublicBackend({ searchStatus: 429 })

    renderPublicRoute()

    await user.type(await screen.findByRole('searchbox', { name: /roll number|name|student/i }), publicStudentRoll)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/too many|try again|slow down/i)
    expect(screen.getByRole('button', { name: /retry|try again/i })).toBeInTheDocument()
    expect(alert).not.toHaveTextContent(/not marked|no records/i)
  })

  // 0.10-WEB-008 — the browser sends NO Authorization header on public calls,
  // and nothing from this page is persisted (AC 13).
  //
  // createAuthorizedFetch retries once on 401 and calls session.signOut() if the
  // refresh fails. Reusing it here would let a public page sign a signed-in
  // admin out of another tab — which is why T12 requires a separate
  // createPublicApi() with no SessionPort at all.
  test('[P0] sends no Authorization header and writes nothing to storage', async () => {
    localStorage.clear()
    sessionStorage.clear()

    const installed = installPublicBackend()

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    await screen.findAllByText(/Onam Celebrations/i)

    expect(installed.authorizationHeaders.length).toBeGreaterThan(0)
    for (const header of installed.authorizationHeaders) {
      expect(header).toBeNull()
    }
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)

    // AC 13 is about the page not persisting IDENTITY or ROSTER data, and the
    // load-time snapshot above cannot tell the difference between "writes
    // nothing" and "has not written yet". The page ships a theme toggle, so
    // exercise it and pin what a write is ALLOWED to be: the theme preference,
    // and nothing else. A student's roll number, a search result or a token
    // appearing here would fail this.
    await userEvent.setup().click(screen.getByRole('button', { name: /switch to (dark|light) theme/i }))

    expect(storageKeys(localStorage)).toEqual([THEME_STORAGE_KEY])
    expect(sessionStorage.length).toBe(0)
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toMatch(/^(light|dark)$/)
  })

  // 0.10-WEB-009 — a public 401 does not sign anybody out (AC 13).
  //
  // The anonymousSession() port throws from refresh() and signOut(), so this
  // passes only if the public client genuinely never reaches for them.
  test('[P0] a 401 from a public call never triggers a refresh or a sign-out', async () => {
    installPublicBackend({ recordsStatus: 401 })

    const { session } = renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    // Asserted on the PORT, not inferred from the alert. A reused authorized
    // fetch would call refresh(), the throw would surface as a query error, and
    // RetryableAlert would render role="alert" — so waiting for the alert alone
    // goes green in precisely the scenario this test exists to catch.
    expect(session.refresh).not.toHaveBeenCalled()
    expect(session.signOut).not.toHaveBeenCalled()
  })
})

/**
 * The way out.
 *
 * THE GAP THIS CLOSES. `/attendance` is reachable directly — a shared link, a
 * bookmark, a QR code on a noticeboard — and it had no route to anywhere else.
 * A coordinator who landed here had to edit the URL to reach sign-in, which is
 * the same friction the front door was built to remove; it was simply still
 * present one screen further in.
 */
describe('leaving the public dashboard', () => {
  test('[P0] a visitor can get back to the home page', async () => {
    const user = userEvent.setup()
    installPublicBackend()
    const { router } = renderPublicRoute()
    await screen.findByRole('searchbox', { name: /roll number|name|student/i })

    await user.click(screen.getByRole('link', { name: /home/i }))

    // The ROUTER's location, not window.location — see renderPublicRoute.
    expect(
      router.state.location.pathname,
      'the home link did not navigate. A dashboard with no way off it leaves anyone who is not a student ' +
        'editing the URL by hand',
    ).toBe('/')
  })

  test('[P0] staff are told where signing in lives', async () => {
    installPublicBackend()
    renderPublicRoute()
    await screen.findByRole('searchbox', { name: /roll number|name|student/i })

    // A LINK, not a button: it is a destination, so it must be openable in a new
    // tab and reachable by the browser controls people actually use.
    const signIn = screen.getByRole('link', { name: /sign in/i })
    expect(signIn).toHaveAttribute('href', '/')
  })
})
