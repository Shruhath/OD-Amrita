import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import type { ResolvedCardStudent } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { CardNfcError, createWebNfcAdapter, type WebNfcStatus } from '../../../features/cardPopulator/webNfc'
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
  const { api, identity, signOut } = useAuth()

  const nfc = useMemo(() => createWebNfcAdapter(window), [])
  const [nfcStatus, setNfcStatus] = useState<WebNfcStatus>(() => nfc.status())
  useEffect(() => setNfcStatus(nfc.status()), [nfc])

  const [draft, setDraft] = useState<Filters>({ branchID: '', batch: '', section: '' })
  const [applied, setApplied] = useState<Filters | null>(null)
  const [progress, setProgress] = useState<Record<string, CardProgress>>(readStoredProgress)
  const [activeStudentID, setActiveStudentID] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)

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
    queryFn: () => api.students({
      branchID: applied?.branchID || undefined,
      batch: applied?.batch || undefined,
      section: applied?.section || undefined,
    }),
    enabled: applied !== null,
  })

  // Memoised so the identity is stable between renders: a fresh array on every
  // render would re-run the counts memo on each keystroke elsewhere on the page.
  const students = useMemo(() => roster.data?.data ?? [], [roster.data])

  const setStudentProgress = useCallback((studentID: string, next: Omit<CardProgress, 'updatedAt'>) => {
    setProgress((current) => ({ ...current, [studentID]: { ...next, updatedAt: Date.now() } }))
  }, [])

  const statusOf = useCallback(
    (studentID: string): CardStatus => progress[studentID]?.status ?? 'pending',
    [progress],
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
    return { written, verified, failed, total: students.length }
  }, [students, statusOf])

  const loadRoster = (event: FormEvent) => {
    event.preventDefault()
    setFormError(null)
    if (!draft.branchID) {
      setFormError('Choose a branch before loading the roster.')
      return
    }
    setApplied({ ...draft })
  }

  const beginOperation = (studentID: string) => {
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setActiveStudentID(studentID)
    return controller
  }

  const failWith = (student: ResolvedCardStudent, cause: unknown, fallback: string) => {
    const message = cause instanceof CardNfcError ? cause.message : messageOf(cause, fallback)
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
    } catch (cause) {
      failWith(student, cause, 'The card could not be written')
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
    } catch (cause) {
      failWith(student, cause, 'The card could not be verified')
    }
  }

  const cardsUsable = nfcStatus === 'available'

  return (
    <div className="portal-shell">
      <header className="topbar">
        <div>
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">OD-Amrita</span>
        </div>
        <nav className="portal-nav" aria-label="Administration sections">
          <Link to="/portal/events">Events</Link>
          <Link to="/portal/teachers">Teachers</Link>
          <Link to="/portal/students">Students</Link>
          <Link to="/portal/card-populator" aria-current="page">Card populator</Link>
        </nav>
        <div className="account-actions">
          <span>{identity?.name ?? identity?.email}</span>
          <button className="text-button" type="button" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>

      <main className="workspace">
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
              <select
                id="populator-branch"
                value={draft.branchID}
                onChange={(event) => setDraft((current) => ({ ...current, branchID: event.target.value }))}
              >
                <option value="">Select a branch</option>
                {(branches.data?.data ?? []).map((branch) => (
                  <option key={branch.id} value={branch.id}>{branch.code} — {branch.name}</option>
                ))}
              </select>
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

        {applied !== null && !roster.isError && (
          <section className="detail-section populator-roster">
            <h2>Section progress</h2>
            {/* aria-live so the operator hears progress without watching the counts. */}
            <p className="populator-counts" role="status">
              {counts.written} written · {counts.verified} verified · {counts.failed} failed · {counts.total} in section
            </p>

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
      </main>
    </div>
  )
}
