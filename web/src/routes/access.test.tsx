import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../test/server'
import { Application } from '../Application'
import type { SessionPort } from '../features/auth/session'
import { createPortalMemoryRouter } from '../router'
import { ROUTE_ROLES, sectionsFor } from './access'

/**
 * The navigation must never offer a link the role cannot follow.
 *
 * WHAT WENT WRONG: the portal bar was a fixed list of five links shown to
 * everyone, while each route enforced its own separate rule. A teacher signing
 * in was offered Events, Teachers, Students and Card populator — four screens
 * that answer "Access denied" — and was NOT offered the marking screen, the one
 * thing they can actually use, because it had never been added to the list.
 *
 * These tests do not compare the nav against the role map, which would only
 * prove the map equals itself. They FOLLOW each offered link and assert the
 * screen opens, and separately assert the hidden ones would have been refused.
 */

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function signedInAs(role: string) {
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `Portal ${role}`,
        role,
      }),
    ),
    // Enough of the portal's reads to let any screen render its frame.
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [] })),
    http.get('*/api/v1/admin/events', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/admin/teachers', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/admin/students', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
    http.get('*/api/v1/portal/mark/events', () => HttpResponse.json({ data: [] })),
    http.get('*/api/v1/disputes', () => HttpResponse.json({ data: [], total: 0, page: 1, page_size: 20 })),
  )
}

function renderAt(path: string) {
  return render(<Application session={sessionPort()} router={createPortalMemoryRouter([path])} />)
}

describe('what each role is offered', () => {
  test('[P0] a teacher is offered only the screens a teacher can open', () => {
    expect(sectionsFor('teacher').map((section) => section.label)).toEqual(['Live', 'Mark', 'Disputes'])
  })

  test('[P0] a teacher is offered the marking screen at all', () => {
    // It was missing from the bar entirely, so the one screen a teacher can use
    // was the one they could not navigate to.
    expect(sectionsFor('teacher').map((section) => section.to)).toContain('/portal/mark')
  })

  test('[P0] an admin keeps every section', () => {
    expect(sectionsFor('admin').map((section) => section.label)).toEqual([
      'Events',
      'Live',
      'Mark',
      'Disputes',
      'Teachers',
      'Students',
      'Card populator',
    ])
  })

  test('[P0] a coordinator and a signed-out visitor are offered no portal sections', () => {
    // The coordinator surface is not part of the portal chrome.
    expect(sectionsFor('coordinator')).toEqual([])
    expect(sectionsFor(undefined)).toEqual([])
  })
})

describe('following the offered links', () => {
  // The assertion that is not circular: open each link the role is given and
  // confirm the screen is not a refusal.
  test.each(['teacher', 'admin'])('[P0] every link offered to a %s actually opens', async (role) => {
    for (const section of sectionsFor(role)) {
      signedInAs(role)
      const view = renderAt(section.to)

      await waitFor(() => {
        expect(
          screen.queryByRole('heading', { name: /access denied/i }),
          `${role} was offered "${section.label}" (${section.to}) and refused when following it`,
        ).not.toBeInTheDocument()
      })
      // And it rendered something, rather than sitting on a spinner forever.
      // `queryAll`, because a screen legitimately has several headings.
      await waitFor(() => expect(screen.queryAllByRole('heading').length).toBeGreaterThan(0))
      view.unmount()
    }
  })

  test('[P0] a teacher IS refused the screens they are not offered', async () => {
    // The other half: hiding a link must reflect a real guard, not replace one.
    // A hidden link that would have worked would mean the nav is lying; a hidden
    // link with no guard behind it would mean the url is still reachable.
    const hidden = (Object.keys(ROUTE_ROLES) as (keyof typeof ROUTE_ROLES)[]).filter(
      (path) => !path.includes(':') && !(ROUTE_ROLES[path] as readonly string[]).includes('teacher'),
    )
    expect(hidden.length).toBeGreaterThan(0)

    for (const path of hidden) {
      signedInAs('teacher')
      const view = renderAt(path)

      expect(
        await screen.findByRole('heading', { name: /access denied/i }),
        `a teacher reached ${path} directly without being refused`,
      ).toBeInTheDocument()
      view.unmount()
    }
  })
})
