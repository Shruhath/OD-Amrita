import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { server } from '../../test/server'
import { CARD_MEDIA_TYPE } from '../../features/nfc/webNfc'
import type { SessionPort } from '../../features/auth/session'
import { Application } from '../../Application'
import { createPortalMemoryRouter } from '../../router'
import {
  collection,
  dataOnly,
  markCardPayload,
  markCategoryID,
  markError,
  markEventID,
  markResult,
  markStatusClosed,
  markStatusOpen,
  markStudent,
  markableCategory,
  markableEvent,
} from '../../../tests/support/factories/marking'

/**
 * The marking screen.
 *
 * This is the adoption-critical surface. A coordinator holds the phone at arm's
 * length, taps ~90 cards, and has one signal to act on: the banner. Everything
 * here protects that signal.
 *
 * THE FOUR RULES THESE TESTS EXIST TO HOLD:
 *
 *  1. GREEN MEANS THE SERVER SAID 201, and nothing earlier. The long-term
 *     architecture fires green on a durable LOCAL write because the phone is
 *     offline-first — Epic 0 has no local write, so an optimistic green would be
 *     a claim the server may then refuse. A mark the server rejected must never
 *     have shown green, not even for a frame.
 *  2. A TIMEOUT IS RED. There is no queue in Epic 0. Amber "queued" is the one
 *     state this screen must never have.
 *  3. NOTHING IS CACHED. No card list, token, fingerprint set or roster in
 *     localStorage, sessionStorage, IndexedDB or the query cache (AR20 no-list).
 *  4. A NON-COORDINATOR ISSUES NO REQUESTS AT ALL — not even the status poll.
 *
 */

/** The session the screen runs under. */
function markSessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

/** Installs the reads the screen needs to render at all. Returns a request log. */
function installMarkingBackend(options: {
  role?: string
  status?: ReturnType<typeof markStatusOpen>
} = {}) {
  const { role = 'coordinator', status = markStatusOpen() } = options
  const requests: string[] = []

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/events', ({ request }) => {
      requests.push(new URL(request.url).pathname)
      return HttpResponse.json(collection([markableEvent()]))
    }),
    http.get('*/api/v1/events/:eventID/categories', ({ request }) => {
      requests.push(new URL(request.url).pathname)
      return HttpResponse.json(dataOnly([markableCategory()]))
    }),
    http.get('*/api/v1/mark/status', ({ request }) => {
      requests.push(new URL(request.url).pathname)
      return HttpResponse.json(status)
    }),
  )

  return requests
}

/**
 * Renders the real route through the real provider stack and the real router.
 *
 * Deliberately the whole stack rather than the screen alone: the access rule
 * lives in SessionBoundary, and rendering the screen directly would test a
 * surface no user can reach and skip the very gate AC 2 is about.
 */
let nfc: ReturnType<typeof installNfc> | null = null

function renderMarkRoute() {
  nfc = installNfc()
  return render(
    <Application session={markSessionPort()} router={createPortalMemoryRouter(['/mark'])} />,
  )
}

/**
 * Simulates one physical tap.
 *
 * Waits for the screen to have armed its scan session first: a tap delivered
 * before the coordinator's scope resolved would be dropped, and the resulting
 * failure would look like a bug in the screen rather than in the test.
 */
async function simulateTap(payload: { version: string; token: string }): Promise<void> {
  await waitFor(() => expect(nfc!.armed).toBe(true))
  nfc!.tap(payload)
}

describe('the coordinator marking screen', () => {
  test('[P0] a coordinator sees the three capture modes and the scoped selectors', async () => {
    installMarkingBackend()
    await renderMarkRoute()

    expect(await screen.findByRole('combobox', { name: /event/i })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /category/i })).toBeInTheDocument()

    // AC 1: NFC tap, camera and search are all available.
    expect(screen.getByRole('button', { name: /tap|nfc/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /camera|scan/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /search/i })).toBeInTheDocument()
  })

  test('[P0] a non-coordinator gets an explicit state and issues NO requests', async () => {
    const requests = installMarkingBackend({ role: 'student' })
    await renderMarkRoute()

    expect(await screen.findByRole('heading', { name: /access|not|denied/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /tap|nfc/i })).not.toBeInTheDocument()

    // AC 2 is explicit that NO mark and NO status request is issued. A screen
    // that renders "access denied" while still polling counters every 5 seconds
    // for the whole event is a real, silent load.
    await waitFor(() => expect(requests.filter((p) => p.includes('/mark/status'))).toHaveLength(0))
    expect(requests.filter((p) => p.includes('/mark'))).toHaveLength(0)
  })

  test('[P0] a successful mark renders GREEN carrying the student name', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markResult(student), { status: 201 })))

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    // AC 4: unambiguous green, the student's DISPLAY NAME, plus roll and slot.
    const banner = await screen.findByRole('status')
    expect(banner).toHaveTextContent('Aparna Menon')
    expect(banner).toHaveTextContent(student.roll_no)
    expect(banner.className).toMatch(/green|success/)
  })

  test('[P0] the confirmation persists until the next mark or an explicit dismissal', async () => {
    installMarkingBackend()
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markResult(), { status: 201 })))

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    const banner = await screen.findByRole('status')
    // UX-DR1: a confirmation that auto-dismisses after a second is a
    // confirmation the coordinator can miss while looking at the student.
    await new Promise((resolve) => setTimeout(resolve, 1_500))
    expect(banner).toBeInTheDocument()
  })

  test('[P0] the raw tag payload is POSTed unmodified with capture_method nfc', async () => {
    installMarkingBackend()
    let body: Record<string, unknown> | null = null
    server.use(
      http.post('*/api/v1/mark', async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(markResult(), { status: 201 })
      }),
    )

    await renderMarkRoute()
    const payload = markCardPayload()
    await simulateTap(payload)

    await waitFor(() => expect(body).not.toBeNull())
    // AC 3: the token is never resolved, hashed, validated or cached client-side.
    // It goes to the server exactly as the tag presented it.
    expect(body!.card).toEqual({ version: payload.version, token: payload.token })
    expect(body!.capture_method).toBe('nfc')
    expect(body!.event_id).toBe(markEventID)
    expect(body!.category_id).toBe(markCategoryID)
    expect(body!.student_id, 'an NFC mark carries the card, never a resolved id').toBeUndefined()
  })

  test('[P0] both server counters render and neither is computed client-side', async () => {
    installMarkingBackend({ status: markStatusOpen({ counters: { mine: 4, category_total: 17 } }) })
    await renderMarkRoute()

    // AC 6 / FR13: their own marks AND the combined total for the event+category.
    expect(await screen.findByText('4')).toBeInTheDocument()
    expect(screen.getByText('17')).toBeInTheDocument()
  })

  test('[P0] a rejected mark does not inflate the counter', async () => {
    installMarkingBackend({ status: markStatusOpen({ counters: { mine: 4, category_total: 17 } }) })
    server.use(
      http.post('*/api/v1/mark', () =>
        HttpResponse.json(markError('SCAN_DUPLICATE', 'Already marked for this slot.'), { status: 409 }),
      ),
    )

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    await screen.findByRole('alert')
    // Optimistic increment would show 5 here and then silently correct on the
    // next poll — the coordinator would have counted a student who was refused.
    expect(screen.getByText('4')).toBeInTheDocument()
  })

  test('[P0] the slot clock counts down from the server window, not the device clock', async () => {
    installMarkingBackend({ status: markStatusOpen({}, 12) })
    await renderMarkRoute()

    // AC 7 / FR14: time remaining in the active slot, derived from the server's
    // window and server time — never from a client-side slot table.
    expect(await screen.findByText(/1[12]:\d\d|12 min/i)).toBeInTheDocument()
  })

  test('[P0] no open slot says so plainly and shows no stale or negative countdown', async () => {
    installMarkingBackend({ status: markStatusClosed() })
    await renderMarkRoute()

    expect(await screen.findByText(/no slot is open/i)).toBeInTheDocument()
    expect(screen.queryByText('00:00')).not.toBeInTheDocument()
    expect(screen.queryByText(/-\d/)).not.toBeInTheDocument()
  })

  test.each([
    ['SCAN_DUPLICATE', 409, /already marked|duplicate/i],
    ['CARD_NOT_RECOGNIZED', 404, /card|not recognised|not recognized/i],
    ['SLOT_CLOSED', 409, /slot/i],
    ['EVENT_ARCHIVED', 409, /event/i],
    ['STUDENT_NOT_FOUND', 404, /student/i],
    ['RBAC_FORBIDDEN', 403, /permission|allowed|forbidden/i],
    ['VALIDATION_FAILED', 400, /check|invalid/i],
  ])('[P0] %s renders RED with its specific reason and a retry path', async (code, status, pattern) => {
    installMarkingBackend()
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markError(code, 'Rejected.'), { status })))

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    // AC 5: unambiguous red, the SPECIFIC reason, never reported as recorded,
    // and a visible retry path.
    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(pattern)
    expect(banner.className).toMatch(/red|error|danger/)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /retry|try again/i })).toBeInTheDocument()
  })

  test('[P0] a network timeout is RED, never an amber queued state', async () => {
    installMarkingBackend()
    server.use(http.post('*/api/v1/mark', () => HttpResponse.error()))

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(/not recorded/i)
    // There is no queue in Epic 0. Telling a coordinator their mark is "queued"
    // when nothing will ever send it is the worst failure this screen can have.
    expect(banner.textContent).not.toMatch(/queued|will retry|saved locally|offline/i)
    expect(banner.className).not.toMatch(/amber|warning/)
  })

  test('[P0] no request leaves an indefinite spinner behind', async () => {
    installMarkingBackend()
    server.use(http.post('*/api/v1/mark', () => HttpResponse.error()))

    await renderMarkRoute()
    await simulateTap(markCardPayload())

    await waitFor(() => expect(screen.queryByText(/marking…|marking\.\.\./i)).not.toBeInTheDocument())
    expect(await screen.findByRole('alert')).toBeInTheDocument()
  })

  test('[P0] several taps in a row each get their own confirmation without re-arming', async () => {
    installMarkingBackend()
    const marked: string[] = []
    server.use(
      http.post('*/api/v1/mark', async ({ request }) => {
        const body = (await request.json()) as { card?: { token: string } }
        marked.push(body.card?.token ?? '')
        return HttpResponse.json(markResult(markStudent(marked.length)), { status: 201 })
      }),
    )

    await renderMarkRoute()
    await simulateTap(markCardPayload({ token: 'token-one' }))
    await simulateTap(markCardPayload({ token: 'token-two' }))

    // AC 8: no "start scanning" press between students.
    await waitFor(() => expect(marked).toEqual(['token-one', 'token-two']))
  })

  test('[P0] the same token repeated inside the debounce window fires one request', async () => {
    installMarkingBackend()
    let posts = 0
    server.use(
      http.post('*/api/v1/mark', () => {
        posts += 1
        return HttpResponse.json(markResult(), { status: 201 })
      }),
    )

    await renderMarkRoute()
    const payload = markCardPayload({ token: 'one-physical-tap' })
    await simulateTap(payload)
    await simulateTap(payload)
    await simulateTap(payload)

    await waitFor(() => expect(posts).toBe(1))
  })
})

describe('the AR20 no-list guarantee', () => {
  test('[P0] no token, payload or roster survives a full mark cycle in web storage', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markResult(student), { status: 201 })))

    await renderMarkRoute()
    const payload = markCardPayload({ token: 'secret-card-token' })
    await simulateTap(payload)
    await screen.findByRole('status')

    const local = JSON.stringify(window.localStorage)
    const session = JSON.stringify(window.sessionStorage)

    for (const store of [local, session]) {
      expect(store).not.toContain(payload.token)
      expect(store).not.toContain(student.roll_no)
      expect(store).not.toContain(student.student_id)
    }

    // The seal key never reaches the device and no card list may be cached on
    // it, so a stolen phone yields nothing (NFR16 / AR20 no-list).
    expect(local).not.toMatch(/card|fingerprint/i)
  })

  test('[P0] search results are not persisted either', async () => {
    installMarkingBackend()
    const student = markStudent(1)
    server.use(
      http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([student]))),
    )

    await renderMarkRoute()
    await userEvent.click(await screen.findByRole('button', { name: /search/i }))
    await userEvent.type(screen.getByRole('searchbox'), student.roll_no)

    await screen.findByText(student.display_name)

    // A resolved roster on the device is exactly the cached list AR20 forbids,
    // whether it arrived by card lookup or by search.
    expect(JSON.stringify(window.localStorage)).not.toContain(student.roll_no)
    expect(JSON.stringify(window.sessionStorage)).not.toContain(student.roll_no)
  })

  test('[P0] the selected event and category MAY persist — they are not identity data', async () => {
    installMarkingBackend()
    await renderMarkRoute()
    await screen.findByRole('combobox', { name: /event/i })

    // Deliberately permissive. Losing the selection on every reload during a
    // long event is a real usability cost, and an event id is public.
    const local = JSON.stringify(window.localStorage)
    if (local.includes(markEventID)) {
      expect(local).not.toContain('token')
    }
  })
})

/**
 * Installs a fake `NDEFReader` on the real window.
 *
 * The screen feature-detects against `window`, so stubbing the global — rather
 * than injecting an adapter — is what makes these tests exercise the same
 * detection path the phone will take. Same approach the card populator's own
 * suite uses, for the same reason.
 */
function installNfc() {
  type ReadingListener = (event: { message: { records: unknown[] } }) => void
  const listeners: ReadingListener[] = []

  class FakeNDEFReader {
    addEventListener(type: string, listener: ReadingListener) {
      if (type === 'reading') listeners.push(listener)
    }

    removeEventListener() {}

    async scan() {}

    async write() {}
  }

  vi.stubGlobal('isSecureContext', true)
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  ;(window as unknown as { NDEFReader: unknown }).NDEFReader = FakeNDEFReader

  return {
    tap(payload: { version: string; token: string }) {
      const records = [{
        recordType: 'mime',
        mediaType: CARD_MEDIA_TYPE,
        data: new DataView(new TextEncoder().encode(JSON.stringify(payload)).buffer),
      }]
      for (const listener of [...listeners]) listener({ message: { records } })
    },
    get armed() {
      return listeners.length > 0
    },
  }
}
