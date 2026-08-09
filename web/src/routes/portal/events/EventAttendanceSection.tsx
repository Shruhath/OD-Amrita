import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import type { CategoryRecord, AttendanceRecordCollection } from '../../../api/client'
import { Select } from '../../../components/ui/Select'
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
  const [categoryID, setCategoryID] = useState('')
  const [slot, setSlot] = useState('')
  const [odDate, setODDate] = useState('')
  const [page, setPage] = useState(1)

  // Bounded to the event's own days, like every other filter here. A free date
  // input would let an admin ask for a day the event never covered and get an
  // empty table that looks like "nobody was marked".
  const dayOptions = useMemo(() => enumerateDays(eventDate, endDate), [endDate, eventDate])

  useEffect(() => {
    setPage(1)
  }, [categoryID, slot, odDate])

  useEffect(() => {
    if (categories.length === 0) return
    if (!categories.some((category) => category.id === categoryID)) {
      setCategoryID('')
    }
  }, [categoryID, categories])

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
    <section className="detail-section">
      <h2>Attendance</h2>
      <p>Review present marks for this event, filter by day, category or slot, and watch for new rows to appear.</p>

      <div className="category-form" style={{ marginBottom: 18 }}>
        <label htmlFor="attendance-category">Category</label>
        <Select
          id="attendance-category"
          value={categoryID}
          onChange={setCategoryID}
          options={[
            { value: '', label: 'All categories' },
            ...categories.map((category) => ({ value: category.id, label: category.name })),
          ]}
        />

        {dayOptions.length > 1 && (
          <>
            <label htmlFor="attendance-day">Day</label>
            <Select
              id="attendance-day"
              value={odDate}
              onChange={setODDate}
              options={[
                { value: '', label: 'All days' },
                ...dayOptions.map((value) => ({ value, label: formatDate(value) })),
              ]}
            />
          </>
        )}

        <label htmlFor="attendance-slot">Slot</label>
        <Select
          id="attendance-slot"
          value={slot}
          onChange={setSlot}
          options={[
            { value: '', label: 'All slots' },
            ...SLOT_VALUES.map((value) => ({ value: String(value), label: String(value) })),
          ]}
        />

        <div className="pagination" aria-label="Attendance pagination">
          <button type="button" onClick={() => setPage((current) => Math.max(1, current - 1))} disabled={page <= 1 || attendance.isPending}>Prev</button>
          <span>{pageLabel}</span>
          <button type="button" onClick={() => setPage((current) => Math.min(totalPages, current + 1))} disabled={page >= totalPages || attendance.isPending}>Next</button>
        </div>
      </div>

      {attendance.isPending && <p role="status">Loading attendance…</p>}
      {attendance.isError && (
        <div className="notice error" role="alert">
          <span>{messageOf(attendance.error, 'Attendance could not be loaded')}</span>
          <button type="button" onClick={() => void attendance.refetch()}>Try again</button>
        </div>
      )}

      {attendance.isSuccess && rows.length === 0 && <p className="empty-note">No marks yet</p>}
      {attendance.isSuccess && rows.length > 0 && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Student</th>
                <th>Category</th>
                <th>Day</th>
                <th>Slot</th>
                <th>Marked by</th>
                <th>Role</th>
                <th>Method</th>
                <th>Server time</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const markedBy = row.marked_by.name ?? row.marked_by.email ?? 'Unknown user'
                return (
                  <tr key={row.record_id}>
                    <th scope="row">
                      <div>{row.student.display_name}</div>
                      <small>{row.student.roll_no} · {row.student.branch} · {row.student.section}</small>
                    </th>
                    <td>{row.category.name}</td>
                    <td>{formatDate(row.od_date)}</td>
                    <td>{row.slot}</td>
                    <td>
                      <div>{markedBy}</div>
                      {row.marked_by.email && <small>{row.marked_by.email}</small>}
                    </td>
                    <td>{row.actor_role}</td>
                    <td>{row.capture_method}</td>
                    <td>{new Intl.DateTimeFormat('en-GB', {
                      day: '2-digit',
                      month: 'short',
                      year: 'numeric',
                      hour: '2-digit',
                      minute: '2-digit',
                    }).format(new Date(row.server_received_at))}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

export type AttendanceRows = AttendanceRecordCollection
