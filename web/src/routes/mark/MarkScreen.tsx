import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { CaptureMethod, CardPayload, MarkAttendanceResult, ResolvedCardStudent } from '../../api/client'
import { ApiFailure } from '../../api/client'
import { useAuth } from '../../features/auth/context'
import { createBarcodeScanner, type BarcodeScannerStatus } from '../../features/marking/barcodeScanner'
import { CardNfcError, createWebNfcAdapter, type WebNfcStatus } from '../../features/nfc/webNfc'

/**
 * The coordinator marking surface.
 *
 * This is the one screen the event actually runs on. A coordinator holds the
 * phone at arm's length, marks ~90 students, and has exactly one signal to act
 * on: the banner. Everything here protects that signal.
 *
 * FOUR RULES THIS SCREEN HOLDS:
 *
 *  1. GREEN MEANS THE SERVER RETURNED 201, and nothing earlier. The long-term
 *     architecture fires green on a durable LOCAL write because the phone is
 *     offline-first — this MVP has no local write, so an optimistic green would
 *     be a claim the server may then refuse. A mark the server rejected must
 *     never have shown green, not even for a frame.
 *  2. A TIMEOUT IS RED. There is no queue here. Amber "queued" is the one state
 *     this screen must never have: it tells a coordinator a student is marked
 *     when nothing will ever send it.
 *  3. NOTHING IS CACHED. No card list, token, fingerprint set or roster reaches
 *     localStorage, sessionStorage or IndexedDB (AR20 no-list). The selected
 *     event and category may persist — an event id is public, not identity data.
 *  4. THE CLIENT COMPUTES NO TRUTH. Slots, counters and authorization are all
 *     server-owned; this screen only renders what the server said.
 */

type CaptureMode = 'nfc' | 'camera' | 'search'

/** The banner's state. There is deliberately no amber: see rule 2. */
type Banner =
  | { tone: 'success'; result: MarkAttendanceResult }
  | { tone: 'error'; message: string; retryable: boolean }
  | null

/** Injected in tests. Production uses the real window-bound adapters. */
export type MarkScreenPorts = {
  nfc?: ReturnType<typeof createWebNfcAdapter>
  camera?: ReturnType<typeof createBarcodeScanner>
}

const STATUS_POLL_MS = 5_000

export default function MarkScreen({ ports }: { ports?: MarkScreenPorts } = {}) {
  const { api, identity } = useAuth()
  const queryClient = useQueryClient()

  const nfc = useMemo(() => ports?.nfc ?? createWebNfcAdapter(window), [ports?.nfc])
  const camera = useMemo(
    () => ports?.camera ?? createBarcodeScanner(window, navigator),
    [ports?.camera],
  )

  const [mode, setMode] = useState<CaptureMode>('nfc')
  const [banner, setBanner] = useState<Banner>(null)
  const [eventID, setEventID] = useState('')
  const [categoryID, setCategoryID] = useState('')

  // ---------------------------------------------------------------------
  // Scoped reads. Both come from the server's own view of what this
  // coordinator may mark for, so the selector can never offer a scope the
  // mark path would refuse.
  // ---------------------------------------------------------------------

  const events = useQuery({
    queryKey: ['markable-events'],
    queryFn: () => api.markableEvents(),
  })

  // Memoized so the selection effects below depend on a stable identity: a fresh
  // array every render would re-run them on every render.
  const eventOptions = useMemo(() => events.data?.data ?? [], [events.data])

  useEffect(() => {
    if (!eventID && eventOptions.length > 0) setEventID(eventOptions[0].id)
  }, [eventID, eventOptions])

  const categories = useQuery({
    queryKey: ['markable-categories', eventID],
    queryFn: () => api.markableCategories(eventID),
    enabled: eventID !== '',
  })

  const categoryOptions = useMemo(() => categories.data?.data ?? [], [categories.data])

  useEffect(() => {
    if (categoryOptions.length === 0) return
    if (!categoryOptions.some((category) => category.id === categoryID)) {
      setCategoryID(categoryOptions[0].id)
    }
  }, [categoryID, categoryOptions])

  const scopeReady = eventID !== '' && categoryID !== ''

  // ---------------------------------------------------------------------
  // Counters and the slot clock — one server endpoint, polled.
  // ---------------------------------------------------------------------

  const status = useQuery({
    queryKey: ['mark-status', eventID, categoryID],
    queryFn: () => api.markStatus(eventID, categoryID),
    enabled: scopeReady,
    refetchInterval: STATUS_POLL_MS,
  })

  // ---------------------------------------------------------------------
  // The mark itself.
  // ---------------------------------------------------------------------

  const mark = useMutation({
    mutationFn: (input: { identity: MarkIdentity; capture: CaptureMethod }) =>
      api.mark({
        event_id: eventID,
        category_id: categoryID,
        capture_method: input.capture,
        ...('card' in input.identity
          ? { card: input.identity.card }
          : { student_id: input.identity.studentID }),
      }),
    onSuccess(result) {
      // Green fires HERE — on the server's 201 — and nowhere earlier.
      setBanner({ tone: 'success', result })
      // Refetched rather than incremented: a rejected mark must never be able to
      // inflate the number, and the server's count is the only true one.
      void queryClient.invalidateQueries({ queryKey: ['mark-status', eventID, categoryID] })
    },
    onError(error) {
      setBanner({ tone: 'error', message: markFailureMessage(error), retryable: true })
    },
  })

  const markRef = useRef(mark.mutate)
  markRef.current = mark.mutate

  const submit = useCallback((identity: MarkIdentity, capture: CaptureMethod) => {
    markRef.current({ identity, capture })
  }, [])

  // ---------------------------------------------------------------------
  // Capture: NFC. One continuous session per (mode, scope) — a coordinator
  // must not press "start" between students.
  // ---------------------------------------------------------------------

  const [nfcStatus, setNfcStatus] = useState<WebNfcStatus>(() => nfc.status())
  useEffect(() => setNfcStatus(nfc.status()), [nfc])

  useEffect(() => {
    if (mode !== 'nfc' || !scopeReady || nfc.status() !== 'available') return

    const controller = new AbortController()
    void nfc.scanSession(
      // The raw tag payload goes to the server exactly as presented. It is never
      // resolved, hashed, validated or stored on the device (AR20/NFR16).
      (payload: CardPayload) => submit({ card: payload }, 'nfc'),
      (error: CardNfcError) => setBanner({ tone: 'error', message: error.message, retryable: true }),
      controller.signal,
    )
    // Aborted on unmount, on a scope change and on a mode change, so no radio
    // outlives the screen it belongs to.
    return () => controller.abort()
  }, [mode, scopeReady, nfc, submit])

  // ---------------------------------------------------------------------
  // Capture: camera. The barcode printed on the ID card carries a ROLL
  // NUMBER, so a decode is resolved through search and marked by student id.
  // ---------------------------------------------------------------------

  const [cameraStatus, setCameraStatus] = useState<BarcodeScannerStatus | 'checking'>('checking')
  useEffect(() => {
    let live = true
    void camera.status().then((value) => {
      if (live) setCameraStatus(value)
    })
    return () => {
      live = false
    }
  }, [camera])

  const resolveRoll = useCallback(async (decoded: string) => {
    const roll = decoded.trim().toUpperCase()
    try {
      const results = await api.searchStudents(roll)
      // Exactly one, and its roll must equal the decoded value. A fuzzy match
      // would mark the wrong student, which is worse than not marking at all.
      const exact = results.data.filter((student) => student.roll_no.toUpperCase() === roll)
      if (exact.length !== 1) {
        setBanner({
          tone: 'error',
          message: `Code not recognised: no single student matches ${roll}. Try again or use search.`,
          retryable: true,
        })
        return
      }
      submit({ studentID: exact[0].student_id }, 'camera')
    } catch (error) {
      setBanner({ tone: 'error', message: markFailureMessage(error), retryable: true })
    }
  }, [api, submit])

  useEffect(() => {
    if (mode !== 'camera' || !scopeReady || cameraStatus !== 'available') return

    const controller = new AbortController()
    // Decodes repeat many times per second while the card is in frame; only the
    // first of a run is acted on.
    let lastDecoded = ''
    let lastAt = 0
    void camera.start(
      (text) => {
        const now = Date.now()
        if (text === lastDecoded && now - lastAt < 2_000) return
        lastDecoded = text
        lastAt = now
        void resolveRoll(text)
      },
      (error) => setBanner({ tone: 'error', message: error.message, retryable: true }),
      controller.signal,
    )
    // Stops every MediaStreamTrack: a camera light left on after the coordinator
    // switches modes is both a battery cost and an obvious trust problem.
    return () => controller.abort()
  }, [mode, scopeReady, camera, cameraStatus, resolveRoll])

  // ---------------------------------------------------------------------
  // Capture: search.
  // ---------------------------------------------------------------------

  const [searchTerm, setSearchTerm] = useState('')
  const trimmedSearch = searchTerm.trim()
  const search = useQuery({
    queryKey: ['student-search', trimmedSearch],
    queryFn: () => api.searchStudents(trimmedSearch),
    // The server's minimum. Below it there is nothing worth asking for.
    enabled: mode === 'search' && trimmedSearch.length >= 3,
    // Results are never persisted and never reused across renders of a
    // different query: a resolved roster on the device is the cached list AR20
    // forbids, however it arrived.
    gcTime: 0,
  })

  // dataUpdatedAt is the DEVICE clock reading at the moment the response landed,
  // which is what makes the server/device offset computable.
  const clockLabel = useSlotClock(status.data ?? null, status.dataUpdatedAt)

  if (!identity) return null

  const modeDisabledReason = (candidate: CaptureMode): string | null => {
    if (candidate === 'nfc' && nfcStatus !== 'available') {
      return nfcStatus === 'insecure'
        ? 'NFC needs an HTTPS page.'
        : 'This browser cannot use NFC. Use Chrome on Android.'
    }
    if (candidate === 'camera' && cameraStatus !== 'available' && cameraStatus !== 'checking') {
      return cameraStatus === 'insecure'
        ? 'The camera needs an HTTPS page.'
        : 'This device cannot scan ID card barcodes.'
    }
    return null
  }

  return (
    <main className="mark-screen">
      <header className="mark-header">
        <h1>Mark attendance</h1>
        <p className="mark-who">{identity.name}</p>
      </header>

      <section className="mark-scope" aria-label="Event and category">
        <label className="mark-field">
          <span>Event</span>
          <select
            value={eventID}
            onChange={(changed) => setEventID(changed.target.value)}
            disabled={eventOptions.length === 0}
          >
            {eventOptions.length === 0 && <option value="">No markable events</option>}
            {eventOptions.map((event) => (
              <option key={event.id} value={event.id}>{event.name}</option>
            ))}
          </select>
        </label>
        <label className="mark-field">
          <span>Category</span>
          <select
            value={categoryID}
            onChange={(changed) => setCategoryID(changed.target.value)}
            disabled={categoryOptions.length === 0}
          >
            {categoryOptions.length === 0 && <option value="">No categories</option>}
            {categoryOptions.map((category) => (
              <option key={category.id} value={category.id}>{category.name}</option>
            ))}
          </select>
        </label>
      </section>

      <section className="mark-meters" aria-label="Progress">
        <div className="mark-meter">
          <span className="mark-meter-value">{status.data?.counters.mine ?? '—'}</span>
          <span className="mark-meter-label">Marked by you</span>
        </div>
        <div className="mark-meter">
          <span className="mark-meter-value">{status.data?.counters.category_total ?? '—'}</span>
          <span className="mark-meter-label">Marked in total</span>
        </div>
        <div className="mark-meter mark-clock">
          <span className="mark-meter-value">{clockLabel.value}</span>
          <span className="mark-meter-label">{clockLabel.label}</span>
        </div>
      </section>

      <MarkBanner banner={banner} pending={mark.isPending} onDismiss={() => setBanner(null)} />

      <nav className="mark-modes" aria-label="Capture mode">
        {(['nfc', 'camera', 'search'] as const).map((candidate) => (
          <button
            key={candidate}
            type="button"
            className={candidate === mode ? 'mark-mode is-active' : 'mark-mode'}
            aria-pressed={candidate === mode}
            onClick={() => {
              setMode(candidate)
              setBanner(null)
            }}
          >
            {candidate === 'nfc' ? 'Tap card' : candidate === 'camera' ? 'Scan camera' : 'Search'}
          </button>
        ))}
      </nav>

      <section className="mark-capture" aria-label="Capture">
        {modeDisabledReason(mode) ? (
          <p role="alert" className="mark-unavailable">{modeDisabledReason(mode)}</p>
        ) : mode === 'nfc' ? (
          <p className="mark-hint">Hold each student's card against the back of the phone.</p>
        ) : mode === 'camera' ? (
          <p className="mark-hint">Point the camera at the barcode printed on the ID card.</p>
        ) : (
          <div className="mark-search">
            <label className="mark-field">
              <span>Find a student</span>
              <input
                type="search"
                value={searchTerm}
                placeholder="Roll number or name"
                onChange={(changed) => setSearchTerm(changed.target.value)}
              />
            </label>
            {trimmedSearch.length > 0 && trimmedSearch.length < 3 && (
              <p className="mark-hint">Type at least 3 characters.</p>
            )}
            <ul className="mark-results">
              {(search.data?.data ?? []).map((student: ResolvedCardStudent) => (
                <li key={student.student_id}>
                  <button
                    type="button"
                    onClick={() => submit({ studentID: student.student_id }, 'search')}
                  >
                    <strong>{student.display_name}</strong>
                    <span>{student.roll_no}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>
    </main>
  )
}

type MarkIdentity = { card: CardPayload } | { studentID: string }

/**
 * The adoption-critical surface.
 *
 * Full width, high contrast, `role="status"` for green and `role="alert"` for
 * red so a screen reader announces each appropriately — and so both double as
 * stable test selectors. It never auto-dismisses: a confirmation that vanishes
 * after a second is one the coordinator can miss while looking at the student.
 */
function MarkBanner({
  banner,
  pending,
  onDismiss,
}: {
  banner: Banner
  pending: boolean
  onDismiss: () => void
}) {
  if (pending) {
    return <p className="mark-banner is-pending">Marking…</p>
  }
  if (!banner) {
    return <p className="mark-banner is-idle">Ready to mark.</p>
  }
  if (banner.tone === 'success') {
    const { student, slot } = banner.result
    return (
      <p role="status" className="mark-banner is-green">
        <strong>{student.display_name}</strong>
        <span>{student.roll_no}</span>
        <span>Marked present · slot {slot}</span>
        <button type="button" onClick={onDismiss}>Dismiss</button>
      </p>
    )
  }
  return (
    <p role="alert" className="mark-banner is-red">
      <strong>Not recorded</strong>
      <span>{banner.message}</span>
      <button type="button" onClick={onDismiss}>Try again</button>
    </p>
  )
}

/**
 * The FR14 countdown.
 *
 * Ticks locally from the server's `ends_at`, offset by the difference between
 * `server_time` and the device clock at the moment of each poll — never from the
 * raw device clock, and never from a client-side slot table. A phone whose clock
 * is ten minutes fast would otherwise show a countdown the server disagrees with.
 */
function useSlotClock(
  // `slot` is optional AND nullable in the generated type: the contract requires
  // only server_time and counters, and `slot: null` is the explicit no-slot
  // state. Both absences mean the same thing here.
  status: { server_time: string; slot?: { ends_at: string } | null } | null,
  receivedAt: number,
) {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(timer)
  }, [])

  if (!status) return { value: '—', label: 'Slot' }
  if (!status.slot) {
    // The explicit no-slot state. Never 00:00, which reads as "about to open".
    return { value: 'No slot is open right now', label: 'Marks are rejected until the next slot' }
  }

  const offset = Date.parse(status.server_time) - receivedAt
  const remaining = Date.parse(status.slot.ends_at) - (now + offset)
  if (remaining <= 0) {
    // The window has run out between polls. The next poll settles it; until then
    // a negative countdown would be worse than an honest "closing".
    return { value: 'Slot closing', label: 'Waiting for the server' }
  }
  const minutes = Math.floor(remaining / 60_000)
  const seconds = Math.floor((remaining % 60_000) / 1_000)
  return {
    value: `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`,
    label: 'Left in this slot',
  }
}

/**
 * The coordinator-facing reason for each refusal.
 *
 * Keyed on the error CODE rather than the server's message: the API's wording is
 * written for an API consumer ("Rejected.", "Student is already marked present
 * for this slot."), and a coordinator at arm's length needs to know what to do
 * next, not what the server called it. Every code here already exists in the
 * closed error registry — this story adds none.
 */
const FAILURE_REASON: Record<string, string> = {
  SCAN_DUPLICATE: 'Already marked — this student is recorded present for this slot.',
  CARD_NOT_RECOGNIZED: 'Card not recognised. Try the camera or search instead.',
  SLOT_CLOSED: 'The slot is closed, so this mark was not recorded.',
  EVENT_ARCHIVED: 'This event is archived and can no longer be marked.',
  EVENT_NOT_FOUND: 'This event is no longer available. Pick another event.',
  CATEGORY_NOT_FOUND: 'This category is no longer available. Pick another category.',
  STUDENT_NOT_FOUND: 'That student is not on the roster.',
  RBAC_FORBIDDEN: 'You do not have permission to mark for this event and category.',
  VALIDATION_FAILED: 'The request was invalid — check the event and category and try again.',
}

/** Turns any failure into the specific reason the coordinator needs to act on. */
function markFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    return FAILURE_REASON[error.body.code] ?? error.body.message
  }
  if (error instanceof CardNfcError) return error.message
  // A network failure or timeout. Red, and explicitly NOT recorded — there is no
  // queue, so anything softer would be a lie.
  return 'The network did not answer, so this mark was not recorded. Check the connection and try again.'
}
