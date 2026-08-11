import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { EventApi, ResolvedCardStudent, StudentCollection } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { Select } from '../../../components/ui/Select'
import { asStringRecord, usePersistentState } from '../../../features/persistence/sessionState'
import { useAuth } from '../../../features/auth/context'
import { CardNfcError, createWebNfcAdapter, type WebNfcStatus } from '../../../features/nfc/webNfc'
import { messageOf } from '../events/format'

/**
 * Per-student progress.
 *
 * `written` and `verified` are deliberately separate states. A resolved write
 * promise only proves the phone believed it wrote; verification is a fresh read
 * plus a server resolve. Collapsing them would let a card that silently failed
 * to take the write count as done, and that student arrives at the event with a
 * blank card.
 */
type CardStatus = 'pending' | 'writing' | 'written' | 'verifying' | 'verified' | 'failed'

type CardProgress = {
  status: CardStatus
  /** Operator-facing note. Never holds a token, payload, or key material. */
  note?: string
  updatedAt: number
}

type Filters = { branchID: string; batch: string; section: string }

const STATUS_LABEL: Record<CardStatus, string> = {
  pending: 'Not written',
  writing: 'Writing…',
  written: 'Written, awaiting verify',
  verifying: 'Verifying…',
  verified: 'Verified',
  failed: 'Failed',
}

/**
 * Progress is keyed by student id and holds no card material, so it is safe to
 * keep across a reload. Raw tokens and payloads never reach storage (AC 6).
 */
const PROGRESS_STORAGE_KEY = 'od-amrita.card-populator.progress.v1'
const ROSTER_PAGE_SIZE = 100

function readStoredProgress(): Record<string, CardProgress> {
  try {
    const raw = window.sessionStorage.getItem(PROGRESS_STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return {}

    // Re-validated on the way in rather than trusted: storage is editable, and
    // an unknown status would render as a blank cell that reads like progress.
    const restored: Record<string, CardProgress> = {}
    for (const [studentID, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof value !== 'object' || value === null) continue
      const { status, note, updatedAt } = value as Record<string, unknown>
      if (typeof status !== 'string' || !(status in STATUS_LABEL)) continue
      restored[studentID] = {
        // An interrupted in-flight state cannot be resumed after a reload, so
        // it is restored as its last settled meaning rather than a spinner that
        // never resolves.
        status: status === 'writing' || status === 'verifying' ? 'pending' : (status as CardStatus),
        note: typeof note === 'string' ? note : undefined,
        updatedAt: typeof updatedAt === 'number' ? updatedAt : 0,
      }
    }
    return restored
  } catch {
    return {}
  }
}

export default function CardPopulatorPortal() {
  const { api } = useAuth()

  const nfc = useMemo(() => createWebNfcAdapter(window), [])
  const [nfcStatus, setNfcStatus] = useState<WebNfcStatus>(() => nfc.status())
  useEffect(() => setNfcStatus(nfc.status()), [nfc])

  // BOTH the draft and the applied filters survive the app being backgrounded.
  //
  // `applied` is the one that matters: the roster query is keyed on it, so
  // restoring it re-fetches the same roster from the server and the operator
  // comes back to the screen they left. The roster itself is deliberately NOT
  // stored — a resolved list of students on the device is exactly what AR20
  // forbids — which is why restoring the SELECTION rather than the data is both
  // the safe answer and the sufficient one.
  //
  // `draft` is stored too, so a half-filled form is not lost either.
  const [draft, setDraft] = usePersistentState<Filters>(
    'card-populator.draft.v1',
    { branchID: '', batch: '', section: '' },
    asStringRecord(['branchID', 'batch', 'section']),
  )
  const [applied, setApplied] = usePersistentState<Filters | null>(
    'card-populator.applied.v1',
    null,
    (raw) => (raw === null ? null : asStringRecord(['branchID', 'batch', 'section'])(raw)),
  )
  useEffect(() => {
    if (applied !== null && (!draft.branchID || !draft.batch || !draft.section)) {
      setDraft(applied)
    }
  }, [applied, draft.batch, draft.branchID, draft.section, setDraft])
  const [progress, setProgress] = useState<Record<string, CardProgress>>(readStoredProgress)
  const [activeStudentID, setActiveStudentID] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [nfcPermissionReady, setNfcPermissionReady] = useState(false)

  // One in-flight card operation at a time. The controller cancels a stuck read
  // when the operator moves on, so a tap meant for the previous student cannot
  // land on the next one.
  const abortRef = useRef<AbortController | null>(null)
  useEffect(() => () => abortRef.current?.abort(), [])

  useEffect(() => {
    try {
      window.sessionStorage.setItem(PROGRESS_STORAGE_KEY, JSON.stringify(progress))
    } catch {
      // Storage being unavailable (private mode, quota) must not break writing
      // cards — progress simply stops surviving a reload.
    }
  }, [progress])

  const branches = useQuery({
    queryKey: ['branches'],
    queryFn: () => api.branches(),
  })

  const roster = useQuery({
    queryKey: ['card-populator-students', applied],
    queryFn: () => loadCompleteRoster(api, applied as Filters),
    enabled: applied !== null && nfcPermissionReady,
  })

  /**
   * THE DURABLE HALF OF PROGRESS.
   *
   * Everything above this line lives in sessionStorage, which is exactly the
   * problem it used to be the whole answer to: it does not survive signing out
   * (persisted state is cleared deliberately, so a shared phone does not hand
   * the next operator the previous one's screen) and it never crossed to a
   * second device. An admin who wrote and verified a section, signed out, and
   * came back was told 0 of 0 verified with every row reading "Not written".
   *
   * The server knows better, and it is the only thing that does — so the counts
   * and the per-row state come from here, and the local progress is layered on
   * top only for the states the server cannot have an opinion about (an
   * operation in flight right now, and a failure on this device).
   */
  const serverStatus = useQuery({
    queryKey: ['card-populator-status', applied],
    queryFn: () => api.sectionCardStatus(applied as Filters),
    enabled: applied !== null && nfcPermissionReady,
  })

  const serverByStudent = useMemo(() => {
    const map = new Map<string, { issued: boolean; verifiedAt: string | null }>()
    for (const row of serverStatus.data?.data ?? []) {
      map.set(row.student_id, { issued: row.issued, verifiedAt: row.verified_at })
    }
    return map
  }, [serverStatus.data])

  // Memoised so the identity is stable between renders: a fresh array on every
  // render would re-run the counts memo on each keystroke elsewhere on the page.
  const students = useMemo(() => roster.data?.data ?? [], [roster.data])

  const setStudentProgress = useCallback((studentID: string, next: Omit<CardProgress, 'updatedAt'>) => {
    setProgress((current) => ({ ...current, [studentID]: { ...next, updatedAt: Date.now() } }))
  }, [])

  /**
   * The state to SHOW, from both sources.
   *
   * Local wins while something is happening on this device — an in-flight
   * operation, or a failure the server never saw — because that is the only
   * place those facts exist. Otherwise the server wins, including over a stale
   * local 'verified' from a card that has since been re-sealed.
   *
   * A server 'verified' is never downgraded by a local 'pending': a fresh device
   * has no local entry at all, and treating that absence as "not written" is the
   * whole defect.
   */
  const statusOf = useCallback(
    (studentID: string): CardStatus => {
      const local = progress[studentID]?.status
      if (local === 'writing' || local === 'verifying' || local === 'failed') return local
      const server = serverByStudent.get(studentID)
      if (server?.verifiedAt) return 'verified'
      // A local 'verified' with no server record is a verification this device
      // did before the server tracked them, or one whose progress note did not
      // save. Believing it is better than losing it — the operator watched it
      // happen.
      if (local === 'verified') return 'verified'
      if (server?.issued) return 'written'
      return local ?? 'pending'
    },
    [progress, serverByStudent],
  )

  // A verified card is also a written one, so `written` counts both. Reporting
  // them as disjoint would make "written + verified" undercount the section and
  // read as if cards had been lost.
  const counts = useMemo(() => {
    let written = 0
    let verified = 0
    let failed = 0
    for (const student of students) {
      const status = statusOf(student.student_id)
      if (status === 'verified') {
        verified += 1
        written += 1
      } else if (status === 'written') {
        written += 1
      } else if (status === 'failed') {
        failed += 1
      }
    }
    return { written, verified, failed, total: roster.data?.total ?? students.length }
  }, [roster.data?.total, students, statusOf])

  const loadRoster = async (event: FormEvent) => {
    event.preventDefault()
    setFormError(null)
    const next = {
      branchID: draft.branchID,
      batch: draft.batch.trim(),
      section: draft.section.trim().toUpperCase(),
    }
    if (!next.branchID || !next.batch || !next.section) {
      setFormError('Choose a branch, batch and section before loading the roster.')
      return
    }
    setDraft(next)
    try {
      await nfc.ensurePermission()
    } catch (cause) {
      setNfcStatus(nfc.status())
      setNfcPermissionReady(false)
      setFormError(messageOf(cause, 'NFC permission is required before loading the card roster.'))
      return
    }
    setNfcPermissionReady(true)
    abortRef.current?.abort()
    setApplied(next)
  }

  const beginOperation = (studentID: string) => {
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setActiveStudentID(studentID)
    return controller
  }

  const finishOperation = (controller: AbortController) => {
    if (abortRef.current === controller) {
      abortRef.current = null
      setActiveStudentID(null)
    }
  }

  const cancelOperation = () => {
    abortRef.current?.abort()
  }

  const failWith = (student: ResolvedCardStudent, cause: unknown, fallback: string) => {
    const message = safeProgressNote(cause instanceof CardNfcError ? cause.message : messageOf(cause, fallback))
    setStudentProgress(student.student_id, { status: 'failed', note: message })
  }

  /**
   * Seal, then write. The seal is fetched per press rather than cached: a cached
   * token would let a retry write a payload the ledger may already have
   * superseded.
   */
  const writeCard = async (student: ResolvedCardStudent) => {
    const controller = beginOperation(student.student_id)
    setStudentProgress(student.student_id, { status: 'writing' })
    try {
      const sealed = await api.sealStudentCard(student.student_id)
      // Rebuilt, never spread: the seal response also carries the student block,
      // and spreading it would put identity on the card.
      await nfc.write({ version: sealed.payload.version, token: sealed.payload.token }, controller.signal)
      setStudentProgress(student.student_id, { status: 'written' })
      // The seal created (or refreshed) the token row, so `issued` has moved.
      void serverStatus.refetch()
    } catch (cause) {
      failWith(student, cause, 'The card could not be written')
    } finally {
      finishOperation(controller)
    }
  }

  /**
   * Read the card back and let the SERVER say whose it is. The token is never
   * compared locally — the seal key is not on this device (NFR16, AR20) — so
   * verification is the resolved student id matching the selected row.
   */
  const verifyCard = async (student: ResolvedCardStudent) => {
    const controller = beginOperation(student.student_id)
    setStudentProgress(student.student_id, { status: 'verifying' })
    try {
      const payload = await nfc.read(controller.signal)
      const resolved = await api.resolveCardToken({ version: payload.version, token: payload.token })
      if (resolved.student_id !== student.student_id) {
        setStudentProgress(student.student_id, {
          status: 'failed',
          note: `This card belongs to ${resolved.display_name} (${resolved.roll_no}). Write it again for ${student.display_name}.`,
        })
        return
      }
      setStudentProgress(student.student_id, {
        status: 'verified',
        note: `Verified ${resolved.display_name} (${resolved.roll_no})`,
      })
      // Re-read the durable count. The server has just recorded this
      // verification, and the number on screen is the one the operator uses to
      // decide whether the section is done.
      void serverStatus.refetch()
    } catch (cause) {
      failWith(student, cause, 'The card could not be verified')
    } finally {
      finishOperation(controller)
    }
  }

  const cardsUsable = nfcStatus === 'available'

  return (
    <PortalShell>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Administration · Cards</p>
          <h1>Card populator</h1>
          <p>Write each student's sealed token to their NFC card, then read it back to verify it.</p>
        </div>
      </div>

      {nfcStatus !== 'available' && (
        <p className="notice error" role="alert">
          {nfcStatus === 'insecure'
            ? 'NFC needs an HTTPS page. Open the populator over HTTPS to write cards.'
            : 'This browser cannot use NFC. Use Chrome on Android to write cards.'}
        </p>
      )}

      <section className="detail-section roster-import">
        <h2>Choose a section</h2>
        <form className="populator-filters" onSubmit={loadRoster} noValidate>
          <div>
            <label htmlFor="populator-branch">Branch</label>
            <Select
              id="populator-branch"
              value={draft.branchID}
              onChange={(value) => setDraft((current) => ({ ...current, branchID: value }))}
              options={[
                { value: '', label: 'Select a branch' },
                ...(branches.data?.data ?? []).map((branch) => ({ value: branch.id, label: branch.code })),
              ]}
            />
          </div>
          <div>
            <label htmlFor="populator-batch">Batch</label>
            <input
              id="populator-batch"
              type="text"
              value={draft.batch}
              onChange={(event) => setDraft((current) => ({ ...current, batch: event.target.value }))}
            />
          </div>
          <div>
            <label htmlFor="populator-section">Section</label>
            <input
              id="populator-section"
              type="text"
              value={draft.section}
              onChange={(event) => setDraft((current) => ({ ...current, section: event.target.value }))}
            />
          </div>
          <button className="primary" type="submit" disabled={roster.isFetching}>
            {roster.isFetching ? 'Loading…' : 'Load roster'}
          </button>
        </form>
        {formError && <p className="notice error" role="alert">{formError}</p>}
        {roster.isError && (
          <p className="notice error" role="alert">{messageOf(roster.error, 'The roster could not be loaded')}</p>
        )}
      </section>

      {applied !== null && nfcPermissionReady && !roster.isError && (
        <section className="detail-section populator-roster">
          <h2>Section progress</h2>
          {/* aria-live so the operator hears progress without watching the counts. */}
          <p className="populator-counts" role="status">
            {counts.written} written · {counts.verified} verified · {counts.failed} failed · {counts.total} in section
          </p>
          {/*
            A failed status read must SAY so. Falling back to local-only progress
            silently is how "0 of 0 verified" looked like a true answer instead of
            a missing one.
          */}
          {serverStatus.isError && (
            <p className="notice error" role="alert">
              {messageOf(serverStatus.error, 'Saved progress could not be loaded')} — the counts below show only
              what this device has done in this session.
            </p>
          )}

          {students.length === 0 && !roster.isFetching ? (
            <p className="empty-note">No students match this branch, batch and section.</p>
          ) : (
            <div className="table-wrap">
              <table aria-label="Students to write">
                <thead>
                  <tr>
                    <th>Roll number</th>
                    <th>Name</th>
                    <th>Section</th>
                    <th>Status</th>
                    <th className="row-action">Card</th>
                  </tr>
                </thead>
                <tbody>
                  {students.map((student) => {
                    const status = statusOf(student.student_id)
                    const note = progress[student.student_id]?.note
                    const busy = status === 'writing' || status === 'verifying'
                    const otherBusy =
                      activeStudentID !== null &&
                      activeStudentID !== student.student_id &&
                      (statusOf(activeStudentID) === 'writing' || statusOf(activeStudentID) === 'verifying')

                    return (
                      <tr key={student.student_id} className={status === 'failed' ? 'populator-row-failed' : undefined}>
                        <td>{student.roll_no}</td>
                        <td>{student.display_name}</td>
                        <td>{student.section}</td>
                        <td>
                          <span className={`status-badge populator-${status}`}>{STATUS_LABEL[status]}</span>
                          {note && <span className="populator-note">{note}</span>}
                        </td>
                        <td className="row-action populator-actions">
                          <button
                            className="secondary"
                            type="button"
                            disabled={!cardsUsable || busy || otherBusy}
                            onClick={() => void writeCard(student)}
                          >
                            {status === 'pending' ? 'Write card for' : 'Rewrite card for'} {student.display_name}
                          </button>
                          <button
                            className="secondary"
                            type="button"
                            disabled={!cardsUsable || busy || otherBusy}
                            onClick={() => void verifyCard(student)}
                          >
                            Verify card for {student.display_name}
                          </button>
                          {busy && activeStudentID === student.student_id && (
                            <button className="secondary" type="button" onClick={cancelOperation}>
                              Cancel card operation for {student.display_name}
                            </button>
                          )}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </section>
      )}
    </PortalShell>
  )
}

async function loadCompleteRoster(api: EventApi, filters: Filters): Promise<StudentCollection> {
  const first = await api.students({
    branchID: filters.branchID,
    batch: filters.batch,
    section: filters.section,
    page: 1,
    pageSize: ROSTER_PAGE_SIZE,
  })
  const data = [...first.data]
  for (let page = 2; data.length < first.total; page += 1) {
    const next = await api.students({
      branchID: filters.branchID,
      batch: filters.batch,
      section: filters.section,
      page,
      pageSize: ROSTER_PAGE_SIZE,
    })
    if (next.data.length === 0) break
    data.push(...next.data)
  }
  return { ...first, data, page: 1, page_size: ROSTER_PAGE_SIZE }
}

function safeProgressNote(message: string) {
  if (/\b(token|hmac|secret|key)\b/i.test(message)) {
    return 'The card operation failed. Retry this student/card.'
  }
  return message
}
