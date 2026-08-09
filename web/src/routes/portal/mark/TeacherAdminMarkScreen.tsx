import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { MarkAttendanceResult, ResolvedCardStudent } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { Select } from '../../../components/ui/Select'
import { useAuth } from '../../../features/auth/context'

const SEARCH_DEBOUNCE_MS = 250
const MIN_SEARCH_RUNES = 3
const MAX_SEARCH_RUNES = 64
const SLOT_VALUES = Array.from({ length: 12 }, (_, index) => index + 1)

export default function TeacherAdminMarkScreen() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [eventID, setEventID] = useState('')
  const [categoryID, setCategoryID] = useState('')
  const [slot, setSlot] = useState('1')
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [selectedStudent, setSelectedStudent] = useState<ResolvedCardStudent | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [success, setSuccess] = useState<MarkAttendanceResult | null>(null)

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [query])

  const events = useQuery({
    queryKey: ['portal', 'mark', 'events'],
    queryFn: () => api.portalMarkEvents(),
  })
  const eventOptions = useMemo(() => events.data?.data ?? [], [events.data])

  useEffect(() => {
    if (eventOptions.length === 0) return
    if (!eventOptions.some((event) => event.id === eventID)) {
      setEventID(eventOptions[0].id)
    }
  }, [eventID, eventOptions])

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
  }, [categoryID, categoryOptions])

  useEffect(() => {
    setSelectedStudent(null)
  }, [eventID, categoryID])

  const studentQuery = useQuery({
    queryKey: ['portal', 'mark', 'students', debouncedQuery],
    queryFn: () => api.searchStudents(debouncedQuery),
    enabled: debouncedQuery.length >= MIN_SEARCH_RUNES && debouncedQuery.length <= MAX_SEARCH_RUNES,
    gcTime: 0,
  })
  const studentOptions = useMemo(() => studentQuery.data?.data ?? [], [studentQuery.data])

  const mark = useMutation({
    mutationFn: () => {
      if (!selectedStudent) throw new Error('Choose a student before marking.')
      return api.mark({
        event_id: eventID,
        category_id: categoryID,
        capture_method: 'search',
        student_id: selectedStudent.student_id,
        slot: Number(slot),
      })
    },
    onSuccess(result) {
      setFailure(null)
      setSuccess(result)
      setQuery('')
      setDebouncedQuery('')
      setSelectedStudent(null)
      void queryClient.invalidateQueries({ queryKey: ['admin', 'attendance', eventID] })
    },
    onError(cause) {
      setSuccess(null)
      setFailure(cause instanceof ApiFailure ? cause.body.message : cause instanceof Error ? cause.message : 'The student could not be marked.')
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        void events.refetch()
      }
    },
  })

  const eventReady = eventID !== '' && categoryID !== ''
  const markDisabled = !eventReady || !selectedStudent || mark.isPending

  const changeQuery = (value: string) => {
    setQuery(value)
    setFailure(null)
    setSuccess(null)
  }

  const changeEvent = (value: string) => {
    setEventID(value)
    setFailure(null)
    setSuccess(null)
    setSelectedStudent(null)
  }

  const changeCategory = (value: string) => {
    setCategoryID(value)
    setFailure(null)
    setSuccess(null)
    setSelectedStudent(null)
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
      <div className="page-heading">
        <div>
          <p className="eyebrow">Event administration</p>
          <h1>Mark attendance</h1>
          <p>Search-mark present students for an event and category, with an explicit slot.</p>
        </div>
      </div>

      <section className="detail-section">
        <h2>Search mark</h2>
        <p>Choose the event, category, slot, then search and select one student.</p>

        {events.isPending && <p role="status">Loading events…</p>}
        {events.isError && (
          <div className="notice error" role="alert">
            <span>{events.error instanceof ApiFailure ? events.error.body.message : 'Events could not be loaded.'}</span>
            <button type="button" onClick={() => void events.refetch()}>Try again</button>
          </div>
        )}
        {events.isSuccess && eventOptions.length === 0 && (
          <p className="empty-note">No events available to mark.</p>
        )}
        {categories.isError && (
          <div className="notice error" role="alert">
            <span>{categories.error instanceof ApiFailure ? categories.error.body.message : 'Categories could not be loaded.'}</span>
            <button type="button" onClick={() => void categories.refetch()}>Try again</button>
          </div>
        )}
        {categories.isSuccess && categoryOptions.length === 0 && (
          <p className="empty-note">No categories available for this event.</p>
        )}

        <form className="category-form" onSubmit={submit} noValidate>
          <label htmlFor="teacher-mark-event">Event</label>
          <Select
            id="teacher-mark-event"
            value={eventID}
            onChange={changeEvent}
            options={eventOptions.map((event) => ({ value: event.id, label: event.name }))}
          />

          <label htmlFor="teacher-mark-category">Category</label>
          <Select
            id="teacher-mark-category"
            value={categoryID}
            onChange={changeCategory}
            disabled={categoryOptions.length === 0}
            options={categoryOptions.map((category) => ({ value: category.id, label: category.name }))}
          />

          <label htmlFor="teacher-mark-slot">Slot</label>
          <Select
            id="teacher-mark-slot"
            value={slot}
            onChange={changeSlot}
            options={SLOT_VALUES.map((value) => ({ value: String(value), label: String(value) }))}
          />

          <label htmlFor="teacher-mark-student">Student</label>
          <input
            id="teacher-mark-student"
            type="search"
            role="searchbox"
            value={query}
            onChange={(event) => changeQuery(event.target.value)}
            autoComplete="off"
            aria-describedby="teacher-mark-student-help"
          />
          <p id="teacher-mark-student-help" className="empty-note">Search by roll number or name.</p>

          <button className="primary" type="submit" disabled={markDisabled}>Mark present</button>
        </form>

        {selectedStudent && (
          <p className="notice" role="status">
            Selected {selectedStudent.display_name} · {selectedStudent.roll_no}
          </p>
        )}
        {success && (
          <p className="notice success" role="status">
            Marked {success.student.display_name} present in slot {success.slot}.
          </p>
        )}
        {failure && <p className="notice error" role="alert">{failure}</p>}

        {debouncedQuery.length > 0 && debouncedQuery.length < MIN_SEARCH_RUNES && (
          <p className="empty-note">Keep typing to search students.</p>
        )}
        {debouncedQuery.length > MAX_SEARCH_RUNES && (
          <p className="empty-note">Search text is too long.</p>
        )}
        {studentQuery.isPending && studentQuery.fetchStatus !== 'idle' && <p role="status">Searching students…</p>}
        {studentQuery.isError && (
          <div className="notice error" role="alert">
            <span>{studentQuery.error instanceof ApiFailure ? studentQuery.error.body.message : 'Student search failed.'}</span>
            <button type="button" onClick={() => void studentQuery.refetch()}>Try again</button>
          </div>
        )}
        {studentQuery.isSuccess && studentOptions.length > 0 && (
          <ul className="category-list" role="listbox" aria-label="Student search results">
            {studentOptions.map((student) => (
              <li key={student.student_id}>
                <button
                  type="button"
                  role="option"
                  onClick={() => {
                    setSelectedStudent(student)
                    setQuery(student.roll_no)
                    setDebouncedQuery(student.roll_no)
                    setFailure(null)
                    setSuccess(null)
                  }}
                >
                  <span>{student.roll_no}</span>
                  <small>{student.display_name}</small>
                  <time>{student.branch} · {student.section}</time>
                </button>
              </li>
            ))}
          </ul>
        )}
        {studentQuery.isSuccess && debouncedQuery.length >= MIN_SEARCH_RUNES && studentOptions.length === 0 && (
          <p className="empty-note">No students matched your search.</p>
        )}
      </section>
    </PortalShell>
  )
}
