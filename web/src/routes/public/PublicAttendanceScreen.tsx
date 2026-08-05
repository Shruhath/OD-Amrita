import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ApiFailure, createPublicApi, type PublicODDay, type PublicStudent } from '../../api/client'
import { ThemeToggle } from '../../components/ThemeToggle'

/**
 * The public attendance dashboard — the only screen in this app that a visitor
 * with no account ever sees (Story 0.10, FR38-FR40).
 *
 * Two rules shape everything below.
 *
 * FIRST: this page must never imply an answer it does not have. A failed read, a
 * successful empty read, and "this student does not exist" are three DIFFERENT
 * statements, and the most damaging thing this screen can do is render any of
 * them as "you were not marked". They therefore get three distinct treatments,
 * and the empty state is shown only after a successful response.
 *
 * SECOND: it renders all twelve slots, not only the ones with a record. PRD
 * Journey 3 is a student opening this page at 10pm, seeing slots 1, 2 and 4, and
 * noticing that slot 3 is missing — the gap IS the information. There is
 * deliberately no third "pending/syncing" state: Epic 0 has no async path at
 * all, so an amber chip would tell a student to wait for a sync that does not
 * exist.
 */

const SEARCH_DEBOUNCE_MS = 250
const MIN_SEARCH_RUNES = 3
const MAX_SEARCH_RUNES = 64
const SLOT_VALUES = Array.from({ length: 12 }, (_, index) => index + 1)

// One client for the whole screen, created at module scope: it holds no session
// and no per-render state, so rebuilding it on every render would only churn the
// underlying fetch wrapper.
const api = createPublicApi()

export default function PublicAttendanceScreen() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')

  // The selected student lives in the URL rather than in component state so a
  // reload, a back button and a link shared over WhatsApp all reopen the same
  // record — which is exactly what PRD Journey 3's student does with it.
  // NFR18: a student uuid is the ONLY thing this page ever puts in the query
  // string. No email, ever.
  const selectedStudentID = searchParams.get('student') ?? ''

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [query])

  const trimmed = query.trim()
  const tooShort = trimmed.length > 0 && trimmed.length < MIN_SEARCH_RUNES
  const tooLong = trimmed.length > MAX_SEARCH_RUNES
  const searchable = debouncedQuery.length >= MIN_SEARCH_RUNES && debouncedQuery.length <= MAX_SEARCH_RUNES

  const search = useQuery({
    queryKey: ['public', 'students', 'search', debouncedQuery],
    queryFn: () => api.searchStudents(debouncedQuery),
    enabled: searchable,
    // Nothing about a roster result should outlive the view that asked for it.
    gcTime: 0,
  })

  const records = useQuery({
    queryKey: ['public', 'students', selectedStudentID, 'od-records'],
    queryFn: () => api.studentRecords(selectedStudentID),
    enabled: selectedStudentID !== '',
    gcTime: 0,
  })

  // Gated on `searchable` rather than on the query's own data: selecting a
  // student clears the box, and a disabled TanStack query keeps its last result
  // until it is garbage-collected — so without this the picker would stay open
  // over the records the visitor just asked for.
  const results = useMemo(() => (searchable ? search.data?.data ?? [] : []), [search.data, searchable])

  const selectStudent = (student: PublicStudent) => {
    setQuery('')
    setDebouncedQuery('')
    // Replaces the whole query string rather than merging, so `student` is the
    // only key that can ever be present.
    setSearchParams({ student: student.student_id })
  }

  const clearStudent = () => setSearchParams({})

  return (
    <main className="public-page">
      <header className="public-header">
        <div className="public-toolbar">
          <ThemeToggle />
        </div>
        <img
          className="public-brand"
          src="/brand/amrita-logo.png"
          alt="Amrita Vishwa Vidyapeetham"
          width={600}
          height={155}
        />
        <p className="eyebrow">On-duty attendance</p>
        <h1>Check your OD record</h1>
        <p className="public-lede">
          Search your roll number or name. No sign-in needed.
        </p>
      </header>

      <section className="public-search" aria-label="Find a student">
        <label className="public-field" htmlFor="public-student-search">
          <span>Roll number or name</span>
          <input
            id="public-student-search"
            type="search"
            autoComplete="off"
            spellCheck={false}
            value={query}
            maxLength={MAX_SEARCH_RUNES * 2}
            placeholder="CB.SC.U4CSE24001"
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>

        {/* A hint, not an error: someone three keystrokes in has not done
            anything wrong, they simply have not finished. */}
        {tooShort && <p className="public-hint">Keep typing — at least 3 characters.</p>}
        {tooLong && <p className="public-hint">That is longer than 64 characters. Try just the roll number.</p>}

        {search.isFetching && <p className="public-hint" role="status">Searching…</p>}

        {search.isError && (
          <RetryableAlert
            message={searchFailureMessage(search.error)}
            onRetry={() => void search.refetch()}
          />
        )}

        {searchable && search.isSuccess && results.length === 0 && (
          <p className="public-hint">No student matches that search.</p>
        )}

        {results.length > 0 && (
          <>
            <ul className="public-results" role="listbox" aria-label="Matching students">
              {results.map((student) => (
                <li
                  key={student.student_id}
                  role="option"
                  aria-selected={student.student_id === selectedStudentID}
                  tabIndex={0}
                  onClick={() => selectStudent(student)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      selectStudent(student)
                    }
                  }}
                >
                  <strong>{student.roll_no}</strong>
                  <span>{student.display_name} · {student.branch}-{student.section}</span>
                </li>
              ))}
            </ul>
            {/* The server caps this at 10 and accepts no limit from the client,
                so a common name can genuinely be cut off. Say so rather than
                letting someone conclude they are not on the roster. */}
            {results.length >= 10 && (
              <p className="public-hint">
                Showing the first 10 matches. Type your full roll number to narrow it down.
              </p>
            )}
          </>
        )}
      </section>

      {selectedStudentID !== '' && (
        <section className="public-records" aria-label="OD records">
          {records.isPending && <p className="public-hint" role="status">Loading records…</p>}

          {records.isError && (
            <RetryableAlert
              message={recordsFailureMessage(records.error)}
              onRetry={() => void records.refetch()}
              onDismiss={clearStudent}
            />
          )}

          {records.isSuccess && (
            <>
              <div className="public-student">
                <h2>{records.data.student.display_name}</h2>
                <p className="public-student-meta">
                  {records.data.student.roll_no} · {records.data.student.branch}-{records.data.student.section}
                  {' · '}Batch {records.data.student.batch}
                </p>
                <p className="public-asof" role="status">
                  As of {formatTime(records.data.generated_at)}
                </p>
              </div>

              {records.data.days.length === 0 ? (
                <p className="public-empty">
                  No OD records found for this student yet.
                </p>
              ) : (
                records.data.days.map((day) => <DaySlots key={day.event_date} day={day} />)
              )}
            </>
          )}
        </section>
      )}
    </main>
  )
}

/**
 * One event day as all twelve slots.
 *
 * The day is a <section>, never a list item: the slots are the list items, and
 * a nesting list item would make every "which slot row is this" locator match
 * the day wrapper too.
 */
function DaySlots({ day }: { day: PublicODDay }) {
  const bySlot = new Map(day.records.map((record) => [record.slot, record]))

  return (
    <section className="public-day" aria-label={`OD records for ${formatDate(day.event_date)}`}>
      <h3>{formatDate(day.event_date)}</h3>
      <ul className="public-slots">
        {SLOT_VALUES.map((slot) => {
          const record = bySlot.get(slot)
          return (
            <li key={slot} className={record ? 'public-slot is-present' : 'public-slot is-absent'}>
              {/* The trailing colon is load-bearing, not decoration. A row's
                  textContent concatenates its children with no separator, so
                  without it "Slot 3" runs straight into "No record" and any
                  "slot 3 as a whole word" match — including a screen reader
                  pausing at the label — sees "Slot 3No". */}
              <span className="public-slot-label">Slot {slot}:</span>
              {record ? (
                <span className="public-slot-detail">
                  <strong>{record.event_name}</strong>
                  <span>{record.category_name}</span>
                  <span>
                    Marked by {record.marked_by_name ?? 'a former staff member'} ({record.marked_by_role})
                    {' at '}{formatTime(record.marked_at)}
                  </span>
                </span>
              ) : (
                <span className="public-slot-detail public-slot-empty">No record</span>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/**
 * Every failure on this page names its reason and offers a way forward. A blank
 * screen or a silent empty list would read as "you were not marked", which is
 * the one answer this page must never give by accident.
 */
function RetryableAlert({
  message,
  onRetry,
  onDismiss,
}: {
  message: string
  onRetry: () => void
  onDismiss?: () => void
}) {
  return (
    <div className="public-alert" role="alert">
      <p>{message}</p>
      <div className="public-alert-actions">
        <button type="button" onClick={onRetry}>Try again</button>
        {onDismiss && <button type="button" onClick={onDismiss}>Search for someone else</button>}
      </div>
    </div>
  )
}

function searchFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.status === 429) return 'Too many searches from this connection. Please slow down and try again in a moment.'
    if (error.status === 400) return 'That search could not be read. Try your full roll number.'
  }
  return 'The search could not be completed. Check your connection and try again.'
}

function recordsFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    // Deliberately worded so it can never be mistaken for an attendance answer:
    // a mistyped roll number is a lookup problem, not an absence.
    if (error.status === 404) return 'No such student. Check the roll number and search again.'
    if (error.status === 429) return 'Too many requests from this connection. Please slow down and try again in a moment.'
  }
  return 'These records could not be loaded. Check your connection and try again.'
}

/** Renders a YYYY-MM-DD event date in the campus timezone-free way it is stored. */
function formatDate(value: string): string {
  const parsed = new Date(`${value}T00:00:00`)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' })
}

function formatTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}
