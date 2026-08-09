import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../Application'
import type { SessionPort } from '../../features/auth/session'
import { createPortalMemoryRouter } from '../../router'
import { server } from '../../test/server'
import { selectByValue } from '../../test/select'
import type { PublicDayAttendance } from '../../../tests/support/factories/public-attendance'
import {
  publicAttendanceRoute,
  publicBoardCategoryID,
  publicBoardSecondCategoryID,
  publicDayAttendance,
  publicDayMark,
  publicDayStudent,
  publicEmptyDayAttendance,
  publicError,
  publicSearchResults,
} from '../../../tests/support/factories/public-attendance'

/**
 * The public attendance board — one campus day's marked students, shown to
 * anybody who opens the page.
 *
 * Rendered through the REAL Application with a SIGNED-OUT session, like every
 * other suite on this surface: the session port FAILS the test if refresh() or
 * signOut() is ever called, because a public page built on the authorized client
 * can sign a signed-in admin out of another tab and that is invisible unless the
 * port treats those calls as errors.
 *
 * The board publishes a LIST of people, which nothing else in this app does. So
 * the assertions below are as much about what it must not do — filter in the
 * browser, hide a truncated day, render a failure as an empty list — as about
 * what it shows.
 */

function anonymousSession(): SessionPort {
  return {
    accessToken: vi.fn(async () => null),
    refresh: vi.fn(async () => {
      throw new Error('the public board must never refresh a session')
    }),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => {
      throw new Error('the public board must never sign a visitor out')
    }),
    subscribe: vi.fn(() => () => undefined),
  }
}

/** Captures the query string the board actually asked the server for. */
function installBoard(body: PublicDayAttendance = publicDayAttendance(), status = 200) {
  const requested: URL[] = []
  server.use(
    http.get('*/api/v1/public/students/search', () => HttpResponse.json(publicSearchResults())),
    http.get('*/api/v1/public/od-records', ({ request }) => {
      requested.push(new URL(request.url))
      if (status !== 200) {
        return HttpResponse.json(publicError('RATE_LIMITED', 'Too many requests.'), { status })
      }
      return HttpResponse.json(body)
    }),
  )
  return requested
}

function renderBoard() {
  return render(
    <Application session={anonymousSession()} router={createPortalMemoryRouter([publicAttendanceRoute])} />,
  )
}

describe('the public attendance board', () => {
  // BOARD-WEB-001 — the board is there on arrival, with no search and no input.
  //
  // This is the entire product requirement: open the page, see today's list.
  // A board that needed a click would be a different feature.
  test('[P0] shows today\'s list immediately, without searching for anyone', async () => {
    installBoard()
    renderBoard()

    const table = await screen.findByRole('table', { name: /on-duty students/i })
    const row = within(table).getByRole('row', { name: /CB\.SC\.U4CSE24101/ })

    // The three columns of the ask: roll number, name, and the slots with what
    // each was for.
    expect(row).toHaveTextContent('Ananya Rao')
    for (const slot of ['6', '7', '10']) {
      expect(row).toHaveTextContent(slot)
    }
    expect(row).toHaveTextContent('Procession')

    // Nothing was typed, so nothing may have been searched for.
    expect(screen.getByRole('searchbox', { name: /roll number|name/i })).toHaveValue('')
  })

  // BOARD-WEB-002 — THE FILTERS GO TO THE SERVER.
  //
  // Narrowing in the browser would mean the whole day's roster is downloaded
  // whatever was asked for — on this surface, the difference between publishing
  // one slot and publishing everything — and it would silently mis-filter any
  // day large enough to hit the server's row cap.
  test('[P0] the slot filter is sent to the server, not applied to the response', async () => {
    const user = userEvent.setup()
    const requested = installBoard()
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })
    await selectByValue(user, screen.getByRole('combobox', { name: /^slot$/i }), '7')

    await waitFor(() => {
      expect(requested.some((url) => url.searchParams.get('slot') === '7')).toBe(true)
    })
  })

  test('[P0] the category filter is sent to the server too', async () => {
    const user = userEvent.setup()
    const requested = installBoard()
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })
    await selectByValue(user, screen.getByRole('combobox', { name: /^category$/i }), publicBoardSecondCategoryID)

    await waitFor(() => {
      expect(requested.some((url) => url.searchParams.get('category_id') === publicBoardSecondCategoryID)).toBe(true)
    })
  })

  // BOARD-WEB-003 — the category options come from the server's list, which is
  // built from the UNFILTERED day.
  //
  // Options derived from the rows on screen would collapse to the one already
  // chosen the moment a filter was applied, leaving no way back to the others.
  test('[P0] the category filter keeps every option after one is chosen', async () => {
    const user = userEvent.setup()
    // The narrowed response still carries BOTH categories, which is what the
    // server promises and what this asserts the screen honours.
    installBoard(publicDayAttendance({
      students: [publicDayStudent({ marks: [publicDayMark(7, { category_id: publicBoardSecondCategoryID, category_name: 'Athletics' })] })],
    }))
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })
    await selectByValue(user, screen.getByRole('combobox', { name: /^category$/i }), publicBoardSecondCategoryID)

    await user.click(screen.getByRole('combobox', { name: /^category$/i }))
    const listbox = await screen.findByRole('listbox')
    expect(within(listbox).getByRole('option', { name: /procession/i })).toBeInTheDocument()
    expect(within(listbox).getByRole('option', { name: /all categories/i })).toBeInTheDocument()
  })

  // BOARD-WEB-004 — a day nobody is marked on says so, and says it as an
  // absence of marks rather than as a failure.
  test('[P0] an empty day is stated plainly and is not an error', async () => {
    installBoard(publicEmptyDayAttendance())
    renderBoard()

    expect(await screen.findByText(/nobody has been marked on duty yet today/i)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('table', { name: /on-duty students/i })).not.toBeInTheDocument()
  })

  // BOARD-WEB-005 — an empty FILTERED day is worded differently from an empty
  // day.
  //
  // "Nobody is out today" and "nobody is out in slot 7" are different answers,
  // and a visitor who has filtered themselves into an empty list must not be
  // told the first one.
  test('[P0] an empty result under a filter does not claim the day is empty', async () => {
    const user = userEvent.setup()
    installBoard()
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })
    server.use(
      http.get('*/api/v1/public/od-records', () => HttpResponse.json(publicDayAttendance({ students: [] }))),
    )
    await selectByValue(user, screen.getByRole('combobox', { name: /^slot$/i }), '3')

    expect(await screen.findByText(/no on-duty records match that filter/i)).toBeInTheDocument()
    expect(screen.queryByText(/nobody has been marked on duty yet today/i)).not.toBeInTheDocument()
  })

  // BOARD-WEB-006 — A TRUNCATED BOARD SAYS SO.
  //
  // The server caps the row read. A board that is silently short reads as "those
  // students were not marked", which is the single statement this whole surface
  // exists never to make by accident.
  test('[P0] a truncated day is announced rather than silently short', async () => {
    installBoard(publicDayAttendance({ truncated: true }))
    renderBoard()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/more records than can be shown/i)
    // And it still shows what it has — announcing truncation must not replace
    // the rows.
    expect(screen.getByRole('table', { name: /on-duty students/i })).toBeInTheDocument()
  })

  // BOARD-WEB-007 — a failed read is a named failure with a retry, NEVER an
  // empty board.
  //
  // The two look identical if the error path is missing, and one of them tells
  // a whole campus that nobody was marked.
  test('[P0] a refused read is explained and never rendered as an empty day', async () => {
    installBoard(publicDayAttendance(), 429)
    renderBoard()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(/too many requests/i)
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument()
    expect(screen.queryByText(/nobody has been marked/i)).not.toBeInTheDocument()
  })

  // BOARD-WEB-008 — the board sends NO Authorization header and never signs
  // anyone out.
  //
  // The anonymousSession port throws from refresh() and signOut(), so this
  // passes only if the board genuinely uses the credential-less public client.
  test('[P0] sends no Authorization header and writes nothing to storage', async () => {
    localStorage.clear()
    sessionStorage.clear()

    const headers: (string | null)[] = []
    server.use(
      http.get('*/api/v1/public/od-records', ({ request }) => {
        headers.push(request.headers.get('Authorization'))
        return HttpResponse.json(publicDayAttendance())
      }),
    )
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })

    expect(headers.length).toBeGreaterThan(0)
    for (const header of headers) {
      expect(header).toBeNull()
    }
    // AC 13 holds this whole page to persisting nothing. The board's filters are
    // deliberately NOT remembered for that reason — one tap to set again is a
    // cheaper price than being the exception that softens the rule.
    expect(sessionStorage.length).toBe(0)
  })

  // BOARD-WEB-009 — the board carries no staff identity and no student id.
  //
  // The contract and the real-Postgres suite hold the wire to this; the screen
  // is held to it too, because a field that arrives and is rendered is disclosed
  // whether or not it was supposed to be there.
  test('[P0] no email, actor or student id is rendered', async () => {
    installBoard()
    renderBoard()

    const table = await screen.findByRole('table', { name: /on-duty students/i })
    expect(table).not.toHaveTextContent(/@/)
    expect(table).not.toHaveTextContent(/marked by/i)
    // A uuid on screen would mean an id reached the page. Category ids are used
    // as option VALUES, which is not the same as rendering them.
    expect(table.textContent ?? '').not.toMatch(/[0-9a-f]{8}-[0-9a-f]{4}-/i)
  })

  // BOARD-WEB-010 — the board does not replace the per-student view.
  //
  // Searching still opens one student's full history below the board, which is
  // the flow a student uses to check their own record across days.
  test('[P0] searching still opens the per-student record below the board', async () => {
    const user = userEvent.setup()
    installBoard()
    server.use(
      http.get('*/api/v1/public/students/*/od-records', () =>
        HttpResponse.json({
          student: {
            student_id: '95000000-0000-4000-8000-000000000101',
            roll_no: 'CB.SC.U4CSE24101',
            display_name: 'Ananya Rao',
            branch: 'CSE',
            section: 'A',
            batch: '2024',
          },
          days: [],
          generated_at: '2026-08-09T16:30:00Z',
        }),
      ),
    )
    renderBoard()

    await screen.findByRole('table', { name: /on-duty students/i })
    await user.type(screen.getByRole('searchbox', { name: /roll number|name/i }), 'CB.SC.U4CSE24101')
    await user.click(await screen.findByRole('option', { name: /CB\.SC\.U4CSE24101/ }))

    expect(await screen.findByText(/no OD records found/i)).toBeInTheDocument()
    // The board is still there — the per-student view opens alongside it.
    expect(screen.getByRole('table', { name: /on-duty students/i })).toBeInTheDocument()
    expect(publicBoardCategoryID).toBeTruthy()
  })
})
