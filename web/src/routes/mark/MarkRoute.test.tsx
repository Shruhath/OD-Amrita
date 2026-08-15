import { selectByValue } from '../../test/select'
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
 *  4. A NON-LIVE-MARKER ISSUES NO REQUESTS AT ALL — not even the status poll.
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

function renderMarkRoute(ports: { camera?: ReturnType<typeof fakeCamera> } = {}) {
  nfc = installNfc()
  if (ports.camera) installCamera(ports.camera)
  return render(
    <Application session={markSessionPort()} router={createPortalMemoryRouter(['/mark'])} />,
  )
}

test('[P0] admin NFC marking keeps the portal header and navigation', async () => {
  installMarkingBackend({ role: 'admin' })
  renderMarkRoute()

  expect(await screen.findByRole('link', { name: /od-amrita home/i })).toBeInTheDocument()
  expect(screen.getByRole('navigation', { name: /administration sections/i })).toBeInTheDocument()
  expect(screen.getAllByRole('link', { name: /nfc marking/i }).length).toBeGreaterThan(0)
  expect(await screen.findByRole('heading', { name: /mark attendance/i })).toBeInTheDocument()
})

test('[P0] coordinator NFC marking remains a focused full-screen surface', async () => {
  installMarkingBackend({ role: 'coordinator' })
  renderMarkRoute()

  expect(await screen.findByRole('heading', { name: /mark attendance/i })).toBeInTheDocument()
  expect(screen.queryByRole('link', { name: /od-amrita home/i })).toBeNull()
  expect(screen.queryByRole('navigation', { name: /administration sections/i })).toBeNull()
  expect(screen.queryByRole('button', { name: /open navigation/i })).toBeNull()
})

/**
 * A fake `BarcodeDetector` + `getUserMedia` pair, installed on the real globals.
 *
 * The screen builds its adapter from `window`/`navigator`, so the seam is faked
 * where the adapter reads it rather than by injecting a port the production path
 * does not use. CI Chromium and jsdom have neither a camera nor the detector.
 */
function fakeCamera() {
  let emit: ((text: string) => void) | null = null
  const state = {
    mounted: false,
    stopped: 0,
    /** Feeds one decoded barcode through the detector. */
    async decode(text: string) {
      await waitFor(() => expect(emit).not.toBeNull())
      emit!(text)
    },
    /** Wired by installCamera. */
    _attach(fn: (text: string) => void) {
      emit = fn
    },
  }
  return state
}

function installCamera(camera: ReturnType<typeof fakeCamera>) {
  class FakeBarcodeDetector {
    static async getSupportedFormats() {
      return ['code_128', 'qr_code']
    }

    async detect(): Promise<{ rawValue: string }[]> {
      // Frames yield nothing until the test decodes something; a pending decode
      // is delivered once and then cleared, exactly like a card leaving frame.
      const queued = pending
      pending = []
      return queued
    }
  }

  let pending: { rawValue: string }[] = []
  camera._attach((text) => {
    pending = [{ rawValue: text }]
  })

  const track = { stop: () => { camera.stopped += 1 } }
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  ;(window as unknown as { BarcodeDetector: unknown }).BarcodeDetector = FakeBarcodeDetector
  Object.defineProperty(navigator, 'mediaDevices', {
    configurable: true,
    value: { getUserMedia: async () => ({ getTracks: () => [track] }) as unknown as MediaStream },
  })

  // jsdom has no rAF loop worth driving frame by frame; a timer stands in.
  vi.stubGlobal('requestAnimationFrame', (fn: FrameRequestCallback) =>
    setTimeout(() => fn(performance.now()), 0) as unknown as number,
  )
  vi.stubGlobal('cancelAnimationFrame', (id: number) => clearTimeout(id as unknown as NodeJS.Timeout))

  // The screen mounts the preview element into the DOM; record that it did.
  const observer = new MutationObserver(() => {
    if (document.querySelector('.mark-preview video')) camera.mounted = true
  })
  observer.observe(document.body, { childList: true, subtree: true })
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

  test('[P0] an admin sees the coordinator-style NFC camera and search surface', async () => {
    installMarkingBackend({ role: 'admin' })
    await renderMarkRoute()

    expect(await screen.findByRole('combobox', { name: /event/i })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /category/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /tap|nfc/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /camera|scan/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /search/i })).toBeInTheDocument()
  })

  test('[P0] a non-live-marker gets an explicit state and issues NO requests', async () => {
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

  // The HTTP status of each code is the one the server's error registry
  // actually maps it to (api/internal/domain/errors.go). Mocking a different
  // status makes the test pass against a response the API never sends.
  test.each([
    ['SCAN_DUPLICATE', 409, /already marked|duplicate/i],
    // 422, not 404: the token is well-formed but maps to no known card, which
    // the registry documents as unprocessable rather than missing.
    ['CARD_NOT_RECOGNIZED', 422, /card|not recognised|not recognized/i],
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

  // -----------------------------------------------------------------------
  // The SEARCH capture path.
  //
  // This existed only as a persistence check that typed into the box and read a
  // name back — it never clicked a result, so nothing asserted that picking a
  // student POSTs `student_id` with capture_method "search" at all.
  // -----------------------------------------------------------------------
  test('[P0] picking a search result marks by student_id with capture_method "search"', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    let posted: Record<string, unknown> | null = null
    server.use(
      http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([student]))),
      http.post('*/api/v1/mark', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(markResult(student), { status: 201 })
      }),
    )

    await renderMarkRoute()
    await userEvent.click(await screen.findByRole('button', { name: /search/i }))
    await userEvent.type(screen.getByRole('searchbox'), student.roll_no)

    await userEvent.click(await screen.findByRole('button', { name: new RegExp(student.display_name, 'i') }))

    await waitFor(() => expect(posted).not.toBeNull())
    expect(posted!.student_id).toBe(student.student_id)
    expect(posted!.capture_method).toBe('search')
    // The card branch must NOT be used for a search mark.
    expect(posted!.card).toBeUndefined()
    expect(posted!.event_id).toBe(markEventID)
    expect(posted!.category_id).toBe(markCategoryID)

    const banner = await screen.findByRole('status')
    expect(banner).toHaveTextContent(student.display_name)
  })

  // -----------------------------------------------------------------------
  // The CAMERA capture path — previously untested at unit level entirely.
  //
  // The barcode on the physical ID card carries a ROLL NUMBER, not a sealed
  // token, so a decode resolves through search and then marks by student_id.
  // -----------------------------------------------------------------------
  test('[P0] a camera decode resolves the roll and marks by student_id with capture_method "camera"', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    let posted: Record<string, unknown> | null = null
    server.use(
      http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([student]))),
      http.post('*/api/v1/mark', async ({ request }) => {
        posted = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(markResult(student), { status: 201 })
      }),
    )

    const camera = fakeCamera()
    await renderMarkRoute({ camera })
    await userEvent.click(await screen.findByRole('button', { name: /scan camera/i }))

    await camera.decode(student.roll_no)

    await waitFor(() => expect(posted).not.toBeNull())
    expect(posted!.student_id).toBe(student.student_id)
    expect(posted!.capture_method).toBe('camera')
    expect(posted!.card).toBeUndefined()
  })

  test('[P0] a camera decode matching no single student is RED and marks nothing', async () => {
    installMarkingBackend()
    let posts = 0
    server.use(
      // Two hits with the same roll: ambiguous, so marking either would be a guess.
      http.get('*/api/v1/students/search', () =>
        HttpResponse.json(dataOnly([markStudent(1, { roll_no: 'CB.SC.U4CSE24001' }), markStudent(2, { roll_no: 'CB.SC.U4CSE24001' })])),
      ),
      http.post('*/api/v1/mark', () => {
        posts += 1
        return HttpResponse.json(markResult(), { status: 201 })
      }),
    )

    const camera = fakeCamera()
    await renderMarkRoute({ camera })
    await userEvent.click(await screen.findByRole('button', { name: /scan camera/i }))
    await camera.decode('CB.SC.U4CSE24001')

    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(/not recognised/i)
    expect(posts).toBe(0)
  })

  test('[P0] the camera preview is on screen — a coordinator cannot aim a blind camera', async () => {
    installMarkingBackend()
    const camera = fakeCamera()
    const { container } = await renderMarkRoute({ camera })
    await userEvent.click(await screen.findByRole('button', { name: /scan camera/i }))

    await waitFor(() => expect(camera.mounted).toBe(true))
    // The element the detector reads frames from must be the element the
    // coordinator sees, not a detached one.
    await waitFor(() => expect(container.querySelector('.mark-preview video')).not.toBeNull())
  })

  // -----------------------------------------------------------------------
  // Regressions for the review's findings.
  // -----------------------------------------------------------------------

  test('[P0] a failure is STICKY — a later success cannot silently erase it', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    let call = 0
    server.use(
      http.post('*/api/v1/mark', () => {
        call += 1
        // First tap is refused, second succeeds — the ordering that used to let
        // the green overwrite the red before the coordinator could read it.
        return call === 1
          ? HttpResponse.json(markError('SCAN_DUPLICATE', 'Rejected.'), { status: 409 })
          : HttpResponse.json(markResult(student), { status: 201 })
      }),
    )

    await renderMarkRoute()
    await simulateTap(markCardPayload({ token: 'first-card' }))
    await screen.findByRole('alert')

    await simulateTap(markCardPayload({ token: 'second-card' }))
    await waitFor(() => expect(call).toBe(2))

    // The refusal is still the thing on screen. An unnoticed failure is the
    // worst outcome on this surface.
    expect(screen.getByRole('alert')).toHaveTextContent(/already marked/i)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  test('[P0] "Try again" RE-ARMS the capture session, it does not just hide the banner', async () => {
    installMarkingBackend()
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markResult(), { status: 201 })))

    await renderMarkRoute()
    await waitFor(() => expect(nfc!.armed).toBe(true))

    // A session that ended (radio off, permission refused) leaves the mode dead
    // until something starts a new one.
    nfc!.failSession('NFC scanning could not start. Check that NFC is switched on.')
    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(/nfc/i)

    const armedBefore = nfc!.sessions
    await userEvent.click(screen.getByRole('button', { name: /try again/i }))

    // A fresh session, not merely a cleared banner.
    await waitFor(() => expect(nfc!.sessions).toBeGreaterThan(armedBefore))
  })

  test('[P0] changing category ends the scan session armed for the previous one', async () => {
    installMarkingBackend()
    server.use(
      http.get('*/api/v1/events/:eventID/categories', () =>
        HttpResponse.json(dataOnly([markableCategory(), markableCategory({ id: '94000000-0000-4000-8000-000000000002', name: 'Cultural' })])),
      ),
      http.post('*/api/v1/mark', () => HttpResponse.json(markResult(), { status: 201 })),
    )

    await renderMarkRoute()
    await waitFor(() => expect(nfc!.armed).toBe(true))
    const before = nfc!.aborts

    await selectByValue(userEvent.setup(), screen.getByRole('combobox', { name: 'Category' }), '94000000-0000-4000-8000-000000000002')

    // The old session must be torn down. Left running, its token debounce would
    // silently swallow a re-tap of the same card in the NEW category.
    await waitFor(() => expect(nfc!.aborts).toBeGreaterThan(before))
  })

  test('[P0] a non-JSON error body still renders a readable reason, never a blank red', async () => {
    installMarkingBackend()
    server.use(
      // Exactly what gin serves for an unmatched route, and what a proxy serves
      // for a 502 — neither is the JSON error envelope.
      http.post('*/api/v1/mark', () =>
        HttpResponse.text('404 page not found', { status: 404 }),
      ),
    )

    await renderMarkRoute()
    await simulateTap(markCardPayload({ token: 'plain-text-failure' }))

    const banner = await screen.findByRole('alert')
    expect(banner).toHaveTextContent(/not recorded/i)
    // The bug this pins: `body.code` and `body.message` were both undefined, so
    // the reason span rendered empty and the coordinator saw a red box with no
    // reason in it at all.
    expect(banner.textContent?.replace(/not recorded/i, '').replace(/try again/i, '').trim()).not.toBe('')
    expect(banner).toHaveTextContent(/unreadable|failed|404/i)
  })

  test('[P0] a failed event read is stated, not rendered as "no markable events"', async () => {
    installMarkingBackend()
    server.use(
      http.get('*/api/v1/events', () =>
        HttpResponse.json(markError('INTERNAL', 'Boom.'), { status: 500 }),
      ),
    )

    await renderMarkRoute()

    // "The server refused your event list" and "you have no events" must not
    // look identical — only one of them means stop tapping and find an admin.
    const alerts = await screen.findAllByRole('alert')
    expect(alerts.some((node) => /could not be loaded/i.test(node.textContent ?? ''))).toBe(true)
  })

  test('[P0] the search box issues ONE query for a typed roll number, not one per keystroke', async () => {
    installMarkingBackend()
    let searches = 0
    server.use(
      http.get('*/api/v1/students/search', () => {
        searches += 1
        return HttpResponse.json(dataOnly([markStudent(1)]))
      }),
    )

    await renderMarkRoute()
    await userEvent.click(await screen.findByRole('button', { name: /search/i }))
    await userEvent.type(screen.getByRole('searchbox'), 'CB.SC.U4CSE24001')

    await waitFor(() => expect(searches).toBeGreaterThan(0))
    // 16 characters used to mean 14 prefix scans of the roster, per lookup, per
    // coordinator, on event Wi-Fi.
    expect(searches).toBeLessThanOrEqual(2)
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

  // AC 3 names FOUR stores: localStorage, sessionStorage, IndexedDB and the
  // query cache. Only the first two were ever checked, so two-thirds of the
  // sentence was unasserted — and the query cache is the one a data-fetching
  // library will populate for you by default.
  test('[P0] nothing reaches IndexedDB — AC 3 names it explicitly', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    server.use(http.post('*/api/v1/mark', () => HttpResponse.json(markResult(student), { status: 201 })))

    const opened: string[] = []
    const realOpen = globalThis.indexedDB?.open
    if (globalThis.indexedDB) {
      vi.spyOn(globalThis.indexedDB, 'open').mockImplementation(function (this: IDBFactory, name: string, ...rest: unknown[]) {
        opened.push(name)
        return (realOpen as unknown as (...args: unknown[]) => IDBOpenDBRequest).call(this, name, ...rest)
      } as typeof globalThis.indexedDB.open)
    }

    await renderMarkRoute()
    await simulateTap(markCardPayload({ token: 'secret-card-token' }))
    await screen.findByRole('status')

    // No database is opened at all on this path. A card list on the device is
    // the thing AR20 forbids, and IndexedDB is where one would plausibly land.
    expect(opened).toEqual([])
  })

  test('[P0] the roster leaves no residue in the query cache once the search is closed', async () => {
    installMarkingBackend()
    const student = markStudent(1, { display_name: 'Aparna Menon' })
    server.use(
      http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([student]))),
    )

    const { container } = await renderMarkRoute()
    await userEvent.click(await screen.findByRole('button', { name: /search/i }))
    await userEvent.type(screen.getByRole('searchbox'), student.roll_no)
    await screen.findByText(student.display_name)

    // Leaving search mode drops the observer; `gcTime: 0` must then evict the
    // entry immediately rather than leaving a resolved roster in memory for the
    // rest of the event. Without that setting this text survives indefinitely.
    await userEvent.click(screen.getByRole('button', { name: /tap card/i }))

    await waitFor(() => {
      expect(container.textContent ?? '').not.toContain(student.roll_no)
    })
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
  let listeners: ReadingListener[] = []
  // Counted so a test can prove a session was STARTED again, and that the
  // previous one was torn down — the two properties that distinguish re-arming
  // from merely clearing a banner.
  const counts = { sessions: 0, aborts: 0 }
  let failSession: ((message: string) => void) | null = null

  class FakeNDEFReader {
    addEventListener(type: string, listener: ReadingListener) {
      if (type === 'reading') listeners.push(listener)
    }

    removeEventListener(type: string, listener: ReadingListener) {
      if (type === 'reading') listeners = listeners.filter((each) => each !== listener)
    }

    scan(options?: { signal?: AbortSignal }) {
      counts.sessions += 1
      options?.signal?.addEventListener('abort', () => { counts.aborts += 1 })
      // Left PENDING on purpose. A real scan() settles when the radio is armed
      // and rejects when it cannot be — and the only way scanSession reports a
      // dead radio is that rejection. Resolving here would close the promise
      // before a test could reject it, so the failure path would be untestable.
      // Nothing awaits the result, and taps arrive through the listener.
      return new Promise<void>((_resolve, reject) => {
        failSession = (message: string) => reject(new Error(message))
      })
    }

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
    /** Ends the session the way a refused permission or a dead radio does. */
    failSession(message: string) {
      listeners = []
      failSession?.(message)
    },
    get armed() {
      return listeners.length > 0
    },
    get sessions() {
      return counts.sessions
    },
    get aborts() {
      return counts.aborts
    },
  }
}
