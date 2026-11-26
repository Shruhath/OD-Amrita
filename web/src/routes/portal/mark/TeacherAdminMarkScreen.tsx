import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { MarkAttendanceResult, ResolvedCardStudent } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { Select } from '../../../components/ui/Select'
import { asString, usePersistentState } from '../../../features/persistence/sessionState'
import { useAuth } from '../../../features/auth/context'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { Check } from '@phosphor-icons/react/dist/icons/Check'
import { ClipboardText } from '@phosphor-icons/react/dist/icons/ClipboardText'

const SEARCH_DEBOUNCE_MS = 250
const MIN_SEARCH_RUNES = 3
const MAX_SEARCH_RUNES = 64
/**
 * The slot picker follows the SERVER's ceiling.
 *
 * Hardcoding 12 here while the evening-slots switch is on would leave a teacher
 * unable to name the slot a coordinator had just marked in; hardcoding 20 while
 * it is off would offer slots the API refuses. Both are the same defect — a
 * screen holding its own copy of a number the server decides.
 */
function slotValues(maxSlot: number): number[] {
  return Array.from({ length: maxSlot }, (_, index) => index + 1)
}

export default function TeacherAdminMarkScreen() {
  const { api, identity } = useAuth()

  // The ceiling the SERVER is enforcing. Only an administrator may read the
  // setting, so a teacher falls back to the teaching day — which is the value
  // the switch defaults to and the one production runs on.
  const slotCeiling = useQuery({
    queryKey: ['admin', 'settings', 'extended-slots'],
    queryFn: () => api.extendedSlotsSetting(),
    enabled: identity?.role === 'admin',
    staleTime: 30_000,
  })
  const maxSlot = slotCeiling.data?.max_slot ?? 12
  const queryClient = useQueryClient()
  const [eventID, setEventID] = usePersistentState('teacher-mark.event.v1', '', asString)
  const [categoryID, setCategoryID] = usePersistentState('teacher-mark.category.v1', '', asString)
  const [slot, setSlot] = usePersistentState('teacher-mark.slot.v1', '1', asString)
  const [query, setQuery] = useState('')
  const [reasonNote, setReasonNote] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [selectedStudents, setSelectedStudents] = useState<ResolvedCardStudent[]>([])
  const [odDate, setODDate] = useState('')
  const [failure, setFailure] = useState<string | null>(null)
  const [success, setSuccess] = useState<MarkAttendanceResult[] | null>(null)
  const isAdministrator = identity?.role === 'admin'
  const isTeacher = identity?.role === 'teacher'

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [query])

  const events = useQuery({
    queryKey: ['portal', 'mark', 'events'],
    queryFn: () => api.portalMarkEvents(),
  })
  const eventOptions = useMemo(() => events.data?.data ?? [], [events.data])
  const selectedEvent = useMemo(
    () => eventOptions.find((event) => event.id === eventID) ?? null,
    [eventID, eventOptions],
  )

  useEffect(() => {
    if (eventOptions.length === 0) return
    if (!eventOptions.some((event) => event.id === eventID)) {
      setEventID(eventOptions[0].id)
    }
  }, [eventID, eventOptions, setEventID])

  const categories = useQuery({
    queryKey: ['portal', 'mark', 'events', eventID, 'categories'],
    queryFn: () => api.portalMarkCategories(eventID),
    enabled: eventID !== '',
  })
  const categoryOptions = useMemo(() => categories.data?.data ?? [], [categories.data])

  useEffect(() => {
    if (categoryOptions.length === 0) return
    if (!categoryOptions.some((category) => category.id === categoryID)) {
      setCategoryID(categoryOptions[0].id)
    }
  }, [categoryID, categoryOptions, setCategoryID])

  useEffect(() => {
    setSelectedStudents([])
    setODDate(selectedEvent?.event_date ?? '')
  }, [eventID, categoryID, selectedEvent?.event_date])

  const studentQuery = useQuery({
    queryKey: ['portal', 'mark', 'students', debouncedQuery],
    queryFn: () => api.searchStudents(debouncedQuery),
    enabled: debouncedQuery.length >= MIN_SEARCH_RUNES && debouncedQuery.length <= MAX_SEARCH_RUNES,
    gcTime: 0,
  })
  const studentOptions = useMemo(() => studentQuery.data?.data ?? [], [studentQuery.data])

  const mark = useMutation({
    mutationFn: async (): Promise<MarkAttendanceResult[]> => {
      if (selectedStudents.length === 0) throw new Error('Choose at least one student before marking.')
      if (isTeacher) {
        const [student] = selectedStudents
        return [await api.mark({
          event_id: eventID,
          category_id: categoryID,
          capture_method: 'search',
          student_id: student.student_id,
          slot: Number(slot),
          reason_note: reasonNote.trim(),
        })]
      }
      if (!isAdministrator) throw new Error('Administrator or teacher access is required for manual marks.')
      return (await api.batchManualMark({
        event_id: eventID,
        category_id: categoryID,
        student_ids: selectedStudents.map((student) => student.student_id),
        slot: Number(slot),
        od_date: odDate,
        reason_note: reasonNote.trim(),
      })).records
    },
    onSuccess(result) {
      setFailure(null)
      setSuccess(result)
      setQuery('')
      setDebouncedQuery('')
      setSelectedStudents([])
      void queryClient.invalidateQueries({ queryKey: ['admin', 'attendance', eventID] })
    },
    onError(cause) {
      setSuccess(null)
      if (cause instanceof ApiFailure && cause.body.code === 'SCAN_DUPLICATE' && 'duplicate_students' in cause.body) {
        const duplicates = (cause.body as { duplicate_students?: ResolvedCardStudent[] }).duplicate_students ?? []
        setFailure(`Already marked: ${duplicates.map((student) => student.roll_no).join(', ')}. Remove them and try again.`)
      } else {
        setFailure(cause instanceof ApiFailure ? cause.body.message : cause instanceof Error ? cause.message : 'The students could not be marked.')
      }
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        void events.refetch()
      }
    },
  })

  const eventReady =
    eventID !== '' &&
    categoryID !== '' &&
    selectedEvent !== null
  const markDisabled = (!isAdministrator && !isTeacher) || !eventReady || (isAdministrator && !odDate) || selectedStudents.length === 0 || reasonNote.trim() === '' || mark.isPending

  const changeQuery = (value: string) => {
    setQuery(value)
    setFailure(null)
    setSuccess(null)
  }

  const changeEvent = (value: string) => {
    setEventID(value)
    setFailure(null)
    setSuccess(null)
    setSelectedStudents([])
  }

  const changeCategory = (value: string) => {
    setCategoryID(value)
    setFailure(null)
    setSuccess(null)
    setSelectedStudents([])
  }

  const changeSlot = (value: string) => {
    setSlot(value)
    setFailure(null)
    setSuccess(null)
  }

  const submit = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    if (markDisabled) return
    setFailure(null)
    setSuccess(null)
    mark.mutate()
  }

  return (
    <PortalShell>
      {/* Page Header */}
      <div className="mb-8">
        <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Attendance</p>
        <h1 className="text-3xl font-bold tracking-tight mb-2">Mark attendance</h1>
        <p className="text-foreground-muted">Search-mark present students for an event and category, with an explicit slot.</p>
      </div>

      <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm">
        <h2 className="text-lg font-bold tracking-tight mb-1">Search mark</h2>
        <p className="text-sm text-foreground-muted mb-6">
          {isTeacher
            ? 'Choose the event, category and slot, then select one student to mark.'
            : 'Choose the event, category, slot and date, then build the student list to mark together.'}
        </p>

        {/* Error/Empty states */}
        {events.isPending && (
          <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-5" role="status">
            <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
            Loading events…
          </div>
        )}
        {events.isError && (
          <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-5" role="alert">
            <span className="font-medium text-sm">{events.error instanceof ApiFailure ? events.error.body.message : 'Events could not be loaded.'}</span>
            <button type="button" onClick={() => void events.refetch()} className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all">Try again</button>
          </div>
        )}
        {events.isSuccess && eventOptions.length === 0 && (
          <div className="bg-surface border border-border rounded-[10px] p-10 flex flex-col items-center text-center gap-3 mb-5" role="status">
        <ClipboardText size={32} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
            <h3 className="text-lg font-bold tracking-tight">No events to mark yet</h3>
            <p className="text-sm text-foreground-muted max-w-sm">
              You can mark attendance for an active event that covers one of your branches. There
              are none right now — an administrator adds your branch to an event to make it appear here.
            </p>
          </div>
        )}
        {categories.isError && (
          <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-5" role="alert">
            <span className="font-medium text-sm">{categories.error instanceof ApiFailure ? categories.error.body.message : 'Categories could not be loaded.'}</span>
            <button type="button" onClick={() => void categories.refetch()} className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all">Try again</button>
          </div>
        )}
        {categories.isSuccess && categoryOptions.length === 0 && (
          <p className="text-sm text-foreground-muted italic mb-5">No categories available for this event.</p>
        )}

        {/* Mark form */}
        {!(events.isSuccess && eventOptions.length === 0) && (
          <form className="flex flex-col gap-5" onSubmit={submit} noValidate>
            {/* Event / Category / Slot selectors */}
            <div className={`grid grid-cols-1 ${isAdministrator ? 'sm:grid-cols-4' : 'sm:grid-cols-3'} gap-4`}>
              <div className="flex flex-col gap-1">
                <label htmlFor="teacher-mark-event" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Event</label>
                <Select
                  id="teacher-mark-event"
                  value={eventID}
                  onChange={changeEvent}
                  options={eventOptions.map((event) => ({ value: event.id, label: event.name }))}
                />
              </div>

              {isAdministrator && (
                <div className="flex flex-col gap-1">
                  <label htmlFor="teacher-mark-date" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">OD date</label>
                  <input
                    id="teacher-mark-date"
                    type="date"
                    value={odDate}
                    min={selectedEvent?.event_date}
                    max={selectedEvent?.end_date}
                    onChange={(event) => { setODDate(event.target.value); setFailure(null); setSuccess(null) }}
                    className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
                  />
                </div>
              )}

              <div className="flex flex-col gap-1">
                <label htmlFor="teacher-mark-category" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Category</label>
                <Select
                  id="teacher-mark-category"
                  value={categoryID}
                  onChange={changeCategory}
                  disabled={categoryOptions.length === 0}
                  options={categoryOptions.map((category) => ({ value: category.id, label: category.name }))}
                />
              </div>

              <div className="flex flex-col gap-1">
                <label htmlFor="teacher-mark-slot" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Slot</label>
                {/* The ceiling is the EVENING-SLOTS setting, not a constant: an
                    event that runs past slot 12 opens 13-20, and hard-coding the
                    list here would silently refuse the marks it exists to take. */}
                <Select
                  id="teacher-mark-slot"
                  value={slot}
                  onChange={changeSlot}
                  options={slotValues(maxSlot).map((value) => ({ value: String(value), label: String(value) }))}
                />
              </div>

            </div>

            {/* Student search */}
            <div className="flex flex-col gap-1.5">
              <label htmlFor="teacher-mark-student" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Student</label>
              <input
                id="teacher-mark-student"
                type="search"
                role="searchbox"
                value={query}
                onChange={(event) => changeQuery(event.target.value)}
                autoComplete="off"
                aria-describedby="teacher-mark-student-help"
                placeholder="Search by roll number or name…"
                className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
              />
              <p id="teacher-mark-student-help" className="text-xs text-foreground-muted">Search by roll number or name.</p>

              {/* Student search states */}
              {debouncedQuery.length > 0 && debouncedQuery.length < MIN_SEARCH_RUNES && (
                <p className="text-xs text-foreground-muted">Keep typing to search students.</p>
              )}
              {debouncedQuery.length > MAX_SEARCH_RUNES && (
                <p className="text-xs text-status-error-fg">Search text is too long.</p>
              )}
              {studentQuery.isPending && studentQuery.fetchStatus !== 'idle' && (
                <p className="text-xs text-foreground-muted flex items-center gap-1.5" role="status">
                  <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
                  Searching students…
                </p>
              )}
              {studentQuery.isError && (
                <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">
                  <span className="font-medium text-xs">{studentQuery.error instanceof ApiFailure ? studentQuery.error.body.message : 'Student search failed.'}</span>
                  <button type="button" onClick={() => void studentQuery.refetch()} className="text-xs font-bold px-2 py-1 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all">Try again</button>
                </div>
              )}
              {studentQuery.isSuccess && studentOptions.length > 0 && (
                <ul className="flex flex-col gap-1 mt-1" role="listbox" aria-label="Student search results">
                  {studentOptions.map((student) => (
                    <li key={student.student_id}>
                      {(() => {
                        const selected = selectedStudents.some((item) => item.student_id === student.student_id)
                        return (
                      <button
                        type="button"
                        role="option"
                        aria-selected={selected}
                        aria-label={`${selected ? 'Deselect' : 'Select'} ${student.roll_no}`}
                        className={`w-full text-left p-3 rounded-[10px] transition-all active:scale-[0.99] border ${
                          selected
                            ? 'bg-foreground text-canvas border-foreground'
                            : 'bg-canvas hover:bg-surface border-border'
                        }`}
                        onClick={() => {
                          setSelectedStudents((current) => {
                            if (current.some((item) => item.student_id === student.student_id)) {
                              return current.filter((item) => item.student_id !== student.student_id)
                            }
                            return isTeacher ? [student] : [...current, student]
                          })
                          setFailure(null)
                          setSuccess(null)
                        }}
                      >
                        <span className="font-mono font-semibold text-sm">{student.roll_no}</span>
                        <span className={`text-sm ml-2 ${selected ? 'text-canvas/80' : 'text-foreground-muted'}`}>
                          {student.display_name} · {student.branch} · {student.section}
                        </span>
                        <span className="sr-only">{selected ? ' Selected. Activate to remove.' : ' Not selected. Activate to add.'}</span>
                      </button>
                        )
                      })()}
                    </li>
                  ))}
                </ul>
              )}
              {studentQuery.isSuccess && debouncedQuery.length >= MIN_SEARCH_RUNES && studentOptions.length === 0 && (
                <p className="text-sm text-foreground-muted italic">No students matched your search.</p>
              )}
            </div>

            {/* Reason note */}
            <div className="flex flex-col gap-1.5">
              <label htmlFor="teacher-mark-reason" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">
                Reason note <span className="text-status-error-fg">*</span>
              </label>
              <textarea
                id="teacher-mark-reason"
                value={reasonNote}
                onChange={(event) => { setReasonNote(event.target.value); setFailure(null); setSuccess(null) }}
                maxLength={2000}
                required
                rows={3}
                placeholder="Reason for this manual search-mark…"
                className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all resize-none"
              />
            </div>

            {selectedStudents.length > 0 && (
              <div className="flex flex-wrap gap-2 p-3 bg-status-info-bg rounded-[10px] border border-status-info-fg/20" aria-live="polite" aria-label="Selected students">
                {selectedStudents.map((student) => (
                  <button key={student.student_id} type="button" aria-label={`Remove ${student.roll_no} from selected students`} className="text-left rounded-full bg-status-info-fg/15 px-3 py-1 text-xs font-semibold text-status-info-fg" onClick={() => setSelectedStudents((selected) => selected.filter((item) => item.student_id !== student.student_id))}>
                    {student.roll_no} ×
                  </button>
                ))}
              </div>
            )}

            {/* Success / error banners */}
            {success && (
              <div className="flex items-center gap-3 p-3 bg-status-success-bg rounded-[10px] border border-status-success-fg/20" role="status">
                <Check size={20} weight="bold" aria-hidden="true" className="text-status-success-fg" />
                <p className="text-sm font-semibold text-status-success-fg">
                  Marked {success.length} student{success.length === 1 ? '' : 's'} present on {success[0]?.od_date} in slot {success[0]?.slot}.
                </p>
              </div>
            )}
            {failure && (
              <div className="p-3 bg-status-error-bg rounded-[10px] border border-status-error-fg/20" role="alert">
                <p className="text-sm font-semibold text-status-error-fg">{failure}</p>
              </div>
            )}

            <button
              className="primary self-start"
              type="submit"
              disabled={markDisabled}
            >
              {mark.isPending ? 'Marking…' : 'Mark selected students'}
            </button>
          </form>
        )}
      </section>
    </PortalShell>
  )
}
