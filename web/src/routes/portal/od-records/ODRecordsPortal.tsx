import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ApiFailure, type AttendanceRecord } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'

const slots = Array.from({ length: 12 }, (_, index) => index + 1)
const pageSize = 50
// A wide, unfiltered page used only to source the section/coordinator filter
// dropdowns, so choosing one filter doesn't shrink the options available for
// the others.
const filterOptionsPageSize = 200

export default function ODRecordsPortal() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [params, setParams] = useSearchParams()
  const [section, setSection] = useState('')
  const [actorUserID, setActorUserID] = useState('')
  const [categoryID, setCategoryID] = useState('')
  const [slot, setSlot] = useState('')
  const [odDate, setODDate] = useState('')
  const [page, setPage] = useState(1)
  const [studentID, setStudentID] = useState('')
  const [historyID, setHistoryID] = useState('')
  const [historyPage, setHistoryPage] = useState(1)
  const [selected, setSelected] = useState<AttendanceRecord | null>(null)
  const [mode, setMode] = useState<'correct' | 'remove' | null>(null)
  const [reasonNote, setReasonNote] = useState('')
  const [correctionSlot, setCorrectionSlot] = useState('')
  const [correctionCategoryID, setCorrectionCategoryID] = useState('')
  const [correctionDate, setCorrectionDate] = useState('')
  const [auditRecordID, setAuditRecordID] = useState('')
  const eventID = params.get('event_id') ?? ''
  const events = useQuery({ queryKey: ['portal', 'mark', 'events'], queryFn: () => api.portalMarkEvents() })
  const categories = useQuery({ queryKey: ['portal', 'mark', 'categories', eventID], queryFn: () => api.portalMarkCategories(eventID), enabled: eventID !== '' })
  useEffect(() => { setPage(1) }, [eventID, categoryID, slot, odDate, section, actorUserID])
  const records = useQuery({
    queryKey: ['portal', 'records', eventID, categoryID, slot, odDate, section, actorUserID, page],
    queryFn: () => api.portalRecords(eventID, {
      categoryID: categoryID || undefined,
      slot: slot ? Number(slot) : undefined,
      odDate: odDate || undefined,
      section: section || undefined,
      actorUserID: actorUserID || undefined,
      page,
      pageSize,
    }),
    enabled: eventID !== '',
  })
  const filterOptions = useQuery({
    queryKey: ['portal', 'records', 'options', eventID],
    queryFn: () => api.portalRecords(eventID, { pageSize: filterOptionsPageSize }),
    enabled: eventID !== '',
  })
  useEffect(() => { setHistoryPage(1) }, [historyID])
  const history = useQuery({ queryKey: ['portal', 'student-history', historyID, historyPage], queryFn: () => api.studentODHistory(historyID, historyPage, pageSize), enabled: historyID !== '' })
  const audit = useQuery({ queryKey: ['portal', 'record-audit', auditRecordID], queryFn: () => api.odRecordAudit(auditRecordID), enabled: auditRecordID !== '' })
  const eventOptions = events.data?.data ?? []
  const categoryOptions = categories.data?.data ?? []
  const actors = useMemo(() => [...new Map((filterOptions.data?.data ?? []).flatMap((row) => row.marked_by.user_id ? [[row.marked_by.user_id, row.marked_by.name ?? row.marked_by.email ?? row.marked_by.user_id]] : [])).entries()], [filterOptions.data])
  const sections = useMemo(() => [...new Set((filterOptions.data?.data ?? []).map((row) => row.student.section).filter(Boolean))], [filterOptions.data])
  const totalPages = records.data ? Math.max(1, Math.ceil(records.data.total / records.data.page_size)) : 1
  const historyTotalPages = history.data ? Math.max(1, Math.ceil(history.data.total / history.data.page_size)) : 1
  const mutate = useMutation({
    mutationFn: async () => {
      if (!selected || !mode || reasonNote.trim() === '') throw new Error('Reason note is required.')
      if (mode === 'remove') return api.removeODRecord(selected.record_id, { reason_note: reasonNote.trim() })
      const input = {
        reason_note: reasonNote.trim(),
        ...(correctionSlot !== '' && Number(correctionSlot) !== selected.slot ? { slot: Number(correctionSlot) } : {}),
        ...(correctionCategoryID !== '' && correctionCategoryID !== selected.category.id ? { category_id: correctionCategoryID } : {}),
        ...(correctionDate !== '' && correctionDate !== selected.od_date ? { od_date: correctionDate } : {}),
      }
      return api.correctODRecord(selected.record_id, input)
    },
    onSuccess: () => {
      setSelected(null); setMode(null); setReasonNote(''); setCorrectionSlot(''); setCorrectionCategoryID(''); setCorrectionDate('')
      void queryClient.invalidateQueries({ queryKey: ['portal', 'records', eventID] })
      void queryClient.invalidateQueries({ queryKey: ['portal', 'student-history'] })
    },
  })
  const error = records.error ?? mutate.error ?? history.error ?? audit.error
  const openAction = (record: AttendanceRecord, action: 'correct' | 'remove') => {
    setSelected(record); setMode(action); setReasonNote('')
    setCorrectionSlot(action === 'correct' ? String(record.slot) : '')
    setCorrectionCategoryID(action === 'correct' ? record.category.id : '')
    setCorrectionDate(action === 'correct' ? record.od_date : '')
  }
  return <PortalShell><div className="page-heading"><div><p className="eyebrow">Attendance</p><h1>Manage OD records</h1><p>Review, correct, or remove attendance with an auditable reason.</p></div></div>
    <section className="detail-section"><label htmlFor="record-event">Event</label><select id="record-event" value={eventID} onChange={(e) => setParams(e.target.value ? { event_id: e.target.value } : {})}><option value="">Choose an event</option>{eventOptions.map((event) => <option key={event.id} value={event.id}>{event.name}</option>)}</select>
      <label htmlFor="record-category">Category</label><select id="record-category" value={categoryID} onChange={(e) => setCategoryID(e.target.value)}><option value="">All categories</option>{categoryOptions.map((category) => <option key={category.id} value={category.id}>{category.name}</option>)}</select>
      <label htmlFor="record-slot">Slot</label><select id="record-slot" value={slot} onChange={(e) => setSlot(e.target.value)}><option value="">All slots</option>{slots.map((number) => <option key={number} value={number}>{number}</option>)}</select>
      <label htmlFor="record-day">OD day</label><input id="record-day" type="date" value={odDate} onChange={(e) => setODDate(e.target.value)} />
      <label htmlFor="record-section">Section</label><select id="record-section" value={section} onChange={(e) => setSection(e.target.value)}><option value="">All sections</option>{sections.map((value) => <option key={value} value={value}>{value}</option>)}</select>
      <label htmlFor="record-actor">Coordinator / marked by</label><select id="record-actor" value={actorUserID} onChange={(e) => setActorUserID(e.target.value)}><option value="">Everyone</option>{actors.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select>
      {error && <p className="notice error" role="alert">{error instanceof ApiFailure ? error.body.message : error instanceof Error ? error.message : 'Records could not be updated.'}</p>}
      {records.isPending && <p role="status">Loading records…</p>}
      {records.data && <table><thead><tr><th>Student</th><th>Category</th><th>Slot</th><th>Day</th><th>Status</th><th>Reason</th><th>Actions</th></tr></thead><tbody>{records.data.data.map((row) => <tr key={row.record_id}><td>{row.student.display_name}</td><td>{row.category.name}</td><td>Slot {row.slot}</td><td>{row.od_date}</td><td>{row.status}</td><td>{row.reason_note ?? '—'}</td><td><button type="button" disabled={row.status === 'removed'} onClick={() => openAction(row, 'correct')}>Correct</button><button type="button" disabled={row.status === 'removed'} onClick={() => openAction(row, 'remove')}>Remove</button><button type="button" onClick={() => setAuditRecordID(row.record_id)}>Audit</button></td></tr>)}</tbody></table>}
      {records.data && records.data.total > records.data.page_size && <p className="pagination"><button type="button" disabled={page <= 1} onClick={() => setPage((current) => current - 1)}>Previous</button><span> Page {page} of {totalPages} ({records.data.total} records) </span><button type="button" disabled={page >= totalPages} onClick={() => setPage((current) => current + 1)}>Next</button></p>}
    </section>
    <section className="detail-section"><h2>Student record history</h2><label htmlFor="student-history-id">Student ID</label><input id="student-history-id" value={studentID} onChange={(e) => setStudentID(e.target.value)} /><button type="button" onClick={() => setHistoryID(studentID.trim())} disabled={studentID.trim() === ''}>View history</button>{history.data && <ul>{history.data.data.map((row) => <li key={row.record_id}>{row.od_date}: {row.category.name}, slot {row.slot} ({row.status})</li>)}</ul>}{history.data && history.data.total > history.data.page_size && <p className="pagination"><button type="button" disabled={historyPage <= 1} onClick={() => setHistoryPage((current) => current - 1)}>Previous</button><span> Page {historyPage} of {historyTotalPages} ({history.data.total} records) </span><button type="button" disabled={historyPage >= historyTotalPages} onClick={() => setHistoryPage((current) => current + 1)}>Next</button></p>}</section>
    {selected && mode && <dialog open aria-label={mode === 'correct' ? 'Correct record' : 'Remove record'}><form onSubmit={(e) => { e.preventDefault(); mutate.mutate() }}><h2>{mode === 'correct' ? 'Correct record' : 'Remove record'}</h2>{mode === 'correct' && <><label htmlFor="correction-category">Category</label><select id="correction-category" value={correctionCategoryID} onChange={(e) => setCorrectionCategoryID(e.target.value)}>{categoryOptions.map((category) => <option key={category.id} value={category.id}>{category.name}</option>)}</select><label htmlFor="correction-slot">Slot</label><select id="correction-slot" value={correctionSlot} onChange={(e) => setCorrectionSlot(e.target.value)}>{slots.map((number) => <option key={number} value={number}>{number}</option>)}</select><label htmlFor="correction-day">OD day</label><input id="correction-day" type="date" value={correctionDate} onChange={(e) => setCorrectionDate(e.target.value)} /></>}<label htmlFor="record-reason">Reason note</label><textarea id="record-reason" required maxLength={2000} value={reasonNote} onChange={(e) => setReasonNote(e.target.value)} /><button type="button" onClick={() => { setSelected(null); setMode(null) }}>Cancel</button><button type="submit" disabled={reasonNote.trim() === '' || mutate.isPending}>{mode === 'correct' ? 'Save correction' : 'Confirm remove'}</button></form></dialog>}
    {auditRecordID && <dialog open aria-label="Record audit"><h2>Record audit</h2>{audit.isPending && <p role="status">Loading audit…</p>}{audit.data && <ul>{audit.data.data.map((entry) => <li key={`${entry.action}-${entry.occurred_at}`}>{entry.occurred_at}: {entry.action} by {entry.actor_email ?? entry.actor_role ?? 'system'} — {JSON.stringify(entry.payload)}</li>)}</ul>}<button type="button" onClick={() => setAuditRecordID('')}>Close audit</button></dialog>}
  </PortalShell>
}
