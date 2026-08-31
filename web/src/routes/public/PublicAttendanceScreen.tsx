import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import {
  ApiFailure,
  createPublicApi,
  type PublicDisputeReceipt,
  type PublicEvent,
  type PublicODDay,
  type PublicODRecordCollection,
  type PublicStudent,
} from '../../api/client'
import { ThemeToggle } from '../../components/ThemeToggle'
import { Select } from '../../components/ui/Select'
import { useSmartPollInterval } from '../../features/marking/useSmartPollInterval'
import { campusToday, defaultDay, enumerateDays } from './days'

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
 * SECOND: it shows ONE DAY AT A TIME, and within that day only the slots that
 * were actually marked.
 *
 * It used to render all twelve slots for every day of a student's history, one
 * day below the next, with a "Report a missed slot" button on each of the empty
 * ones. A three-day festival was therefore thirty-six rows, thirty of them
 * saying "No record" — the answer the student is looking for buried in a wall of
 * the answer they are not. The gap is still visible, and still the point of the
 * page: a student who was marked for slots 1, 2 and 4 reads exactly those three
 * and sees for themselves that 3 is not among them.
 *
 * Reporting a missed slot moved to a single button that stays put, where the day
 * and the slot are chosen inside the form. That is the same information the
 * per-slot buttons carried, asked for once instead of being pre-answered twelve
 * times per day.
 *
 * There is deliberately no third "pending/syncing" state: Epic 0 has no async
 * path at all, so an amber chip would tell a student to wait for a sync that
 * does not exist.
 */

const SEARCH_DEBOUNCE_MS = 250
/**
 * How often the board refreshes itself while somebody is looking at it.
 *
 * Matched to the server's own cache lifetime. Polling faster cannot produce a
 * newer answer — it can only re-fetch bytes the server is serving from memory —
 * so the extra requests are pure cost, and they are multiplied by every student
 * with the page open behind one campus NAT.
 */
const BOARD_POLL_MS = 60_000
const MIN_SEARCH_RUNES = 3
const MAX_SEARCH_RUNES = 64
const SLOT_VALUES = Array.from({ length: 12 }, (_, index) => index + 1)
const DISPUTE_CONFIRMATION = 'Your dispute has been submitted. A teacher will review it.'

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

  // Counted in RUNES, matching the server. `String.length` is UTF-16 code
  // units, so a name in a non-BMP script counts double and the page would
  // refuse to send a query the API would have accepted.
  const trimmed = query.trim()
  const trimmedRunes = runeCount(trimmed)
  const tooShort = trimmedRunes > 0 && trimmedRunes < MIN_SEARCH_RUNES
  const tooLong = trimmedRunes > MAX_SEARCH_RUNES
  const debouncedRunes = runeCount(debouncedQuery)
  const searchable = debouncedRunes >= MIN_SEARCH_RUNES && debouncedRunes <= MAX_SEARCH_RUNES

  const search = useQuery({
    queryKey: ['public', 'students', 'search', debouncedQuery],
    queryFn: () => api.publicSearchStudents(debouncedQuery),
    enabled: searchable,
    // Nothing about a roster result should outlive the view that asked for it.
    gcTime: 0,
  })

  const records = useQuery({
    queryKey: ['public', 'students', selectedStudentID, 'od-records'],
    queryFn: () => api.publicStudentRecords(selectedStudentID),
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
    <main className="min-h-screen bg-canvas text-foreground flex flex-col items-center p-4 md:p-12 transition-colors duration-200">
      <header className="w-full max-w-3xl flex flex-col items-center text-center mb-10">
        <div className="w-full flex items-center justify-between gap-3 mb-4">
          {/*
            THE WAY BACK. This page is reachable directly — a shared link, a
            bookmark, a QR code — and it had no route to anywhere else. A
            coordinator who landed here had to edit the URL to reach sign-in,
            which is the same friction the front door was built to remove; it was
            simply still present one screen further in.

            A real <Link>, so it can be opened in a new tab and behaves like the
            destination it is. `/` decides what to show: the front door for a
            signed-out visitor, and their own screen for anyone already signed
            in — which is why this says "Home" rather than "Sign in".
          */}
          <Link className="public-home-link text-sm font-semibold text-foreground-muted hover:text-foreground" to="/">← Home</Link>
          <ThemeToggle />
        </div>
        <img
          className="h-16 md:h-20 w-auto object-contain transition-all duration-300 mb-6"
          src="/brand/amrita-logo.png"
          alt="Amrita Vishwa Vidyapeetham"
          width={600}
          height={155}
        />
        <p className="eyebrow text-xs uppercase tracking-widest text-foreground-muted font-bold mb-2">On-duty attendance</p>
        <h1 className="text-3xl md:text-4xl font-bold tracking-tight mb-4">Check your OD record</h1>
        <p className="public-lede text-foreground-muted text-lg">
          Today's on-duty list is below. Search to see your own record in full.
        </p>
        {/*
          Staff arrive here too — the dashboard is the one link students share,
          so it is the one link that gets forwarded. Naming sign-in explicitly
          saves them working out that "Home" is where it lives.
        */}
        <p className="public-hint text-sm text-foreground-muted mt-3">
          Staff marking attendance? <Link to="/">Sign in</Link>.
        </p>
      </header>

      {/* The BOARD, above the search and rendered without being asked for. The
          page's first answer is "here is who is out today", which is the
          question almost everybody opening it has. */}
      <DayBoard />

      <section className="public-search w-full min-w-0 max-w-3xl bg-surface border border-border p-6 md:p-8 rounded-2xl shadow-sm mb-8" aria-label="Find a student">
        <label className="public-field flex flex-col gap-2 mb-4" htmlFor="public-student-search">
          <span className="font-semibold text-sm">Roll number or name</span>
          <input
            className="w-full bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-xl p-4 text-lg transition-all outline-none"
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
        {tooShort && <p className="text-sm text-foreground-muted">Keep typing — at least 3 characters.</p>}
        {tooLong && <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-lg">That is longer than 64 characters. Try just the roll number.</p>}

        {search.isFetching && <p className="text-sm text-status-info-fg bg-status-info-bg p-3 rounded-lg flex items-center gap-2" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" /> Searching…
        </p>}

        {search.isError && (
          <div className="mt-4">
            <RetryableAlert
              message={searchFailureMessage(search.error)}
              onRetry={() => void search.refetch()}
            />
          </div>
        )}

        {searchable && search.isSuccess && results.length === 0 && (
          <p className="text-sm text-foreground-muted bg-canvas p-4 rounded-xl border border-border">No student matches that search.</p>
        )}

        {results.length > 0 && (
          <div className="mt-6 border-t border-border pt-4">
            <ul className="flex flex-col gap-2" role="listbox" aria-label="Matching students">
              {results.map((student) => (
                <li
                  key={student.student_id}
                  role="option"
                  aria-selected={student.student_id === selectedStudentID}
                  tabIndex={0}
                  className={`flex flex-col sm:flex-row sm:items-center justify-between p-4 rounded-xl cursor-pointer transition-all active:scale-97 border ${
                    student.student_id === selectedStudentID 
                      ? 'bg-foreground text-canvas border-foreground' 
                      : 'bg-canvas hover:bg-surface border-border'
                  }`}
                  onClick={() => selectStudent(student)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault()
                      selectStudent(student)
                    }
                  }}
                >
                  <strong className="font-mono text-lg">{student.roll_no}</strong>
                  <span className={`text-sm ${student.student_id === selectedStudentID ? 'text-canvas/80' : 'text-foreground-muted'}`}>
                    {student.display_name} · {student.branch}-{student.section}
                  </span>
                </li>
              ))}
            </ul>
            {results.length >= 10 && (
              <p className="text-xs text-foreground-muted mt-4 text-center">
                Showing the first 10 matches. Type your full roll number to narrow it down.
              </p>
            )}
          </div>
        )}
      </section>

      {selectedStudentID !== '' && (
        <section className="w-full min-w-0 max-w-3xl flex flex-col gap-6" aria-label="OD records">
          {records.isPending && <p className="text-sm text-status-info-fg bg-status-info-bg p-4 rounded-xl flex items-center justify-center gap-2" role="status">
            <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" /> Loading records…
          </p>}

          {records.isError && (
            <RetryableAlert
              message={recordsFailureMessage(records.error)}
              onRetry={() => void records.refetch()}
              onDismiss={clearStudent}
            />
          )}

          {/*
            Keyed on the student so every piece of per-student state — which day
            is on screen, and any half-opened dispute — is discarded when the
            visitor looks somebody else up. Resetting that by hand in each of the
            several places a student can change is how one of them eventually
            gets missed.
          */}
          {records.isSuccess && <StudentRecords key={selectedStudentID} collection={records.data} />}
        </section>
      )}
    </main>
  )
}


/**
 * The public attendance board: one campus day, everybody marked on it.
 *
 * IT LOADS WITHOUT BEING ASKED. That is the whole point — a visitor opens the
 * page and the day's list is there, with no search and no sign-in. Every other
 * surface in this app answers about ONE student; this one publishes a list, and
 * that is a decision taken deliberately rather than a side effect.
 *
 * THE FILTERS GO TO THE SERVER. Narrowing the rows in the browser would mean
 * the whole day's roster is downloaded whatever the visitor asked for — on this
 * surface that is the difference between publishing one slot and publishing
 * everything — and it would also break the moment a day exceeds the server's
 * row cap, because the browser would be filtering a truncated list without
 * knowing it.
 */
function DayBoard() {
  // PLAIN STATE, not the persisted kind the portal screens use. AC 13 holds
  // this page to writing nothing at all to localStorage or sessionStorage
  // beyond the theme, and a filter selection is not worth being the exception
  // that softens that rule — it costs a visitor one tap to set again.
  const [slot, setSlot] = useState('')
  const [categoryID, setCategoryID] = useState('')

  const pollInterval = useSmartPollInterval({
    activeInterval: BOARD_POLL_MS,
    idleInterval: 180_000, // 3 minutes when idle
    maxIdleTimeoutMs: 300_000, // Stop polling after 5 minutes of total inactivity
  })

  const board = useQuery({
    queryKey: ['public', 'board', slot, categoryID],
    queryFn: () => api.publicDayAttendance({
      slot: slot === '' ? undefined : Number(slot),
      categoryID: categoryID === '' ? undefined : categoryID,
    }),
    // The board is a live list during an event, and it is the same answer for
    // everybody, so the server caches it too — this interval decides how stale
    // a screen left open goes, not how hard the database is hit.
    refetchInterval: pollInterval,
    refetchIntervalInBackground: false,
    gcTime: 0,
  })

  const students = board.data?.students ?? []
  // Memoised because the effect below depends on it: a fresh [] every render
  // would re-run the effect on every render.
  const categories = useMemo(() => board.data?.categories ?? [], [board.data])

  // A category that has gone — the board rolls over to a new day while a tab is
  // left open — would otherwise leave the control showing a placeholder while
  // still narrowing the read to nothing.
  useEffect(() => {
    if (categoryID !== '' && board.isSuccess && !categories.some((category) => category.id === categoryID)) {
      setCategoryID('')
    }
  }, [board.isSuccess, categories, categoryID, setCategoryID])

  return (
    // w-full AND max-w-2xl, because the page is a centred flex column: without a
    // width the board becomes as wide as its widest child, and the roll-number
    // table is wider than a phone — so the PAGE scrolled sideways instead of the
    // table scrolling inside its own box, which is what .public-board-scroll is
    // there to do. min-w-0 is the other half of it: a flex item refuses to
    // shrink below its content otherwise.
    <section className="public-board w-full min-w-0 max-w-3xl" aria-label="Today's on-duty list">
      <div className="public-board-heading">
        <h2>On duty today</h2>
        {board.data && (
          <p className="public-hint" role="status">
            {formatDate(board.data.od_date)} · {students.length}
            {students.length === 1 ? ' student' : ' students'}
          </p>
        )}
      </div>

      <div className="public-board-filters">
        <div className="public-field">
          <span id="public-board-slot-label">Slot</span>
          <Select
            id="public-board-slot"
            aria-labelledby="public-board-slot-label"
            value={slot}
            onChange={setSlot}
            options={[{ value: '', label: 'All slots' }, ...SLOT_VALUES.map((value) => ({
              value: String(value),
              label: `Slot ${value}`,
            }))]}
          />
        </div>
        <div className="public-field">
          <span id="public-board-category-label">Category</span>
          <Select
            id="public-board-category"
            aria-labelledby="public-board-category-label"
            value={categoryID}
            onChange={setCategoryID}
            disabled={categories.length === 0}
            options={[{ value: '', label: 'All categories' }, ...categories.map((category) => ({
              value: category.id,
              label: category.name,
            }))]}
          />
        </div>
      </div>

      {board.isPending && <p className="public-hint" role="status">Loading today's list…</p>}

      {board.isError && (
        <RetryableAlert
          message={boardFailureMessage(board.error)}
          onRetry={() => void board.refetch()}
        />
      )}

      {/*
        The cap bit. Saying so matters more here than anywhere else on the page:
        a board that is silently short reads as "those students were not
        marked", which is the one statement this whole surface exists never to
        make by accident.
      */}
      {board.data?.truncated && (
        <p className="public-alert" role="alert">
          This day has more records than can be shown at once. Filter by slot or category to narrow it down.
        </p>
      )}

      {board.isSuccess && students.length === 0 && (
        <p className="public-hint">
          {slot === '' && categoryID === ''
            ? 'Nobody has been marked on duty yet today.'
            : 'No on-duty records match that filter today.'}
        </p>
      )}

      {students.length > 0 && (
        <div className="public-board-scroll">
          <table className="public-board-table" aria-label="On-duty students today">
            <thead>
              <tr>
                <th scope="col">Roll number</th>
                <th scope="col">Name</th>
                <th scope="col">Slots</th>
              </tr>
            </thead>
            <tbody>
              {students.map((student) => (
                <tr key={student.roll_no}>
                  <td className="public-board-roll">{student.roll_no}</td>
                  <td>{student.display_name}</td>
                  <td>
                    {/* The slot and what it was for, together. Which category
                        belongs to which slot is the part a student actually
                        checks, and a separate column could not say it once a
                        student has marks in two categories on one day. */}
                    <ul className="public-board-slots">
                      {student.marks.map((mark) => (
                        <li key={`${mark.slot}:${mark.category_id}`}>
                          <strong>{mark.slot}</strong>
                          <span>{mark.category_name}</span>
                        </li>
                      ))}
                    </ul>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

/**
 * DisputeDraft is what opening the report form carries: the day on screen, a
 * slot, and the EVENT that day belongs to. The event and category are '' only
 * when the selected day holds no record to derive them from — the never-marked
 * student, and any day the student was not marked on — and the form then falls
 * back to the public event list.
 */
type DisputeDraft = { day: string; slot: number; eventID: string; categoryID: string }

/**
 * One student's records, one day at a time.
 *
 * The day lives here rather than in the parent so that remounting on a student
 * change resets it, and so the day filter, the day on screen and the day a
 * report is filed against can never disagree — they are one piece of state.
 */
function StudentRecords({ collection }: { collection: PublicODRecordCollection }) {
  const recordDays = useMemo(
    () => collection.days.map((day) => day.od_date).sort(),
    [collection.days],
  )

  // Ascending, so the filter's ◀ is older and ▶ is newer — the direction the
  // arrows point. TODAY is unioned in even when it holds nothing: a student
  // checking during an event must be able to land on today and see it is empty,
  // which is a different statement from today not being offered at all.
  const dayOptions = useMemo(
    () => [...new Set([...recordDays, campusToday()])].sort(),
    [recordDays],
  )

  const [selectedDay, setSelectedDay] = useState(() => defaultDay(recordDays, campusToday()))
  const [disputeDraft, setDisputeDraft] = useState<DisputeDraft | null>(null)

  // A refetch can retire the selected day — a teacher removing the only record
  // on it, say. Falling back to the default is better than rendering a day that
  // is no longer in the list and no longer reachable by the arrows.
  useEffect(() => {
    if (!dayOptions.includes(selectedDay)) setSelectedDay(defaultDay(recordDays, campusToday()))
  }, [dayOptions, recordDays, selectedDay])

  const selectedIndex = dayOptions.indexOf(selectedDay)
  const day = collection.days.find((candidate) => candidate.od_date === selectedDay)

  const openDispute = () =>
    setDisputeDraft({
      day: selectedDay,
      slot: 1,
      // The event comes from the day ON SCREEN, never from the first record in
      // the whole history: a student with records under two events who reports
      // against the second one must not have the form silently re-scoped to the
      // first — including its date, which an effect would then rewrite.
      eventID: day?.records[0]?.event_id ?? '',
      categoryID: day?.records[0]?.category_id ?? '',
    })

  return (
    <>
      <div className="public-student">
        <h2>{collection.student.display_name}</h2>
        <p className="public-student-meta">
          {collection.student.roll_no} · {collection.student.branch}-{collection.student.section}
          {' · '}Batch {collection.student.batch}
        </p>
        {/*
          role="status" is 0.10's, and it stays. It was removed while adding the
          dispute form so a new findByRole('status') locator would not match two
          elements — silently deleting a live-region announcement to make a test
          unambiguous. The dispute success is scoped by its own panel label.
        */}
        <p className="public-asof" role="status">
          As of {formatTime(collection.generated_at)}
        </p>
      </div>

      {collection.days.length === 0 ? (
        <div className="public-empty">
          <p>No OD records found for this student yet.</p>
        </div>
      ) : (
        <>
          <div className="public-day-filter">
            <button
              type="button"
              className="public-day-step"
              aria-label="Previous day"
              disabled={selectedIndex <= 0}
              onClick={() => setSelectedDay(dayOptions[selectedIndex - 1] ?? selectedDay)}
            >
              ‹
            </button>
            {/*
              A <span> and aria-labelledby rather than a <label> wrapping the
              control. A combobox inside its own <label> takes its name from the
              label's whole subtree, INCLUDING the value the trigger is
              currently showing — so the name would read "Day Wednesday, 19
              August 2026" and change every time the day did. Pointing at the
              caption alone keeps the name stable, which is what a screen reader
              announces and what anything else finding this control relies on.
            */}
            <div className="public-field">
              <span id="public-day-filter-label">Day</span>
              <Select
                id="public-day-filter"
                aria-labelledby="public-day-filter-label"
                value={selectedDay}
                onChange={setSelectedDay}
                options={dayOptions.map((value) => ({ value, label: formatDate(value) }))}
              />
            </div>
            <button
              type="button"
              className="public-day-step"
              aria-label="Next day"
              disabled={selectedIndex < 0 || selectedIndex >= dayOptions.length - 1}
              onClick={() => setSelectedDay(dayOptions[selectedIndex + 1] ?? selectedDay)}
            >
              ›
            </button>
          </div>

          <DaySlots day={day} odDate={selectedDay} />
        </>
      )}

      {disputeDraft ? (
        <PublicDisputeForm
          // Keyed on the draft so a fresh report starts from a clean form rather
          // than from the last one's state — including `receipt`, which would
          // otherwise pin the panel to a past success with no way back.
          key={`${disputeDraft.eventID}:${disputeDraft.day}:${disputeDraft.slot}`}
          student={collection.student}
          days={collection.days}
          initialDay={disputeDraft.day}
          initialSlot={disputeDraft.slot}
          initialEventID={disputeDraft.eventID}
          initialCategoryID={disputeDraft.categoryID}
          onClose={() => setDisputeDraft(null)}
        />
      ) : (
        // ONE report affordance, parked in the corner, instead of one per empty
        // slot. Hidden while the form is open so the page never offers two ways
        // into the same thing.
        <button type="button" className="public-report-fab" onClick={openDispute}>
          Report a missed slot
        </button>
      )}
    </>
  )
}

/**
 * The marked slots of ONE day.
 *
 * The day is a <section>, never a list item: the slots are the list items, and
 * a nesting list item would make every "which slot row is this" locator match
 * the day wrapper too.
 *
 * ONLY THE SLOTS THAT WERE MARKED. A student reads the slots they were credited
 * for and sees for themselves which numbers are not there; twelve rows per day,
 * nine of them saying "No record", buried that answer rather than giving it.
 */
function DaySlots({ day, odDate }: { day: PublicODDay | undefined; odDate: string }) {
  // A slot holds a LIST, not a single record. Uniqueness in the database is
  // (student_id, event_id, od_date, slot) — per event per DAY — while a day here
  // is a calendar date, so two events running on the same day can each hold a
  // mark at the same slot for the same student. Keying a Map on the slot number
  // alone silently keeps the last one, and the mark that loses disappears
  // entirely: the page hiding a mark the student was given, which is the mirror
  // image of the outcome this screen exists to avoid.
  const bySlot = new Map<number, PublicODDay['records']>()
  for (const record of day?.records ?? []) {
    const existing = bySlot.get(record.slot)
    if (existing) existing.push(record)
    else bySlot.set(record.slot, [record])
  }
  const markedSlots = [...bySlot.keys()].sort((left, right) => left - right)

  return (
    <section className="public-day bg-surface border border-border rounded-2xl p-6 md:p-8 shadow-sm" aria-label={`OD records for ${formatDate(odDate)}`}>
      <h3 className="text-xl font-bold tracking-tight mb-6">{formatDate(odDate)}</h3>
      {markedSlots.length === 0 ? (
        // Deliberately about THIS DAY, and deliberately not the same sentence as
        // the student who has no records at all. "Nothing on this day" and "you
        // are not in our records" are different answers and must not be worded
        // as one.
        <p className="public-hint text-foreground-muted">No OD was marked on this day.</p>
      ) : (
        <ul className="public-slots grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {markedSlots.map((slot) => {
            const records = bySlot.get(slot) ?? []
            return (
              <li
                key={slot}
                className="public-slot is-present p-4 rounded-xl flex flex-col gap-2 border bg-status-success-bg border-status-success-fg/20 text-status-success-fg"
              >
                {/* The trailing colon is load-bearing, not decoration. A row's
                    textContent concatenates its children with no separator, so
                    without it "Slot 3" runs straight into the event name and any
                    "slot 3 as a whole word" match — including a screen reader
                    pausing at the label — reads them as one word. */}
                <span className="public-slot-label font-semibold text-sm opacity-80">Slot {slot}:</span>
                <span className="public-slot-detail flex flex-col gap-3">
                  {records.map((record) => (
                    <div className="flex flex-col gap-1" key={record.record_id}>
                      <strong className="font-bold leading-tight">{record.event_name}</strong>
                      <span className="text-xs uppercase tracking-wider font-bold opacity-75">{record.category_name}</span>
                      <span className="text-[11px] leading-relaxed opacity-80 mt-1">
                        Marked by {record.marked_by_name ?? 'a former staff member'} ({record.marked_by_role})
                        <br/>at {formatTime(record.marked_at)}
                      </span>
                    </div>
                  ))}
                </span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

function PublicDisputeForm({
  student,
  days,
  initialDay,
  initialSlot,
  initialEventID,
  initialCategoryID,
  onClose,
}: {
  student: PublicStudent
  days: PublicODDay[]
  initialDay: string
  initialSlot: number
  initialEventID: string
  initialCategoryID: string
  onClose(): void
}) {
  // ALWAYS fetched, not only when the student has no records. Deriving the
  // options from records alone meant a student could only ever pick a category
  // they had already been marked in — which excludes the commonest dispute of
  // all: "I was never marked for this category at all". The record-derived
  // events stay the preferred source for names and ordering; the public list
  // fills in the categories the records cannot know about.
  const publicEvents = useQuery({
    queryKey: ['public', 'events'],
    queryFn: () => api.publicEvents(),
    gcTime: 0,
  })
  const recordEvents = useMemo(() => eventsFromRecords(days), [days])
  const publicEventData = publicEvents.data?.data
  const eventOptions = useMemo(
    () => mergeEventOptions(recordEvents, publicEventData ?? []),
    [publicEventData, recordEvents],
  )
  // Which events carry a REAL end_date. An event rebuilt from a record has its
  // end_date set to the event's first day (eventsFromRecords), so its range is a
  // placeholder and must not be used to reject a day.
  const publicEventIDs = useMemo(
    () => new Set((publicEventData ?? []).map((event) => event.event_id)),
    [publicEventData],
  )
  const [eventID, setEventID] = useState(initialEventID)
  const [categoryID, setCategoryID] = useState(initialCategoryID)
  const [odDate, setODDate] = useState(initialDay)
  const [slot, setSlot] = useState(String(initialSlot))
  const [email, setEmail] = useState('')
  const [coordinator, setCoordinator] = useState('')
  const [note, setNote] = useState('')
  const [failure, setFailure] = useState<string | null>(null)
  const [receipt, setReceipt] = useState<PublicDisputeReceipt | null>(null)

  // The button that opens this form is parked in the corner of the viewport, so
  // the form itself can appear below the fold — a student would tap and see
  // nothing happen. Feature-detected because jsdom has no layout and would
  // otherwise throw where the enhancement simply does not apply.
  const panelRef = useRef<HTMLFormElement>(null)
  useEffect(() => {
    panelRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  }, [])

  const selectedEvent = eventOptions.find((event) => event.event_id === eventID) ?? eventOptions[0]
  const categoryOptions = useMemo(() => selectedEvent?.categories ?? [], [selectedEvent])
  // T23: the day is a bounded CHOICE scoped to the selected event, never an
  // unbounded <input type="date">. The submitter here is anonymous over the open
  // internet, and an unbounded control lets them file a dispute dated 1970.
  //
  // The range widened the SOURCE, not the control: the options are now every day
  // from event_date to end_date inclusive. `initialDay` — the day whose slot the
  // student actually clicked — is unioned in rather than assumed present,
  // because an event rebuilt from a record carries only its first day (see
  // eventsFromRecords), and the one option they need would otherwise be the one
  // missing.
  const dayOptions = useMemo(() => {
    if (!selectedEvent) return initialDay ? [initialDay] : []
    const days = enumerateDays(selectedEvent.event_date, selectedEvent.end_date)
    // The clicked day is added back ONLY for the event it was clicked on. An
    // event rebuilt from record history knows only its first day, so without
    // this the one option the student came here for can be missing — but
    // carrying it across an event change would offer a day of festival A while
    // event B is selected, and the database's range guard would refuse that
    // submission after the student had already filled the form.
    //
    // The push is ALSO skipped when the event's authoritative range is known and
    // excludes the day. `end_date` is only trustworthy for an event that came
    // from the public events list; one rebuilt from a record has end_date set to
    // its own first day, and for that case the union above is exactly the point.
    // Where the real range IS known, offering a day outside it would walk the
    // student through the whole form only to have the database's range guard
    // refuse the submission — the same failure this comment already avoids for a
    // different event, arrived at from a different direction.
    if (initialDay && selectedEvent.event_id === initialEventID && !days.includes(initialDay)) {
      const rangeIsAuthoritative = publicEventIDs.has(selectedEvent.event_id)
      const insideRange =
        initialDay >= selectedEvent.event_date && initialDay <= selectedEvent.end_date
      if (!rangeIsAuthoritative || insideRange) {
        days.push(initialDay)
      }
    }
    return days.sort()
  }, [initialDay, initialEventID, publicEventIDs, selectedEvent])

  useEffect(() => {
    if (!selectedEvent) return
    if (!eventOptions.some((event) => event.event_id === eventID)) {
      setEventID(selectedEvent.event_id)
    }
  }, [eventID, eventOptions, selectedEvent])

  useEffect(() => {
    if (!selectedEvent) return
    if (!categoryOptions.some((category) => category.id === categoryID)) {
      setCategoryID(categoryOptions[0]?.id ?? '')
    }
    if (!dayOptions.includes(odDate)) {
      setODDate(dayOptions[0] ?? '')
    }
  }, [categoryID, categoryOptions, dayOptions, odDate, selectedEvent])

  const submit = useMutation({
    mutationFn: () =>
      api.publicSubmitDispute({
        student_id: student.student_id,
        submitted_email: email.trim(),
        event_id: eventID,
        category_id: categoryID,
        od_date: odDate,
        slot: Number(slot),
        claimed_coordinator_name: coordinator.trim(),
        student_note: note.trim(),
      }),
    onSuccess(result) {
      setFailure(null)
      setReceipt(result)
    },
    onError(cause) {
      setFailure(disputeFailureMessage(cause))
    },
  })

  const submitDisabled = submit.isPending || !email.trim() || !eventID || !categoryID || !odDate || !slot || !coordinator.trim() || !note.trim()

  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    if (submitDisabled) return
    setFailure(null)
    submit.mutate()
  }

  if (receipt) {
    return (
      <section className="bg-surface border border-status-success-bg p-8 rounded-2xl flex flex-col items-center text-center shadow-sm" aria-label="Dispute receipt">
        <p className="text-status-success-fg font-medium mb-6" role="status">
          {DISPUTE_CONFIRMATION} Reference <strong className="font-mono bg-status-success-bg px-2 py-1 rounded">{receipt.reference}</strong>.
        </p>
        {/*
          Without a way out, this panel was permanent: `receipt` lives in the
          form's own state, so every later "Report a missed slot" click kept
          rendering this same success and a student who missed two slots could
          only ever report one.
        */}
        <button 
          type="button" 
          className="primary mt-2" 
          onClick={onClose}
        >
          Report another slot
        </button>
      </section>
    )
  }

  return (
    <form ref={panelRef} className="public-dispute-panel bg-surface border border-border rounded-2xl shadow-sm p-6 md:p-8 flex flex-col gap-6" aria-label="Report a missed slot dispute" role="form" onSubmit={onSubmit} noValidate>
      <div className="public-dispute-heading flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-border pb-6">
        <div>
          <h3 className="text-2xl font-bold tracking-tight mb-2">Report a missed slot</h3>
          <p className="text-foreground-muted text-sm">Resolving this report records a decision. Correcting the OD record is a separate staff action.</p>
        </div>
        <button 
          type="button" 
          className="secondary self-start md:self-auto py-2" 
          onClick={onClose} 
          disabled={submit.isPending}
        >
          Close
        </button>
      </div>

      <dl className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-5 gap-4 bg-canvas p-4 rounded-xl border border-border" aria-label="Student details">
        <div className="flex flex-col"><dt className="text-xs uppercase font-bold text-foreground-muted mb-1">Roll number</dt><dd className="font-mono text-sm">{student.roll_no}</dd></div>
        <div className="flex flex-col"><dt className="text-xs uppercase font-bold text-foreground-muted mb-1">Name</dt><dd className="text-sm font-medium">{student.display_name}</dd></div>
        <div className="flex flex-col"><dt className="text-xs uppercase font-bold text-foreground-muted mb-1">Branch</dt><dd className="text-sm font-medium">{student.branch}</dd></div>
        <div className="flex flex-col"><dt className="text-xs uppercase font-bold text-foreground-muted mb-1">Section</dt><dd className="text-sm font-medium">{student.section}</dd></div>
        <div className="flex flex-col"><dt className="text-xs uppercase font-bold text-foreground-muted mb-1">Batch</dt><dd className="text-sm font-medium">{student.batch}</dd></div>
      </dl>

      {publicEvents.isPending && eventOptions.length === 0 && <p className="text-sm text-status-info-fg bg-status-info-bg p-4 rounded-xl flex items-center gap-2" role="status"><span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" /> Loading events…</p>}
      {publicEvents.isError && eventOptions.length === 0 && (
        <RetryableAlert
          message="Events could not be loaded right now. Please try again."
          onRetry={() => void publicEvents.refetch()}
        />
      )}
      {/*
        Without this, a student facing an empty event list saw three empty
        selects and a permanently greyed-out button with no explanation — the
        form looked broken rather than inapplicable.
      */}
      {!publicEvents.isPending && !publicEvents.isError && eventOptions.length === 0 && (
        <p className="text-sm text-status-warning-fg bg-status-warning-bg p-4 rounded-xl border border-status-warning-fg/20" role="alert">
          There are no events available to report against yet. Please check with your teacher.
        </p>
      )}
      {eventOptions.length > 0 && categoryOptions.length === 0 && (
        <p className="text-sm text-status-warning-fg bg-status-warning-bg p-4 rounded-xl border border-status-warning-fg/20" role="alert">
          This event has no categories yet, so a slot cannot be reported against it. Please check with your teacher.
        </p>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <label className="flex flex-col gap-2" htmlFor="public-dispute-email">
          <span className="font-semibold text-sm">University email</span>
          <input
            className="w-full bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-xl p-3 transition-all outline-none"
            id="public-dispute-email"
            type="email"
            autoComplete="email"
            value={email}
            maxLength={320}
            onChange={(event) => setEmail(event.target.value)}
          />
        </label>

        <label className="flex flex-col gap-2" htmlFor="public-dispute-event">
          <span className="font-semibold text-sm">Event</span>
          <div className="relative">
            <Select
              id="public-dispute-event"
              value={eventID}
              onChange={(value) => { setEventID(value); setFailure(null) }}
              options={eventOptions.map((event) => ({ value: event.event_id, label: event.name }))}
            />
          </div>
        </label>

        <label className="flex flex-col gap-2" htmlFor="public-dispute-category">
          <span className="font-semibold text-sm">Category</span>
          <div className="relative">
            <Select
              id="public-dispute-category"
              value={categoryID}
              onChange={(value) => { setCategoryID(value); setFailure(null) }}
              disabled={categoryOptions.length === 0}
              options={categoryOptions.map((category) => ({ value: category.id, label: category.name }))}
            />
          </div>
        </label>

        <label className="flex flex-col gap-2" htmlFor="public-dispute-day">
          <span className="font-semibold text-sm">Day</span>
          <div className="relative">
            <Select
              id="public-dispute-day"
              value={odDate}
              onChange={(value) => { setODDate(value); setFailure(null) }}
              options={dayOptions.map((value) => ({ value, label: `${formatDate(value)} (${value})` }))}
            />
          </div>
        </label>

        <label className="flex flex-col gap-2" htmlFor="public-dispute-slot">
          <span className="font-semibold text-sm">Slot</span>
          <div className="relative">
            <Select
              id="public-dispute-slot"
              value={slot}
              onChange={(value) => { setSlot(value); setFailure(null) }}
              options={SLOT_VALUES.map((value) => ({ value: String(value), label: `Slot ${value}` }))}
            />
          </div>
        </label>

        <label className="flex flex-col gap-2" htmlFor="public-dispute-coordinator">
          <span className="font-semibold text-sm">Coordinator name</span>
          <input
            className="w-full bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-xl p-3 transition-all outline-none"
            id="public-dispute-coordinator"
            value={coordinator}
            maxLength={200}
            required
            onChange={(event) => setCoordinator(event.target.value)}
          />
        </label>
      </div>

      <label className="flex flex-col gap-2" htmlFor="public-dispute-note">
        <span className="font-semibold text-sm">Student note</span>
        <textarea
          className="w-full bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-xl p-3 transition-all outline-none resize-none"
          id="public-dispute-note"
          value={note}
          maxLength={1000}
          required
          onChange={(event) => setNote(event.target.value)}
          rows={3}
        />
      </label>

      <div className="pt-4 border-t border-border flex flex-col gap-3">
        <button 
          type="submit" 
          className="primary w-full" 
          disabled={submitDisabled}
        >
          {submit.isPending ? 'Submitting dispute…' : 'Submit dispute'}
        </button>
        {failure && <p className="text-sm text-status-error-fg bg-status-error-bg p-4 rounded-xl border border-status-error-fg/20" role="alert">{failure}</p>}
      </div>
    </form>
  )
}

/**
 * mergeEventOptions keeps the record-derived events first (they are the ones the
 * visitor actually attended, and the ones Journey 3 disputes), then unions in the
 * public list so an event's OTHER categories — the ones this student has no mark
 * in — are selectable too. A category the student was never marked in is exactly
 * what a "you missed me entirely" dispute needs to name.
 */
function mergeEventOptions(fromRecords: PublicEvent[], fromPublic: PublicEvent[]): PublicEvent[] {
  const merged = new Map<string, PublicEvent>()
  for (const event of fromRecords) {
    merged.set(event.event_id, { ...event, categories: [...event.categories] })
  }
  for (const event of fromPublic) {
    const existing = merged.get(event.event_id)
    if (!existing) {
      merged.set(event.event_id, { ...event, categories: [...event.categories] })
      continue
    }
    // The public list is AUTHORITATIVE for the range. A record only carries the
    // event's first day, so an event rebuilt from history looks one day long;
    // keeping that here would leave the dispute form offering day one of a
    // thirty-day festival and nothing else — to a student whose whole reason for
    // being on this form is a day further in.
    //
    // The record-derived entry still wins on name and ordering, which is what
    // the merge order above is for.
    existing.event_date = event.event_date
    existing.end_date = event.end_date
    for (const category of event.categories) {
      if (!existing.categories.some((item) => item.id === category.id)) {
        existing.categories.push(category)
      }
    }
  }
  return Array.from(merged.values())
}

function eventsFromRecords(days: PublicODDay[]): PublicEvent[] {
  const byEvent = new Map<string, PublicEvent>()
  for (const day of days) {
    for (const record of day.records) {
      const existing = byEvent.get(record.event_id)
      const category = { id: record.category_id, name: record.category_name }
      if (existing) {
        if (!existing.categories.some((item) => item.id === category.id)) existing.categories.push(category)
        continue
      }
      byEvent.set(record.event_id, {
        event_id: record.event_id,
        name: record.event_name,
        event_date: record.event_date,
        // A RECORD carries the event's first day, not its last: the public
        // records read has no reason to ship a range it does not group on. So an
        // event reconstructed from history is treated as the single day we
        // actually know, and the day the student clicked is added back
        // separately — see dayOptions. When the event also appears in the public
        // events list, that entry carries the real range and wins.
        end_date: record.event_date,
        categories: [category],
      })
    }
  }
  return Array.from(byEvent.values())
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
    <div
      className="bg-status-error-bg border border-status-error-fg/20 text-status-error-fg rounded-[10px] p-4 flex flex-col gap-3"
      role="alert"
    >
      <p className="text-sm font-medium">{message}</p>
      <div className="flex gap-3 flex-wrap">
        <button type="button" className="primary" onClick={onRetry}>Try again</button>
        {onDismiss && <button type="button" className="secondary" onClick={onDismiss}>Search for someone else</button>}
      </div>
    </div>
  )
}

function boardFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.status === 429) return 'Too many requests from this connection. Please wait a moment and try again.'
    if (error.status >= 500) return "Today's list could not be loaded right now. This is our problem, not yours — please try again."
  }
  // Worded so it can never be read as an attendance answer. An empty board and
  // a failed read are different statements and must not look alike.
  return "Today's list could not be loaded. Check your connection and try again."
}

function searchFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.status === 429) return 'Too many searches from this connection. Please slow down and try again in a moment.'
    if (error.status === 400) return 'That search could not be read. Try your full roll number.'
    if (error.status >= 500) return 'The server could not answer that search right now. This is our problem, not yours — please try again.'
  }
  return 'The search could not be completed. Check your connection and try again.'
}

function recordsFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    // Deliberately worded so it can never be mistaken for an attendance answer:
    // a mistyped roll number is a lookup problem, not an absence.
    if (error.status === 404) return 'No such student. Check the roll number and search again.'
    if (error.status === 429) return 'Too many requests from this connection. Please slow down and try again in a moment.'
    // A truncated or mangled shared link is the common way to get here, and it
    // is the visitor's link that is broken, not their network. AC 12 asks each
    // failure to NAME its reason; "check your connection" names the wrong one.
    if (error.status === 400) return 'That link is not a valid student reference. Search for the student instead.'
    if (error.status >= 500) return 'The server could not load these records right now. This is our problem, not yours — please try again.'
  }
  return 'These records could not be loaded. Check your connection and try again.'
}

function disputeFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.body.code === 'DISPUTE_IDENTITY_UNVERIFIED') {
      return 'We could not verify that email against the roster. Check that you used your university address.'
    }
    if (error.body.code === 'DISPUTE_ALREADY_OPEN') {
      return error.body.message || 'You already reported this slot.'
    }
    if (error.body.code === 'RATE_LIMITED' || error.status === 429) {
      return 'Too many dispute submissions from this connection. Please slow down and try again in a moment.'
    }
    if (error.body.code === 'CATEGORY_NOT_FOUND') {
      return 'That category no longer belongs to the selected event. Choose the event and category again.'
    }
    if (error.body.code === 'EVENT_NOT_FOUND') {
      return 'That event is no longer available for this report.'
    }
    if (error.body.code === 'VALIDATION_FAILED') {
      // The service puts the generic summary in `message` and the field-level
      // reason in `details[0].message`, so rendering `message` alone told the
      // student only "Validation failed." with no indication of which field.
      const detail = validationDetail(error.body)
      if (detail) return detail
      return error.body.message || 'Check the dispute details and try again.'
    }
    if (error.status >= 500) return 'The server could not submit this dispute right now. This is our problem, not yours — please try again.'
  }
  return 'The dispute could not be submitted. Check your connection and try again.'
}

/** Reads the first typed field failure out of a ValidationError envelope. */
function validationDetail(body: unknown): string | null {
  if (typeof body !== 'object' || body === null || !('details' in body)) return null
  const details = (body as { details?: unknown }).details
  if (!Array.isArray(details) || details.length === 0) return null
  const first = details[0]
  if (typeof first !== 'object' || first === null || !('message' in first)) return null
  const message = (first as { message?: unknown }).message
  return typeof message === 'string' && message !== '' ? message : null
}

/** Counts Unicode code points, the unit the Go service bounds on. */
function runeCount(value: string): number {
  return [...value].length
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
