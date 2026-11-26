/**
 * Component tests for the portal dispute inbox.
 *
 * Rendered through the REAL Application on the REAL router, so "is /portal/disputes
 * registered above the `*` catch-all" and "does SessionBoundary admit a teacher"
 * are actually exercised. SessionBoundary DEFAULTS allowedRoles to ['admin'] —
 * copying an existing portal route and forgetting to widen it produces an
 * "Access denied" screen for every teacher, which is 0.9's flagship defect in its
 * web form and the exact failure T16 is written against.
 *
 * NOT gated behind an env flag or a build tag.
 */
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import { createPortalMemoryRouter } from '../../../router'
import type { SessionPort } from '../../../features/auth/session'
import { server } from '../../../test/server'
import { testBranches } from '../../../../tests/support/factories/event'
import {
  disputeError,
  disputeID,
  disputeReference,
  portalDispute,
  portalDisputeCollection,
  portalDisputesRoute,
} from '../../../../tests/support/factories/student-disputes'

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

type Installed = {
  resolutions: { disputeID: string; body: Record<string, unknown> }[]
  listRequests: URL[]
}

function installPortalDisputeBackend(
  options: {
    role?: 'teacher' | 'admin' | 'coordinator'
    disputes?: ReturnType<typeof portalDisputeCollection>
    listStatus?: number
    resolveStatus?: number
    resolveBody?: unknown
    pages?: Record<number, ReturnType<typeof portalDisputeCollection>>
  } = {},
): Installed {
  const role = options.role ?? 'teacher'
  const installed: Installed = { resolutions: [], listRequests: [] }

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '90000000-0000-4000-8000-000000000004',
        email: `${role}@amrita.edu`,
        name: `Portal ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: testBranches })),
    http.get('*/api/v1/disputes', ({ request }) => {
      installed.listRequests.push(new URL(request.url))
      if (options.listStatus && options.listStatus !== 200) {
        return HttpResponse.json(disputeError('RBAC_FORBIDDEN', 'Forbidden.'), { status: options.listStatus })
      }
      // Serve the page the client actually asked for, so a component pinned to
      // page 1 cannot pass a pagination test by accident.
      const page = Number(new URL(request.url).searchParams.get('page') ?? '1')
      if (options.pages) {
        return HttpResponse.json(options.pages[page] ?? { data: [], total: 0, page, page_size: 20 })
      }
      return HttpResponse.json(options.disputes ?? portalDisputeCollection())
    }),
    http.post('*/api/v1/disputes/:disputeID/resolution', async ({ params, request }) => {
      installed.resolutions.push({
        disputeID: String(params.disputeID),
        body: (await request.json()) as Record<string, unknown>,
      })
      const status = options.resolveStatus ?? 200
      if (status !== 200) {
        return HttpResponse.json(options.resolveBody ?? disputeError('DISPUTE_ALREADY_RESOLVED', 'Already resolved.'), { status })
      }
      return HttpResponse.json(
        // resolved_by_user_id, not resolved_by_name: the API returns the actor's
        // id, and the factory's hand-written type had invented a field the
        // server never sends.
        portalDispute({
          status: 'resolved',
          resolution_note: 'Corrected on the register.',
          resolved_at: '2026-07-29T04:00:00Z',
          resolved_by_user_id: '90000000-0000-4000-8000-000000000004',
        }),
      )
    }),
  )

  return installed
}

function renderDisputesPortal(path = portalDisputesRoute) {
  const router = createPortalMemoryRouter([path])
  return { ...render(<Application session={sessionPort()} router={router} />), router }
}

describe('portal dispute inbox', () => {
  // 0.11-PORTAL-001 — /portal/disputes is registered and a TEACHER can open it
  // (AC 11).
  //
  // Two failure modes are covered by one assertion. If the route is registered
  // AFTER the `*` catch-all in router.tsx it is unreachable and RoleLanding
  // renders instead; if SessionBoundary was left at its ['admin'] default the
  // teacher gets "Access denied". Both look like "the page is broken" and neither
  // is a compile error.
  test('[P0] a teacher can open /portal/disputes and see the inbox', async () => {
    installPortalDisputeBackend({ role: 'teacher' })

    renderDisputesPortal()

    expect(
      await screen.findByRole('heading', { name: /dispute/i }),
      'the disputes heading never rendered. Either /portal/disputes sits below the `*` catch-all in router.tsx ' +
        '(unreachable — RoleLanding renders instead) or SessionBoundary was left at its ["admin"] default, ' +
        'which denies every teacher (T16)',
    ).toBeInTheDocument()
    expect(screen.queryByText(/access denied/i)).not.toBeInTheDocument()
  })

  // 0.11-PORTAL-002 — a dispute expands to its FULL detail (FR36, AC 11).
  //
  // Each field is listed because FR36 enumerates them, and a list view that shows
  // only the student and the slot forces a teacher to go and ask what the student
  // actually said — which is the workflow this screen replaces.
  test('[P0] a dispute expands to student, email, event, category, slot, note and status', async () => {
    const user = userEvent.setup()
    installPortalDisputeBackend()

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })

    const row = await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') })
    await user.click(row)

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    for (const [label, value] of [
      ['roll number', /CB\.SC\.U4CSE99101/i],
      ['student name', /Ananya Rao/i],
      ['branch/section', /CSE/i],
      ['batch', /2024/i],
      ['submitted email', /cb\.sc\.u4cse99101@cb\.students\.amrita\.edu/i],
      ['event', /Onam Celebrations/i],
      ['category', /Procession/i],
      ['slot', /slot\s*3\b/i],
      ['student note', /card did not scan/i],
      ['status', /open/i],
      // AC 11 lists "status and submitted time". submitted_at was on the wire and
      // required by the contract, and simply never rendered — and this list,
      // which is the assertion for FR36's "expandable to full detail", omitted
      // it too, so the gap was invisible from both sides.
      ['submitted time', /july 2026|2026/i],
    ] as const) {
      expect(detail, `the expanded dispute does not show its ${label} (FR36)`).toHaveTextContent(value)
    }
  })

  // 0.11-PORTAL-003 — resolving requires a non-empty note and sends it (AC 12).
  //
  // The note IS the resolution. Because 0.12 owns the actual record repair, this
  // note is the only durable trace of the decision — an empty one closes a dispute
  // with no record of why.
  test('[P0] an empty note cannot be submitted and a real one is sent', async () => {
    const user = userEvent.setup()
    const installed = installPortalDisputeBackend()

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })
    await user.click(await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') }))

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    const resolve = within(detail).getByRole('button', { name: /approve/i })

    // Empty note: refused client-side, and nothing leaves the browser.
    await user.click(resolve)
    expect(installed.resolutions).toHaveLength(0)
    expect(await screen.findByRole('alert')).toHaveTextContent(/note|reason|required/i)

    // Whitespace only is the same answer — the server trims, and a UI that
    // accepts "   " sends a request it knows will fail.
    await user.type(within(detail).getByRole('textbox', { name: /note|resolution/i }), '   ')
    await user.click(resolve)
    expect(installed.resolutions).toHaveLength(0)

    await user.clear(within(detail).getByRole('textbox', { name: /note|resolution/i }))
    await user.type(within(detail).getByRole('textbox', { name: /note|resolution/i }), 'Corrected on the register.')
    await user.click(resolve)

    await waitFor(() => expect(installed.resolutions).toHaveLength(1))
    expect(installed.resolutions[0].disputeID).toBe(disputeID)
    // The OUTCOME travels with the note. Without it the server cannot tell an
    // approval from a refusal, and the choice between crediting a slot and
    // declining to would be made by a default rather than by the teacher.
    expect(installed.resolutions[0].body).toMatchObject({
      resolution_note: 'Corrected on the register.',
      outcome: 'approved',
    })
  })

  // Rejecting is a REAL, reachable decision, not the absence of approving.
  //
  // If the inbox only offered "approve", a teacher facing a claim they knew to
  // be false would either leave it open forever or approve it to clear the list
  // — and approving now manufactures attendance.
  test('[P0] rejecting sends the refusal and credits nothing', async () => {
    const user = userEvent.setup()
    const installed = installPortalDisputeBackend()

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })
    await user.click(await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') }))

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    await user.type(within(detail).getByRole('textbox', { name: /note|resolution/i }), 'The register shows the student absent.')
    await user.click(within(detail).getByRole('button', { name: /^reject/i }))

    await waitFor(() => expect(installed.resolutions).toHaveLength(1))
    expect(installed.resolutions[0].body).toMatchObject({
      resolution_note: 'The register shows the student absent.',
      outcome: 'rejected',
    })
  })

  // THE PANEL SAYS WHAT APPROVING DOES, BEFORE IT IS PRESSED.
  //
  // This assertion is INVERTED from what it used to be. While resolving was an
  // annotation, the copy harm was a teacher believing the record had been fixed
  // when it had not — so the panel had to say the record was untouched. Now
  // approving writes the attendance record, and the harm runs the other way: a
  // teacher who does not realise the button marks a student present cannot weigh
  // the claim properly. Either way the rule is the same — the screen must not be
  // silent about what happens to the student's attendance.
  test('[P0] the panel says approving marks the student present', async () => {
    const user = userEvent.setup()
    installPortalDisputeBackend()

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })
    await user.click(await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') }))

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    expect(
      detail.textContent ?? '',
      'the panel does not say that approving marks the student present. Silence here means a teacher cannot ' +
        'tell that the button writes an attendance record the student will see on the public dashboard',
    ).toMatch(/marks the student present|mark(ed)? present/i)
    expect(
      detail.textContent ?? '',
      'the panel does not say that rejecting changes no attendance',
    ).toMatch(/no attendance|changes no attendance|records the refusal/i)
  })

  // 0.11-PORTAL-005 — each resolution refusal is a distinct message and the row
  // does not flip to resolved on a failure (AC 13).
  //
  // A row that shows "resolved" after a 409 is worse than an error: the teacher
  // moves on believing they handled it, and the optimistic state disappears on
  // the next refetch.
  test.each([
    { name: 'already resolved (409)', status: 409, code: 'DISPUTE_ALREADY_RESOLVED', expect: /already resolved/i },
    { name: 'not found (404)', status: 404, code: 'DISPUTE_NOT_FOUND', expect: /not found|no longer/i },
    { name: 'out of branch (403)', status: 403, code: 'RBAC_FORBIDDEN', expect: /not (yours|allowed)|permission|another branch/i },
  ])('[P0] $name is named and leaves the row unresolved', async ({ status, code, expect: pattern }) => {
    const user = userEvent.setup()
    installPortalDisputeBackend({ resolveStatus: status, resolveBody: disputeError(code, 'Refused.') })

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })
    await user.click(await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') }))

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    await user.type(within(detail).getByRole('textbox', { name: /note|resolution/i }), 'Corrected on the register.')
    await user.click(within(detail).getByRole('button', { name: /approve/i }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(pattern)
    // Assert on the STATUS value and on the success paragraph, not on the whole
    // region's text. `/^resolved$/` is anchored against the region's entire
    // normalised textContent — a long run of labels and values — so it could
    // never match, before the bug or after it.
    const statusValue = within(detail).getByText(/^open$/i)
    expect(
      statusValue,
      'the dispute is showing as resolved after a refused resolution. Green only after a server 2xx — 0.6 ' +
        'settled that rule and 0.7 settled that failures stay sticky rather than being silently cleared',
    ).toHaveTextContent(/^open$/i)
    expect(
      within(detail).queryByText(/^Resolved:/),
      'the resolved-note panel rendered after a REFUSED resolution',
    ).not.toBeInTheDocument()
  })

  // 0.11-PORTAL-006 — a coordinator is refused, and the refusal is legible (AC 11).
  //
  // The 403 comes from the service, not the route (markAPI carries
  // RequirePrincipal but no role gate), so this is the browser-side half of the
  // same assertion the service unit test makes.
  test('[P0] a coordinator cannot reach the dispute inbox', async () => {
    installPortalDisputeBackend({ role: 'coordinator', listStatus: 403 })

    renderDisputesPortal()

    await waitFor(() => {
      const denied = screen.queryByText(/access denied|not allowed|permission/i)
      expect(denied, 'a coordinator was not refused the dispute inbox').toBeInTheDocument()
    })
    expect(screen.queryByText(/cb\.sc\.u4cse99101@/i)).not.toBeInTheDocument()
  })

  // 0.11-PORTAL-006b — the SERVICE's 403 is legible too (AC 11).
  //
  // The test above proves the ROUTE refuses a coordinator, and it proves only
  // that: SessionBoundary allowedRoles={['teacher','admin']} renders "Access
  // denied" before any request is made, so its `listStatus: 403` was never
  // reached and listFailureMessage's 403 branch — the thing its comment claimed
  // to be testing — went entirely unexercised. markAPI carries RequirePrincipal
  // but no role gate, so a teacher whose scope the service rejects gets a real
  // 403 over the wire, and it must not render as a blank list.
  test('[P0] a service-side 403 is named rather than shown as an empty inbox', async () => {
    installPortalDisputeBackend({ listStatus: 403 })

    renderDisputesPortal()

    const alert = await screen.findByRole('alert')
    expect(
      alert,
      'a 403 from the dispute service rendered without naming the reason; an empty inbox reads as "no ' +
        'disputes" rather than "you are not allowed to see them"',
    ).toHaveTextContent(/not allowed|permission|denied/i)
    expect(screen.queryByText(/no open disputes/i)).not.toBeInTheDocument()
  })

  // 0.11-PORTAL-007 — the rendered order matches the SQL order (created_at DESC,
  // id DESC).
  //
  // 0.7's review caught an optimistic ordering that disagreed with the query, so
  // a row jumped position the moment the list refetched. Asserted with two rows
  // whose ordering is unambiguous.
  test('[P0] disputes render newest-first, matching the server ordering', async () => {
    // DISTINCT reference prefixes. Both fixtures previously derived their
    // reference from a 97000000-… uuid, and the assertion's `|9700000`
    // alternative matched either of them — so reversing the server's order left
    // the test green.
    const newerID = '98000000-0000-4000-8000-000000000002'
    const olderID = '99000000-0000-4000-8000-000000000003'
    const newer = portalDispute({
      dispute_id: newerID,
      reference: newerID.slice(0, 8),
      submitted_at: '2026-07-29T09:00:00Z',
      slot: 5,
    })
    const older = portalDispute({
      dispute_id: olderID,
      reference: olderID.slice(0, 8),
      submitted_at: '2026-07-28T17:05:00Z',
      slot: 3,
    })
    installPortalDisputeBackend({ disputes: portalDisputeCollection([newer, older]) })

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })

    const rows = await screen.findAllByRole('button', { name: /^9[89]000000/i })
    expect(rows.length).toBeGreaterThanOrEqual(2)
    expect(
      rows[0].textContent ?? '',
      'the first row is not the newest dispute. The SQL orders created_at DESC, id DESC; the rendered order ' +
        'must match it or rows move on every refetch (0.7)',
    ).toContain(newerID.slice(0, 8))
    expect(rows[1].textContent ?? '').toContain(olderID.slice(0, 8))
  })
})

// 0.11-PORTAL-008 — the inbox can reach past the first page (AC 11).
//
// The list was hard-pinned to page 1 with a frozen query key, and `total` was
// fetched and never rendered — so a teacher with 21 open disputes could not see
// number 21 and nothing on screen suggested it existed. FR36's inbox is only an
// inbox if it holds everything.
describe('portal dispute inbox paging', () => {
  test('[P0] a second page of disputes is reachable', async () => {
    const user = userEvent.setup()
    const firstID = '98000000-0000-4000-8000-000000000021'
    const secondID = '99000000-0000-4000-8000-000000000022'
    const installed = installPortalDisputeBackend({
      pages: {
        1: { data: [portalDispute({ dispute_id: firstID, reference: firstID.slice(0, 8) })], total: 21, page: 1, page_size: 20 },
        2: { data: [portalDispute({ dispute_id: secondID, reference: secondID.slice(0, 8) })], total: 21, page: 2, page_size: 20 },
      },
    })

    renderDisputesPortal()
    await screen.findByRole('button', { name: new RegExp(firstID.slice(0, 8), 'i') })

    const next = await screen.findByRole('button', { name: /next/i })
    await user.click(next)

    expect(
      await screen.findByRole('button', { name: new RegExp(secondID.slice(0, 8), 'i') }),
      'the inbox never requested page 2; it was pinned to page 1 with a frozen query key, so disputes past ' +
        'the twentieth were unreachable with nothing on screen saying so',
    ).toBeInTheDocument()
    expect(installed.listRequests.some((url) => url.searchParams.get('page') === '2')).toBe(true)
  })
})
