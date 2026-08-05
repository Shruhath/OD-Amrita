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
import { server } from '../../test/server'
import {
  publicAttendanceRoute,
  publicEmptyRecordCollection,
  publicError,
  publicRecordCollection,
  publicSearchResults,
  publicStudentID,
  publicStudentRoll,
} from '../../../tests/support/factories/public-attendance'

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
 * Renders the app on a memory router and hands the ROUTER back.
 *
 * window.location never changes under a memory router, so an assertion about
 * `?student=` has to read the router's own location — checking window.location
 * would pass no matter what the page did, which is worse than no assertion.
 */
function renderPublicRoute(path = publicAttendanceRoute) {
  const router = createPortalMemoryRouter([path])
  const result = render(<Application session={anonymousSession()} router={router} />)
  return { ...result, router }
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
    expect(screen.queryByRole('button', { name: /sign in|log in|continue with microsoft/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/access denied/i)).not.toBeInTheDocument()
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

  // 0.10-WEB-005 — every slot 1..12 is rendered, present slots are visually
  // distinct from gaps, and there is NO amber pending state (AC 11, T14).
  //
  // The fixture day is PRD Journey 3: slots 1, 2 and 4 present, slot 3 missing.
  // Rendering only the present rows would let a student conclude they were
  // marked for everything, which is the opposite of what this page is for.
  test('[P0] renders slots 1..12 with an explicit no-record state and no pending state', async () => {
    installPublicBackend()

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    await screen.findAllByText(/Onam Celebrations/i)

    for (const slot of [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12]) {
      expect(screen.getByText(new RegExp(`slot\\s*${slot}\\b`, 'i'))).toBeInTheDocument()
    }

    // Slot 3 is the gap. It must say so, not merely be absent.
    expect(screen.getAllByText(/no record/i).length).toBeGreaterThan(0)

    // Epic 0 has no async path at all — AR8/AR9 are deferred and there is no
    // queue. A "pending/syncing" state would tell a student to wait for a sync
    // that does not exist.
    expect(screen.queryByText(/pending|syncing|not yet synced|processing|queued/i)).not.toBeInTheDocument()

    // The marker is a name and a role, never an email or a user id.
    expect(screen.getAllByText(/Portal Coordinator/i)).not.toHaveLength(0)
    expect(screen.queryByText(/@amrita\.edu/i)).not.toBeInTheDocument()
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
  })

  // 0.10-WEB-009 — a public 401 does not sign anybody out (AC 13).
  //
  // The anonymousSession() port throws from refresh() and signOut(), so this
  // passes only if the public client genuinely never reaches for them.
  test('[P0] a 401 from a public call never triggers a refresh or a sign-out', async () => {
    installPublicBackend({ recordsStatus: 401 })

    renderPublicRoute(`${publicAttendanceRoute}?student=${publicStudentID}`)

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    // If the authorized fetch had been reused, refresh()/signOut() would have
    // thrown by now and this assertion would never be reached.
  })
})
