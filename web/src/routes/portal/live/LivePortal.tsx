import { useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { Select } from '../../../components/ui/Select'
import { Spinner } from '../../../components/ui/Spinner'
import { useAuth } from '../../../features/auth/context'
import { asString, usePersistentState } from '../../../features/persistence/sessionState'
import { campusToday } from './campusDay'

/** How often the list refreshes. Matches the admin attendance view. */
const LIVE_POLL_MS = 5_000

function formatTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return '—'
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    timeZone: 'Asia/Kolkata',
  }).format(parsed)
}

/**
 * The live view of a day's on-duty marks.
 *
 * WHAT IT ANSWERS: "who has been marked so far today?" — the question a teacher
 * asks repeatedly during an event, and which they previously had no way to ask
 * at all. The attendance read existed but was admin-only at both the route and
 * the service, so a teacher could mark students and then never see the result.
 *
 * The server scopes a teacher to the branches they teach. This screen does no
 * filtering of its own and must not start: a client-side filter over a wider
 * response would mean the rows were sent to the browser regardless.
 */
export default function LivePortal() {
  const { api, identity } = useAuth()

  const [eventID, setEventID] = usePersistentState('live.event.v1', '', asString)
  // The day is deliberately NOT persisted: this screen is about TODAY, and
  // coming back tomorrow to yesterday's list — while the heading says Live —
  // would be worse than losing a selection.
  const [odDate, setODDate] = usePersistentState('live.day.v1', campusToday(), asString)

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
  }, [eventID, eventOptions, setEventID])

  // A stored day from a previous visit is snapped back to today on arrival —
  // see the note above.
  useEffect(() => {
    const today = campusToday()
    setODDate((current) => (current && current < today ? today : current || today))
  }, [setODDate])

  const selectedEvent = eventOptions.find((event) => event.id === eventID)
  const dayOptions = useMemo(() => {
    if (!selectedEvent) return []
    const days: string[] = []
    const [year, month, day] = selectedEvent.event_date.split('-').map(Number)
    const cursor = new Date(year ?? 0, (month ?? 1) - 1, day ?? 1)
    const last = selectedEvent.end_date && selectedEvent.end_date > selectedEvent.event_date
      ? selectedEvent.end_date
      : selectedEvent.event_date
    for (let guard = 0; guard < 200; guard += 1) {
      const value = [
        String(cursor.getFullYear()).padStart(4, '0'),
        String(cursor.getMonth() + 1).padStart(2, '0'),
        String(cursor.getDate()).padStart(2, '0'),
      ].join('-')
      days.push(value)
      if (value >= last) break
      cursor.setDate(cursor.getDate() + 1)
    }
    return days
  }, [selectedEvent])

  const attendance = useQuery({
    queryKey: ['portal', 'live', eventID, odDate],
    queryFn: () => api.portalAttendance(eventID, { odDate, pageSize: 100 }),
    enabled: eventID !== '',
    // Stops polling once it has failed, so a refused or broken read does not
    // retry every five seconds for as long as the tab is open.
    refetchInterval: (query) => (query.state.error ? false : LIVE_POLL_MS),
  })

  const rows = attendance.data?.data ?? []

  return (
    <PortalShell>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Attendance</p>
          <h1>Live</h1>
          <p>
            {identity?.role === 'admin'
              ? 'Marks as they land, for the selected event and day.'
              : 'Marks as they land for students in the branches you teach.'}
          </p>
        </div>
      </div>

      <section className="detail-section">
        <div className="category-form" style={{ marginBottom: 18 }}>
          <label htmlFor="live-event">Event</label>
          <Select
            id="live-event"
            value={eventID}
            onChange={setEventID}
            disabled={eventOptions.length === 0}
            placeholder="No events"
            options={eventOptions.map((event) => ({ value: event.id, label: event.name }))}
          />

          {dayOptions.length > 1 && (
            <>
              <label htmlFor="live-day">Day</label>
              <Select
                id="live-day"
                value={odDate}
                onChange={setODDate}
                options={dayOptions.map((value) => ({ value, label: value }))}
              />
            </>
          )}

          {attendance.isFetching ? <Spinner label="Refreshing" size="small" /> : null}
        </div>

        {events.isSuccess && eventOptions.length === 0 && (
          <div className="empty-state" role="status">
            <h3>No events to show</h3>
            <p>
              Live attendance appears for an active event that covers one of your branches. An
              administrator adds your branch to an event to make it appear here.
            </p>
          </div>
        )}

        {attendance.isError && (
          <div className="notice error" role="alert">
            <span>
              {attendance.error instanceof ApiFailure
                ? attendance.error.body.message
                : 'Attendance could not be loaded.'}
            </span>
            <button type="button" onClick={() => void attendance.refetch()}>Try again</button>
          </div>
        )}

        {eventID !== '' && attendance.isSuccess && rows.length === 0 && (
          <div className="empty-state" role="status">
            <h3>Nothing marked yet</h3>
            <p>
              No on-duty attendance has been recorded for this day. New marks appear here on their
              own, without reloading.
            </p>
          </div>
        )}

        {rows.length > 0 && (
          <>
            <p className="empty-note" role="status">
              {attendance.data?.total ?? rows.length} marked on {odDate}
            </p>
            <div className="table-wrap">
              <table aria-label="Live attendance">
                <thead>
                  <tr>
                    <th scope="col">Roll number</th>
                    <th scope="col">Name</th>
                    <th scope="col">Branch</th>
                    <th scope="col">Slot</th>
                    <th scope="col">Category</th>
                    <th scope="col">Marked at</th>
                    <th scope="col">Marked by</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((record) => (
                    <tr key={record.record_id}>
                      <td>{record.student.roll_no}</td>
                      <td>{record.student.display_name}</td>
                      <td>{record.student.branch}</td>
                      <td>{record.slot}</td>
                      <td>{record.category.name}</td>
                      <td>{formatTime(record.server_received_at)}</td>
                      {/* NAME AND ROLE ONLY. `marked_by` also carries the
                          actor's email, and this table is read by teachers
                          across an event — there is no reason for a colleague's
                          address to be on it. */}
                      <td>{record.marked_by.name ?? record.actor_role}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </section>
    </PortalShell>
  )
}
