import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { CategoryRecord, AttendanceRecordCollection } from '../../../api/client'
import { Select } from '../../../components/ui/Select'
import { asString, usePersistentState } from '../../../features/persistence/sessionState'
import { useAuth } from '../../../features/auth/context'
import { enumerateDays, formatDate, messageOf } from './format'

const ATTENDANCE_POLL_MS = 5_000
const SLOT_VALUES = Array.from({ length: 12 }, (_, index) => index + 1)

export function EventAttendanceSection({
  eventID,
  categories,
  eventDate,
  endDate,
}: {
  eventID: string
  categories: CategoryRecord[]
  eventDate: string
  endDate: string
}) {
  const { api } = useAuth()
  // Keyed PER EVENT, so returning to a different event does not inherit the
  // filters from the last one — which would show an empty table and read as
  // "nobody was marked" rather than as a filter that was still applied.
  const [categoryID, setCategoryID] = usePersistentState(`attendance.${eventID}.category.v1`, '', asString)
  const [slot, setSlot] = usePersistentState(`attendance.${eventID}.slot.v1`, '', asString)
  const [odDate, setODDate] = usePersistentState(`attendance.${eventID}.day.v1`, '', asString)
  // The page is NOT persisted. Coming back to page 7 of a table whose contents
  // have moved on is worse than coming back to the top of it.
  const [page, setPage] = useState(1)

  const dayOptions = useMemo(() => enumerateDays(eventDate, endDate), [endDate, eventDate])

  useEffect(() => {
    setPage(1)
  }, [categoryID, slot, odDate])

  useEffect(() => {
    if (categories.length === 0) return
    if (!categories.some((category) => category.id === categoryID)) {
      setCategoryID('')
    }
  }, [categoryID, categories, setCategoryID])

  const attendance = useQuery({
    queryKey: ['admin', 'attendance', eventID, categoryID || 'all', slot || 'all', odDate || 'all', page],
    queryFn: () => api.adminAttendance(eventID, {
      categoryID: categoryID || undefined,
      slot: slot ? Number(slot) : undefined,
      odDate: odDate || undefined,
      page,
      pageSize: 20,
    }),
    enabled: eventID !== '',
    refetchInterval: (query) => (query.state.error ? false : ATTENDANCE_POLL_MS),
  })

  const rows = attendance.data?.data ?? []
  const total = attendance.data?.total ?? 0
  const pageSize = attendance.data?.page_size ?? 20
  const currentPage = attendance.data?.page ?? page
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  const pageLabel = useMemo(() => `${currentPage} / ${totalPages}`, [currentPage, totalPages])

  return (
    <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm" aria-label="Attendance">
      <div className="mb-4">
        <h2 className="text-lg font-bold tracking-tight">Attendance</h2>
        <p className="text-sm text-foreground-muted mt-1">
          Review present marks for this event, filter by day, category or slot, and watch for new rows to appear.
        </p>
      </div>

      {/* Filters + pagination row */}
      <div className="flex flex-wrap items-end gap-3 mb-5">
        <div className="flex flex-col gap-1">
          <label htmlFor="attendance-category" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Category</label>
          <Select
            id="attendance-category"
            value={categoryID}
            onChange={setCategoryID}
            options={[
              { value: '', label: 'All categories' },
              ...categories.map((category) => ({ value: category.id, label: category.name })),
            ]}
          />
        </div>

        {dayOptions.length > 1 && (
          <div className="flex flex-col gap-1">
            <label htmlFor="attendance-day" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Day</label>
            <Select
              id="attendance-day"
              value={odDate}
              onChange={setODDate}
              options={[
                { value: '', label: 'All days' },
                ...dayOptions.map((value) => ({ value, label: formatDate(value) })),
              ]}
            />
          </div>
        )}

        <div className="flex flex-col gap-1">
          <label htmlFor="attendance-slot" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Slot</label>
          <Select
            id="attendance-slot"
            value={slot}
            onChange={setSlot}
            options={[
              { value: '', label: 'All slots' },
              ...SLOT_VALUES.map((value) => ({ value: String(value), label: String(value) })),
            ]}
          />
        </div>

        <div className="ml-auto flex items-center gap-2" aria-label="Attendance pagination">
          <button
            type="button"
            onClick={() => setPage((current) => Math.max(1, current - 1))}
            disabled={page <= 1 || attendance.isPending}
            className="text-sm font-medium px-3 py-1.5 rounded-[10px] border border-border hover:bg-canvas active:scale-95 transition-all disabled:opacity-50"
          >
            Prev
          </button>
          <span className="text-sm text-foreground-muted tabular-nums">{pageLabel}</span>
          <button
            type="button"
            onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
            disabled={page >= totalPages || attendance.isPending}
            className="text-sm font-medium px-3 py-1.5 rounded-[10px] border border-border hover:bg-canvas active:scale-95 transition-all disabled:opacity-50"
          >
            Next
          </button>
          {attendance.isFetching && (
            <span className="flex items-center gap-1.5 text-xs text-foreground-muted">
              <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
            </span>
          )}
        </div>
      </div>

      {/* Loading state */}
      {attendance.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-4" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading attendance…
        </div>
      )}

      {/* Error state */}
      {attendance.isError && (
        <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-4" role="alert">
          <span className="font-medium text-sm">{messageOf(attendance.error, 'Attendance could not be loaded')}</span>
          <button
            type="button"
            onClick={() => void attendance.refetch()}
            className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
          >
            Try again
          </button>
        </div>
      )}

      {/* Empty state */}
      {attendance.isSuccess && rows.length === 0 && (
        <p className="text-sm text-foreground-muted italic text-center py-8">No marks yet for the selected filters.</p>
      )}

      {/* Table */}
      {attendance.isSuccess && rows.length > 0 && (
        <>
          <p className="text-sm text-foreground-muted mb-3">
            Showing <span className="font-semibold text-foreground">{rows.length}</span> of{' '}
            <span className="font-semibold text-foreground">{total}</span> marks
          </p>
          <div className="table-wrap overflow-x-auto rounded-[10px] border border-border">
            <table className="w-full text-left border-collapse" aria-label="Event attendance">
              <thead className="bg-canvas border-b border-border">
                <tr>
                  {['Student', 'Category', 'Day', 'Slot', 'Marked by', 'Role', 'Method', 'Server time'].map((col) => (
                    <th key={col} className="py-2.5 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                      {col}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {rows.map((row) => {
                  const markedBy = row.marked_by.name ?? row.marked_by.email ?? 'Unknown user'
                  return (
                    <tr key={row.record_id} className="hover:bg-canvas/50 transition-colors">
                      <th scope="row" className="py-3 px-4 font-normal text-left">
                        <div className="text-sm font-semibold">{row.student.display_name}</div>
                        <div className="text-xs text-foreground-muted font-mono">{row.student.roll_no} · {row.student.branch} · {row.student.section}</div>
                      </th>
                      <td className="py-3 px-4 text-sm">{row.category.name}</td>
                      <td className="py-3 px-4 text-sm text-foreground-muted">{formatDate(row.od_date)}</td>
                      <td className="py-3 px-4 text-center">
                        <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-foreground/10 text-xs font-bold">
                          {row.slot}
                        </span>
                      </td>
                      <td className="py-3 px-4">
                        <div className="text-sm font-medium">{markedBy}</div>
                        {row.marked_by.email && <div className="text-xs text-foreground-muted">{row.marked_by.email}</div>}
                      </td>
                      <td className="py-3 px-4 text-sm text-foreground-muted">{row.actor_role}</td>
                      <td className="py-3 px-4 text-sm text-foreground-muted">{row.capture_method}</td>
                      <td className="py-3 px-4 text-xs text-foreground-muted tabular-nums whitespace-nowrap">
                        {new Intl.DateTimeFormat('en-GB', {
                          day: '2-digit',
                          month: 'short',
                          year: 'numeric',
                          hour: '2-digit',
                          minute: '2-digit',
                        }).format(new Date(row.server_received_at))}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  )
}

export type AttendanceRows = AttendanceRecordCollection
