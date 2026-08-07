/**
 * Red-phase component tests for Story 0.11's portal dispute inbox (AC 11-14).
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
        portalDispute({ status: 'resolved', resolution_note: 'Corrected on the register.', resolved_at: '2026-07-29T04:00:00Z', resolved_by_name: 'Portal teacher' }),
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
      ['roll number', /CB\.SC\.U4CSE24101/i],
      ['student name', /Ananya Rao/i],
      ['branch/section', /CSE/i],
      ['batch', /2024/i],
      ['submitted email', /cb\.sc\.u4cse24101@cb\.students\.amrita\.edu/i],
      ['event', /Onam Celebrations/i],
      ['category', /Procession/i],
      ['slot', /slot\s*3\b/i],
      ['student note', /card did not scan/i],
      ['status', /open/i],
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
    const resolve = within(detail).getByRole('button', { name: /resolve|mark resolved/i })

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
    expect(installed.resolutions[0].body).toMatchObject({ resolution_note: 'Corrected on the register.' })
  })

  // 0.11-PORTAL-004 — the screen states plainly that resolving does NOT correct
  // the record (AC 14, T15).
  //
  // This is a copy assertion because the harm is a copy harm. A teacher who
  // believes the record was fixed will not do the thing that fixes it, and the
  // student is left with a "resolved" dispute and the same missing slot. 0.12
  // exists precisely because this story cannot repair the record.
  test('[P0] the resolution UI says the record itself is not corrected', async () => {
    const user = userEvent.setup()
    installPortalDisputeBackend()

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })
    await user.click(await screen.findByRole('button', { name: new RegExp(disputeReference, 'i') }))

    const detail = await screen.findByRole('region', { name: new RegExp(disputeReference, 'i') })
    expect(
      detail.textContent ?? '',
      'the resolution panel does not say that resolving records the decision without correcting the record. ' +
        'Correcting it is Story 0.12 (T15/AC 14) — silence here reads as "fixed"',
    ).toMatch(/does not (correct|change|fix)|records the decision|separate (action|step)/i)

    // And it must not claim the opposite.
    expect(detail).not.toHaveTextContent(/record (has been |was )?(corrected|fixed|updated)/i)
    expect(detail).not.toHaveTextContent(/marked present|credited/i)
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
    await user.click(within(detail).getByRole('button', { name: /resolve|mark resolved/i }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(pattern)
    expect(
      detail,
      'the dispute is showing as resolved after a refused resolution. Green only after a server 2xx — 0.6 ' +
        'settled that rule and 0.7 settled that failures stay sticky rather than being silently cleared',
    ).not.toHaveTextContent(/^resolved$/i)
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
    expect(screen.queryByText(/cb\.sc\.u4cse24101@/i)).not.toBeInTheDocument()
  })

  // 0.11-PORTAL-007 — the rendered order matches the SQL order (created_at DESC,
  // id DESC).
  //
  // 0.7's review caught an optimistic ordering that disagreed with the query, so
  // a row jumped position the moment the list refetched. Asserted with two rows
  // whose ordering is unambiguous.
  test('[P0] disputes render newest-first, matching the server ordering', async () => {
    const newer = portalDispute({
      dispute_id: '97000000-0000-4000-8000-000000000002',
      submitted_at: '2026-07-29T09:00:00Z',
      slot: 5,
    })
    const older = portalDispute({ submitted_at: '2026-07-28T17:05:00Z', slot: 3 })
    installPortalDisputeBackend({ disputes: portalDisputeCollection([newer, older]) })

    renderDisputesPortal()
    await screen.findByRole('heading', { name: /dispute/i })

    const rows = await screen.findAllByRole('button', { name: /^9700000/i })
    expect(rows.length).toBeGreaterThanOrEqual(2)
    expect(
      rows[0].textContent ?? '',
      'the first row is not the newest dispute. The SQL orders created_at DESC, id DESC; any client-side ' +
        'ordering must match it or rows move on every refetch (0.7)',
    ).toMatch(/97000000-0000-4000-8000-000000000002|9700000/i)
  })
})
