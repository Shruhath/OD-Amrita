import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { CaptureMethod, CardPayload, MarkAttendanceResult, ResolvedCardStudent } from '../../api/client'
import { ApiFailure } from '../../api/client'
import { AccountMenu } from '../../components/AccountMenu'
import { asString, usePersistentState } from '../../features/persistence/sessionState'
import { ThemeToggle } from '../../components/ThemeToggle'
import { Select } from '../../components/ui/Select'
import { useAuth } from '../../features/auth/context'
import { createBarcodeScanner, type BarcodeScannerStatus } from '../../features/marking/barcodeScanner'
import { CardNfcError, createWebNfcAdapter, type WebNfcStatus } from '../../features/nfc/webNfc'
import { enqueueScan, getPendingCount } from '../../lib/offline/storage'
import { registerSyncListener, flushOfflineQueue } from '../../lib/offline/sync'

/**
 * The coordinator marking surface.
 *
 * This is the one screen the event actually runs on. A coordinator holds the
 * phone at arm's length, marks ~90 students, and has exactly one signal to act
 * on: the banner. Everything here protects that signal.
 *
 * FIVE RULES THIS SCREEN HOLDS:
 *
 *  1. GREEN MEANS THE SERVER RETURNED 201, and nothing earlier. The long-term
 *     architecture fires green on a durable LOCAL write because the phone is
 *     offline-first — this MVP has no local write, so an optimistic green would
 *     be a claim the server may then refuse. A mark the server rejected must
 *     never have shown green, not even for a frame.
 *  2. A TIMEOUT IS RED. There is no queue here. Amber "queued" is the one state
 *     this screen must never have: it tells a coordinator a student is marked
 *     when nothing will ever send it.
 *  3. A FAILURE IS STICKY. A red state survives until the coordinator dismisses
 *     or retries it, and a later success can never quietly replace it. An
 *     unnoticed failure is the worst outcome on this screen, so "the last
 *     response wins" is exactly the wrong rule when two taps overlap.
 *  4. NOTHING IS CACHED. No card list, token, fingerprint set or roster reaches
 *     localStorage, sessionStorage or IndexedDB (AR20 no-list). The selected
 *     event and category may persist — an event id is public, not identity data.
 *  5. THE CLIENT COMPUTES NO TRUTH. Slots, counters and authorization are all
 *     server-owned; this screen only renders what the server said.
 */

type CaptureMode = 'nfc' | 'camera' | 'search'

/** The scope a capture was taken under. Carried with the capture, never assumed. */
type Scope = { eventID: string; categoryID: string }

/** Injected in tests. Production uses the real window-bound adapters. */
export type MarkScreenPorts = {
  nfc?: ReturnType<typeof createWebNfcAdapter>
  camera?: ReturnType<typeof createBarcodeScanner>
}

const STATUS_POLL_MS = 5_000
const SEARCH_DEBOUNCE_MS = 250
/** Matches the server's bounds exactly — see student.MinSearchRunes/MaxSearchRunes. */
const MIN_SEARCH_RUNES = 3
const MAX_SEARCH_RUNES = 64

export default function MarkScreen({ ports }: { ports?: MarkScreenPorts } = {}) {
  const { api, identity } = useAuth()
  const queryClient = useQueryClient()

  const nfc = useMemo(() => ports?.nfc ?? createWebNfcAdapter(window), [ports?.nfc])
  const camera = useMemo(
    () => ports?.camera ?? createBarcodeScanner(window, navigator),
    [ports?.camera],
  )

  const [mode, setMode] = useState<CaptureMode>('nfc')
  // Rule 4 above says the selected event and category MAY persist, and that an
  // event id is public rather than identity data. This is where that is used:
  // switching apps mid-event no longer costs the operator their scope. Nothing
  // the scope FETCHES is stored — the roster, the search results and every card
  // value stay in memory only.
  const [eventID, setEventID] = usePersistentState('mark.event.v1', '', asString)
  const [categoryID, setCategoryID] = usePersistentState('mark.category.v1', '', asString)

  // ---------------------------------------------------------------------
  // The banner, split in two on purpose.
  //
  // `failure` is STICKY and outranks `success`. Two cards tapped inside one
  // round trip produce two concurrent requests whose responses can land in
  // either order; with a single banner the later response won, so a refusal
  // could be erased by the next student's green before the coordinator looked
  // up. Keeping them apart means a failure is never lost to a race.
  // ---------------------------------------------------------------------
  const [failure, setFailure] = useState<string | null>(null)
  const [success, setSuccess] = useState<MarkAttendanceResult | null>(null)
  const [pendingCount, setPendingCount] = useState<number>(0)

  useEffect(() => {
    void getPendingCount().then(setPendingCount)
    const cleanup = registerSyncListener(api, (count) => {
      setPendingCount((prev) => Math.max(0, prev - count))
    })
    // Also trigger initial sync check if online
    if (navigator.onLine) {
      void flushOfflineQueue(api, (count) => {
        setPendingCount((prev) => Math.max(0, prev - count))
      })
    }
    return cleanup
  }, [api])

  const reportFailure = useCallback((message: string) => {
    setFailure(message)
    setSuccess(null)
  }, [])

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

  // Reconciles the selection against the server's list, rather than only filling
  // an empty one. If an event is archived mid-session it disappears from this
  // list, and a stale id left selected would keep the status poll and every tap
  // pointed at a scope the server now refuses — while the <select> rendered a
  // value with no matching option. The category effect below always did this;
  // the event effect was the asymmetric one.
  useEffect(() => {
    if (eventOptions.length === 0) return
    if (!eventOptions.some((event) => event.id === eventID)) {
      setEventID(eventOptions[0].id)
    }
  }, [eventID, eventOptions, setEventID])

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
  }, [categoryID, categoryOptions, setCategoryID])

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
  const fallbackCredit = status.data?.fallback_credit
  const fallbackExhausted = fallbackCredit?.exhausted ?? false
  const fallbackWarningThreshold = fallbackCredit?.warning_threshold ?? 1

  // ---------------------------------------------------------------------
  // The mark itself.
  // ---------------------------------------------------------------------

  const mark = useMutation({
    mutationFn: (input: { identity: MarkIdentity; capture: CaptureMethod; scope: Scope }) =>
      api.mark({
        // The scope travels WITH the capture rather than being read from state
        // at request time. A camera decode resolves through the search endpoint
        // first, so the coordinator can change event or category while that
        // round trip is in flight — and the student decoded under one scope
        // would otherwise be POSTed against another. The server would accept it,
        // because the new scope is genuinely markable: a silent mis-mark that no
        // server-side check can catch.
        event_id: input.scope.eventID,
        category_id: input.scope.categoryID,
        capture_method: input.capture,
        ...('card' in input.identity
          ? { card: input.identity.card }
          : { student_id: input.identity.studentID }),
      }),
    onSuccess(result, variables) {
      // Green fires HERE — on the server's 201 — and nowhere earlier.
      setSuccess(result)
      // Refetched rather than incremented: a rejected mark must never be able to
      // inflate the number, and the server's count is the only true one.
      void queryClient.invalidateQueries({
        queryKey: ['mark-status', variables.scope.eventID, variables.scope.categoryID],
      })
    },
    async onError(error, variables) {
      if (!navigator.onLine || (error instanceof TypeError && error.message.includes('Failed to fetch'))) {
        try {
          await enqueueScan({
            eventID: variables.scope.eventID,
            categoryID: variables.scope.categoryID,
            captureMethod: variables.capture,
            ...('card' in variables.identity
              ? { cardPayload: variables.identity.card }
              : { studentToken: variables.identity.studentID }),
          })
          setPendingCount((c) => c + 1)
          setSuccess({
            ok: true,
            status: 'marked_present',
            scanned_at: new Date().toISOString(),
            student: { student_id: 'Offline Scan', roll_number: 'Queued', name: 'Saved to Device' },
          } as any)
          return
        } catch {
          // Fallthrough to regular failure reporting if storage fails
        }
      }
      reportFailure(markFailureMessage(error))
      if (error instanceof ApiFailure && error.body.code === 'CAPTURE_CREDIT_LIMIT_REACHED') {
        void queryClient.invalidateQueries({
          queryKey: ['mark-status', variables.scope.eventID, variables.scope.categoryID],
        })
      }
    },
  })

  const markRef = useRef(mark.mutate)
  markRef.current = mark.mutate

  // The live scope, for the async capture paths to compare against.
  const scopeRef = useRef<Scope>({ eventID, categoryID })
  scopeRef.current = { eventID, categoryID }

  const submit = useCallback((identity: MarkIdentity, capture: CaptureMethod, scope: Scope) => {
    // A capture taken with no scope selected would POST an empty event_id and
    // come back as a validation error naming controls the coordinator cannot
    // fix. Refuse it here, and say which selection is missing.
    if (!scope.eventID || !scope.categoryID) {
      reportFailure('Choose an event and a category before marking.')
      return
    }
    markRef.current({ identity, capture, scope })
  }, [reportFailure])

  // ---------------------------------------------------------------------
  // Retry. This re-ARMS the capture session; it does not merely hide the red.
  //
  // A refused NFC permission or a switched-off radio tears the session down.
  // Clearing the banner alone left every subsequent tap doing nothing at all,
  // with no indication — the mode was silently dead until the coordinator
  // switched modes and back, or reloaded. `captureEpoch` is in both capture
  // effects' dependencies, so bumping it starts a fresh session.
  // ---------------------------------------------------------------------
  const [captureEpoch, setCaptureEpoch] = useState(0)

  const retryCapture = useCallback(() => {
    setFailure(null)
    setSuccess(null)
    setCaptureEpoch((epoch) => epoch + 1)
  }, [])

  // ---------------------------------------------------------------------
  // Capture: NFC. One continuous session per (mode, scope) — a coordinator
  // must not press "start" between students.
  // ---------------------------------------------------------------------

  const [nfcStatus, setNfcStatus] = useState<WebNfcStatus>(() => nfc.status())
  useEffect(() => setNfcStatus(nfc.status()), [nfc])

  useEffect(() => {
    if (mode !== 'nfc' || !scopeReady || nfc.status() !== 'available') return

    // Captured for this session. The debounce map inside scanSession is
    // session-scoped, so a session that outlived a scope change would also
    // swallow a re-tap of the same card in the NEW category — silently, with a
    // stale green still on screen reading "Marked present".
    const scope: Scope = { eventID, categoryID }
    const controller = new AbortController()
    void nfc.scanSession(
      // The raw tag payload goes to the server exactly as presented. It is never
      // resolved, hashed, validated or stored on the device (AR20/NFR16).
      (payload: CardPayload) => submit({ card: payload }, 'nfc', scope),
      (error: CardNfcError) => reportFailure(error.message),
      controller.signal,
    )
    // Aborted on unmount, on a mode change, on a retry, and — via the ids in the
    // dependency list rather than a `scopeReady` boolean — on any event or
    // category change. No radio outlives the scope it was armed for.
    return () => controller.abort()
  }, [mode, scopeReady, eventID, categoryID, captureEpoch, nfc, submit, reportFailure])

  // ---------------------------------------------------------------------
  // Capture: camera. The barcode printed on the ID card carries a ROLL
  // NUMBER, so a decode is resolved through search and marked by student id.
  // ---------------------------------------------------------------------

  const [cameraStatus, setCameraStatus] = useState<BarcodeScannerStatus | 'checking'>('checking')
  useEffect(() => {
    let live = true
    // Re-queried on retry: a coordinator who granted camera permission after a
    // refusal must be able to reach 'available' without reloading.
    void camera.status().then((value) => {
      if (live) setCameraStatus(value)
    })
    return () => {
      live = false
    }
  }, [camera, captureEpoch])

  // The live preview. Aiming a phone at a barcode with nothing on screen is not
  // a usable capture mode, so the element the detector reads frames from is the
  // element the coordinator sees.
  const [preview, setPreview] = useState<HTMLVideoElement | null>(null)
  const previewHost = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const host = previewHost.current
    if (!host || !preview) return
    preview.className = 'mark-preview-video'
    host.appendChild(preview)
    return () => {
      if (preview.parentNode === host) host.removeChild(preview)
    }
  }, [preview])

  const resolveRoll = useCallback(async (decoded: string, scope: Scope) => {
    const roll = decoded.trim().toUpperCase()
    try {
      const results = await api.searchStudents(roll)
      // The scope may have moved while the search was in flight. Marking the
      // resolved student against the NEW scope would be a silent mis-mark the
      // server cannot detect, because that scope is legitimately markable.
      if (scope.eventID !== scopeRef.current.eventID || scope.categoryID !== scopeRef.current.categoryID) {
        return
      }
      // Exactly one, and its roll must equal the decoded value. A fuzzy match
      // would mark the wrong student, which is worse than not marking at all.
      const exact = results.data.filter((student) => student.roll_no.toUpperCase() === roll)
      if (exact.length !== 1) {
        reportFailure(`Code not recognised: no single student matches ${roll}. Try again or use search.`)
        return
      }
      submit({ studentID: exact[0].student_id }, 'camera', scope)
    } catch (error) {
      reportFailure(markFailureMessage(error))
    }
  }, [api, submit, reportFailure])

  useEffect(() => {
    if (mode !== 'camera' || !scopeReady || cameraStatus !== 'available' || fallbackExhausted) return

    const scope: Scope = { eventID, categoryID }
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
        void resolveRoll(text, scope)
      },
      (error) => reportFailure(error.message),
      controller.signal,
      setPreview,
    )
    // Stops every MediaStreamTrack: a camera light left on after the coordinator
    // switches modes is both a battery cost and an obvious trust problem.
    return () => {
      controller.abort()
      setPreview(null)
    }
  }, [mode, scopeReady, eventID, categoryID, captureEpoch, camera, cameraStatus, fallbackExhausted, resolveRoll, reportFailure])

  // ---------------------------------------------------------------------
  // Capture: search.
  // ---------------------------------------------------------------------

  const [searchTerm, setSearchTerm] = useState('')
  const trimmedSearch = searchTerm.trim()
  // Runes, not UTF-16 units, because that is what the server counts. `.length`
  // let a 2-character astral query through to a guaranteed 400, and imposed no
  // maximum at all, so an over-long paste was always a round trip to a refusal.
  const searchRunes = useMemo(() => [...trimmedSearch].length, [trimmedSearch])

  // Debounced so a typed roll number is one query, not one per keystroke. The
  // prefix search is documented to plan as a sequential scan under a non-C
  // collation, and a 16-character roll fired 14 of them.
  const [debouncedSearch, setDebouncedSearch] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(trimmedSearch), SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [trimmedSearch])

  const debouncedRunes = useMemo(() => [...debouncedSearch].length, [debouncedSearch])

  const search = useQuery({
    queryKey: ['student-search', debouncedSearch],
    queryFn: () => api.searchStudents(debouncedSearch),
    // The server's bounds, mirrored exactly. Below or above them there is
    // nothing worth asking for.
    enabled: mode === 'search' && debouncedRunes >= MIN_SEARCH_RUNES && debouncedRunes <= MAX_SEARCH_RUNES,
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
    if ((candidate === 'camera' || candidate === 'search') && fallbackExhausted) {
      return 'Fallback credits are exhausted. Use NFC or request an admin reset or raise.'
    }
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
        {/* Presentational only — aria-hidden, so the heading below is still the
            first thing announced on this screen. */}
        <span className="brand-mark" aria-hidden="true">OD</span>
        <h1>Mark attendance</h1>
        <ThemeToggle />
        {/* This screen had no way to sign out at all — a coordinator marking on
            a shared phone could not hand it over. The same control as the
            portal, so the account lives in one place on every surface. */}
        <AccountMenu />
      </header>

      <section className="mark-scope" aria-label="Event and category">
        <label className="mark-field">
          <span>Event</span>
          <Select
            value={eventID}
            onChange={setEventID}
            disabled={eventOptions.length === 0}
            placeholder="No markable events"
            options={eventOptions.map((event) => ({ value: event.id, label: event.name }))}
          />
        </label>
        <label className="mark-field">
          <span>Category</span>
          <Select
            value={categoryID}
            onChange={setCategoryID}
            disabled={categoryOptions.length === 0}
            placeholder="No categories"
            options={categoryOptions.map((category) => ({ value: category.id, label: category.name }))}
          />
        </label>
      </section>

      {/*
        A failed read is stated, never rendered as emptiness. "No markable
        events" and "the server refused your event list" look identical
        otherwise, and only one of them means the coordinator should stop
        tapping and find an administrator.
      */}
      {events.isError && (
        <p role="alert" className="mark-read-error">
          Your events could not be loaded. {markFailureMessage(events.error)}
          {' '}<button type="button" onClick={() => void events.refetch()}>Retry</button>
        </p>
      )}
      {categories.isError && (
        <p role="alert" className="mark-read-error">
          The categories could not be loaded. {markFailureMessage(categories.error)}
          {' '}<button type="button" onClick={() => void categories.refetch()}>Retry</button>
        </p>
      )}
      {status.isError && (
        <p role="alert" className="mark-read-error">
          Counters and the slot clock are unavailable. {markFailureMessage(status.error)}
        </p>
      )}

      {/*
        Every meter here is scoped to TODAY, and each label says so.

        Under Story 0.12a all three of these reset at campus midnight: the two
        counters gained `AND od_date = @od_date` and the credit pool is keyed
        `(event_id, user_id, usage_date)`. On day 2 of a festival a coordinator
        who marked 200 students yesterday opens this screen to "0", "0" and a
        full credit pool. That is CORRECT — but labelled "Marked by you" on a
        thirty-day event it reads as data loss, and a coordinator who believes
        the counter is wrong is a coordinator who stops trusting the screen.
      */}
      <section className="mark-meters" aria-label="Today's progress">
        <div className="mark-meter">
          <span className="mark-meter-value">{status.data?.counters.mine ?? '—'}</span>
          <span className="mark-meter-label">Marked by you today</span>
        </div>
        <div className="mark-meter">
          <span className="mark-meter-value">{status.data?.counters.category_total ?? '—'}</span>
          <span className="mark-meter-label">Marked in total today</span>
        </div>
        <div className="mark-meter mark-clock">
          <span className="mark-meter-value">{clockLabel.value}</span>
          <span className="mark-meter-label">{clockLabel.label}</span>
        </div>
        <div className={fallbackExhausted ? 'mark-meter is-warning' : 'mark-meter'}>
          <span className="mark-meter-value">
            {fallbackCredit ? `${fallbackCredit.remaining}/${fallbackCredit.limit}` : '—'}
          </span>
          <span className="mark-meter-label">Fallback credits today</span>
        </div>
      </section>

      {/*
        Three states, three weights. All three used to render in the red
        read-error panel, so "18 fallback credits remaining" — a completely
        healthy state — looked exactly like a failure. Only the wording and the
        class differ here; the roles and the copy are unchanged.
      */}
      {fallbackCredit && fallbackCredit.remaining <= fallbackWarningThreshold && !fallbackCredit.exhausted && (
        <p className="mark-note is-caution" aria-live="polite">
          {fallbackCredit.remaining} fallback credit{fallbackCredit.remaining === 1 ? '' : 's'} left. Fallback marks are almost exhausted.
        </p>
      )}
      {fallbackCredit && fallbackCredit.remaining > fallbackWarningThreshold && !fallbackCredit.exhausted && (
        <p className="mark-note" aria-live="polite">
          {fallbackCredit.remaining} fallback credits remaining.
        </p>
      )}
      {fallbackCredit?.exhausted && (
        <p className="mark-read-error" aria-live="polite">
          Fallback credits are exhausted. Camera and search are disabled; use NFC or request an admin reset or raise.
        </p>
      )}

      <MarkBanner
        failure={failure}
        success={success}
        onDismiss={() => {
          setFailure(null)
          setSuccess(null)
        }}
        onRetry={retryCapture}
      />

      {/*
        Progress sits OUTSIDE the banner. Rendering "Marking…" in the banner's
        place would hide an unacknowledged red every time the next card was
        tapped, which is the one thing rule 3 exists to stop.

        `aria-live` rather than `role="status"`: the green banner owns that role,
        and a second status node would make "the confirmation" ambiguous both to
        a screen reader and to every test that selects the banner by role.
      */}
      <p className="mark-activity" aria-live="polite">{mark.isPending ? 'Marking…' : ''}</p>

      <nav className="mark-modes" aria-label="Capture mode">
        {(['nfc', 'camera', 'search'] as const).map((candidate) => (
          <button
            key={candidate}
            type="button"
            className={candidate === mode ? 'mark-mode is-active' : 'mark-mode'}
            aria-pressed={candidate === mode}
            disabled={Boolean(modeDisabledReason(candidate))}
            onClick={() => {
              setMode(candidate)
              setFailure(null)
              setSuccess(null)
            }}
          >
            {candidate === 'nfc' ? 'Tap card' : candidate === 'camera' ? 'Scan camera' : 'Search'}
          </button>
        ))}
      </nav>

      <section className="mark-capture" aria-label="Capture">
        {!scopeReady && !events.isError && (
          <p className="mark-hint">Choose an event and a category to start marking.</p>
        )}
        {modeDisabledReason(mode) ? (
          <p role="alert" className="mark-unavailable">{modeDisabledReason(mode)}</p>
        ) : mode === 'nfc' ? (
          <p className="mark-hint">Hold each student's card against the back of the phone.</p>
        ) : mode === 'camera' ? (
          <>
            <p className="mark-hint">Point the camera at the barcode printed on the ID card.</p>
            <div className="mark-preview" ref={previewHost} aria-label="Camera preview" />
          </>
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
            {searchRunes > 0 && searchRunes < MIN_SEARCH_RUNES && (
              <p className="mark-hint">Type at least {MIN_SEARCH_RUNES} characters.</p>
            )}
            {searchRunes > MAX_SEARCH_RUNES && (
              <p className="mark-hint">That is too long — {MAX_SEARCH_RUNES} characters at most.</p>
            )}
            {search.isError && (
              <p role="alert" className="mark-read-error">
                The search failed. {markFailureMessage(search.error)}
              </p>
            )}
            <ul className="mark-results">
              {(search.data?.data ?? []).map((student: ResolvedCardStudent) => (
                <li key={student.student_id}>
                  <button
                    type="button"
                    disabled={!scopeReady || fallbackExhausted}
                    onClick={() => submit({ studentID: student.student_id }, 'search', scopeRef.current)}
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
 *
 * A failure OUTRANKS a success. When two taps overlap, the responses can land in
 * either order, and letting the later one win meant a refusal could be replaced
 * by the next student's green before it was ever read.
 */
function MarkBanner({
  failure,
  success,
  onDismiss,
  onRetry,
}: {
  failure: string | null
  success: MarkAttendanceResult | null
  onDismiss: () => void
  onRetry: () => void
}) {
  if (failure) {
    return (
      <p role="alert" className="mark-banner is-red">
        <strong>Not recorded</strong>
        <span>{failure}</span>
        {/*
          This RE-ARMS the capture session rather than only clearing the banner.
          After a refused permission or a switched-off radio the session is torn
          down, and a button that merely hid the message left the mode dead.
        */}
        <button type="button" onClick={onRetry}>Try again</button>
      </p>
    )
  }
  if (success) {
    const { student, slot } = success
    return (
      <p role="status" className="mark-banner is-green">
        <strong>{student.display_name}</strong>
        <span>{student.roll_no}</span>
        <span>Marked present · slot {slot}</span>
        <button type="button" onClick={onDismiss}>Dismiss</button>
      </p>
    )
  }
  return <p className="mark-banner is-idle">Ready to mark.</p>
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
  // `slot` is required AND nullable in the contract, so the key is always on the
  // wire and `slot: null` is the explicit no-slot state. It is typed optional
  // here only to tolerate a response from an older server.
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
    //
    // This is ALSO the state for a day outside a multi-day event's range: the
    // server answers `slot: null` there too, and deliberately so. It must not
    // grow a second, scarier branch — a coordinator opening the app the day
    // before a festival should see that nothing is on, not a red failure they
    // will try to debug at the venue.
    return { value: 'No slot is open right now', label: 'Marks are rejected until the next slot' }
  }

  const offset = Date.parse(status.server_time) - receivedAt
  const remaining = Date.parse(status.slot.ends_at) - (now + offset)
  // An unparseable timestamp makes `remaining` NaN, and `NaN <= 0` is FALSE — so
  // without this the "Slot closing" guard below is skipped and the meter renders
  // `NaN:NaN`, which at arm's length reads as a broken phone.
  if (!Number.isFinite(remaining)) return { value: '—', label: 'Slot' }
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
  CAPTURE_CREDIT_LIMIT_REACHED: 'Fallback credits are exhausted. Use NFC or ask an admin to reset or raise the cap.',
  RBAC_FORBIDDEN: 'You do not have permission to mark for this event and category.',
  VALIDATION_FAILED: 'The request was invalid — check the event and category and try again.',
}

/** Turns any failure into the specific reason the coordinator needs to act on. */
function markFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    // `body` is normalised into a real envelope by the API client, so `code` and
    // `message` are always present even when the server answered with plain text
    // or an HTML error page. Without that, both were `undefined` here and the red
    // banner rendered with no reason in it at all.
    return FAILURE_REASON[error.body.code] ?? error.body.message
  }
  if (error instanceof CardNfcError) return error.message
  // A network failure or timeout. Red, and explicitly NOT recorded — there is no
  // queue, so anything softer would be a lie.
  return 'The network did not answer, so this mark was not recorded. Check the connection and try again.'
}
