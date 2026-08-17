import { useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { Select } from '../../../components/ui/Select'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { ClipboardText } from '@phosphor-icons/react/dist/icons/ClipboardText'
import { Hourglass } from '@phosphor-icons/react/dist/icons/Hourglass'
import { useAuth } from '../../../features/auth/context'
import { asString, usePersistentState } from '../../../features/persistence/sessionState'
import { campusToday } from './campusDay'

/**
 * How often the list refreshes. Matches the admin attendance view.
 *
 * This was 5s. Each poll costs the server a list query, a full re-count and,
 * until recently, two uncached authorization queries — and the screen polls
 * whether or not anything is happening. Across this screen, the admin
 * attendance view and the mark status endpoint, production measured ~51,000
 * queries of pure polling against 319 actual attendance marks.
 *
 * Twenty seconds is still "live" for a list a person is watching fill up, and
 * it is a quarter of the load. The `enabled` guard below matters more than the
 * number: an idle screen now polls not at all rather than slowly.
 */
const LIVE_POLL_MS = 20_000

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
 */
export default function LivePortal() {
  const { api, identity } = useAuth()

  const [eventID, setEventID] = usePersistentState('live.event.v1', '', asString)
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
    // A PAST DAY IS NOT LIVE. Marks land on the campus day they were captured
    // on, so a day that is already over cannot gain rows — polling it forever is
    // pure cost for an answer that is settled. The screen still polls today at
    // LIVE_POLL_MS; it simply stops asking about history.
    //
    // The error guard is unchanged: a failed request stops the timer rather than
    // hammering a server that just refused.
    refetchInterval: (query) => {
      if (query.state.error) return false
      return odDate === campusToday() ? LIVE_POLL_MS : false
    },
  })

  const rows = attendance.data?.data ?? []

  return (
    <PortalShell>
      {/* Page Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-8">
        <div>
          <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Attendance</p>
          <div className="flex items-center gap-3">
            <h1 className="text-3xl font-bold tracking-tight">Live</h1>
            {/* Live pulse indicator */}
            <span className="flex items-center gap-1.5 text-xs font-semibold text-status-success-fg bg-status-success-bg px-2.5 py-1 rounded-full border border-status-success-fg/20">
              <span className="w-1.5 h-1.5 rounded-full bg-status-success-fg animate-pulse" aria-hidden="true" />
              Live
            </span>
          </div>
          <p className="text-foreground-muted mt-1">
            {identity?.role === 'admin'
              ? 'Marks as they land, for the selected event and day.'
              : 'Marks as they land for students in the branches you teach.'}
          </p>
        </div>

        {/* Controls */}
        <div className="flex flex-col sm:flex-row gap-3 items-start sm:items-center">
          {attendance.isFetching && (
            <span className="flex items-center gap-2 text-xs text-foreground-muted">
              <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
              Refreshing…
            </span>
          )}
          <div className="flex flex-col sm:flex-row gap-2">
            <div className="flex flex-col gap-1">
              <label htmlFor="live-event" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Event</label>
              <Select
                id="live-event"
                value={eventID}
                onChange={setEventID}
                disabled={eventOptions.length === 0}
                placeholder="No events"
                options={eventOptions.map((event) => ({ value: event.id, label: event.name }))}
              />
            </div>
            {dayOptions.length > 1 && (
              <div className="flex flex-col gap-1">
                <label htmlFor="live-day" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Day</label>
                <Select
                  id="live-day"
                  value={odDate}
                  onChange={setODDate}
                  options={dayOptions.map((value) => ({ value, label: value }))}
                />
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Empty state: no events */}
      {events.isSuccess && eventOptions.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3" role="status">
          <ClipboardText size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">No events to show</h2>
          <p className="text-foreground-muted max-w-sm">
            Live attendance appears for an active event that covers one of your branches. An
            administrator adds your branch to an event to make it appear here.
          </p>
        </div>
      )}

      {/* Error state */}
      {attendance.isError && (
        <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-6" role="alert">
          <span className="font-medium text-sm">
            {attendance.error instanceof ApiFailure
              ? attendance.error.body.message
              : 'Attendance could not be loaded.'}
          </span>
          <button
            type="button"
            onClick={() => void attendance.refetch()}
            className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
          >
            Try again
          </button>
        </div>
      )}

      {/* Empty state: nothing marked yet */}
      {eventID !== '' && attendance.isSuccess && rows.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3" role="status">
          <Hourglass size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">Nothing marked yet</h2>
          <p className="text-foreground-muted max-w-sm">
            No on-duty attendance has been recorded for this day. New marks appear here on their
            own, without reloading.
          </p>
        </div>
      )}

      {/* Records table */}
      {rows.length > 0 && (
        <div className="flex flex-col gap-4">
          <p className="text-sm font-medium text-foreground-muted" role="status">
            <span className="font-bold text-foreground">{attendance.data?.total ?? rows.length}</span> marked on {odDate}
          </p>

          <div className="bg-surface border border-border rounded-[10px] overflow-hidden shadow-sm">
            <div className="overflow-x-auto">
              <table className="w-full text-left border-collapse" aria-label="Live attendance">
                <thead className="bg-canvas border-b border-border">
                  <tr>
                    {['Roll number', 'Name', 'Branch', 'Slot', 'Category', 'Marked at', 'Marked by'].map((col) => (
                      <th key={col} scope="col" className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {rows.map((record) => (
                    <tr key={record.record_id} className="hover:bg-canvas/50 transition-colors">
                      <td className="py-3 px-4 font-mono text-sm font-medium">{record.student.roll_no}</td>
                      <td className="py-3 px-4 text-sm">{record.student.display_name}</td>
                      <td className="py-3 px-4">
                        <span className="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold bg-canvas border border-border">
                          {record.student.branch}
                        </span>
                      </td>
                      <td className="py-3 px-4 text-sm text-center">
                        <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-foreground/10 text-xs font-bold">
                          {record.slot}
                        </span>
                      </td>
                      <td className="py-3 px-4 text-sm">{record.category.name}</td>
                      <td className="py-3 px-4 text-sm text-foreground-muted tabular-nums">{formatTime(record.server_received_at)}</td>
                      <td className="py-3 px-4 text-sm text-foreground-muted">{record.marked_by.name ?? record.actor_role}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}
    </PortalShell>
  )
}
