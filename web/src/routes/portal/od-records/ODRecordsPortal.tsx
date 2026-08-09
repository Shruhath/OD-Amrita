import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
import { ApiFailure, type AttendanceRecord } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'
import { AnimatePresence, motion } from 'motion/react'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { Archive } from '@phosphor-icons/react/dist/icons/Archive'
import { ArrowClockwise } from '@phosphor-icons/react/dist/icons/ArrowClockwise'
import { ClockCounterClockwise } from '@phosphor-icons/react/dist/icons/ClockCounterClockwise'
import { Funnel } from '@phosphor-icons/react/dist/icons/Funnel'
import { MagnifyingGlass } from '@phosphor-icons/react/dist/icons/MagnifyingGlass'
import { NotePencil } from '@phosphor-icons/react/dist/icons/NotePencil'
import { Trash } from '@phosphor-icons/react/dist/icons/Trash'
import { Button } from '../../../components/ui/button'

const slots = Array.from({ length: 12 }, (_, index) => index + 1)
const pageSize = 50
const filterOptionsPageSize = 200

function StatusBadge({ status }: { status: string }) {
  const tone = status === 'removed'
    ? 'bg-status-error-bg text-status-error-fg'
    : status === 'corrected'
      ? 'bg-status-warning-bg text-status-warning-fg'
      : 'bg-status-success-bg text-status-success-fg'
  return <span className={`inline-flex items-center px-2 py-1 text-xs font-bold uppercase tracking-wider ${tone}`}>{status}</span>
}

/**
 * A native <select> wrapper that gives Playwright (and assistive technology)
 * a real combobox element. The filter panel uses this so that
 * page.getByRole('combobox', …).selectOption(…) works in e2e specs.
 * The correction-modal selects keep Radix for the richer dropdown UX.
 */
function StyledSelect({
  id,
  value,
  onChange,
  disabled,
  options,
  label,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  disabled?: boolean
  options: Array<{ value: string; label: string }>
  label: string
}) {
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">{label}</label>
      <select
        id={id}
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all appearance-none"
      >
        {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </div>
  )
}

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

  return (
    <PortalShell>
      <header className="mb-8 rounded-2xl border border-border bg-surface p-5 shadow-sm sm:p-7">
        <div className="flex flex-col gap-6 lg:flex-row lg:items-end lg:justify-between">
          <div className="min-w-0">
            <p className="mb-2 text-xs font-bold uppercase tracking-[0.18em] text-primary">Attendance / Records</p>
            <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">OD Records</h1>
            <p className="mt-2 max-w-2xl text-sm leading-6 text-foreground-muted sm:text-base">Find attendance records, make a precise correction, or review the audit trail without losing context.</p>
          </div>
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground-muted">
            <span className="grid h-9 w-9 place-items-center rounded-xl bg-accent text-primary"><Funnel size={18} weight="duotone" aria-hidden="true" /></span>
            {eventID ? 'Event selected' : 'Choose an event to begin'}
          </div>
        </div>
      </header>

      <section className="mb-8 grid grid-cols-1 gap-3 sm:grid-cols-3" aria-label="Record workspace summary">
        <SummaryCard icon={<Archive size={20} weight="duotone" aria-hidden="true" />} label="Records found" value={records.data?.total ?? '—'} />
        <SummaryCard icon={<Funnel size={20} weight="duotone" aria-hidden="true" />} label="Active filters" value={[categoryID, slot, odDate, section, actorUserID].filter(Boolean).length} />
        <SummaryCard icon={<ClockCounterClockwise size={20} weight="duotone" aria-hidden="true" />} label="History lookup" value={historyID ? 'Ready' : 'Optional'} />
      </section>

      <section className="mb-8 rounded-2xl border border-border bg-surface p-5 shadow-sm sm:p-6" aria-labelledby="record-filters-title">
        <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <p className="text-xs font-bold uppercase tracking-wider text-primary">Workspace controls</p>
            <h2 id="record-filters-title" className="mt-1 text-lg font-bold tracking-tight">Filter records</h2>
          </div>
          <Button variant="ghost" size="sm" onClick={() => { setParams({}); setCategoryID(''); setSlot(''); setODDate(''); setSection(''); setActorUserID('') }}>
            <ArrowClockwise size={16} aria-hidden="true" /> Reset filters
          </Button>
        </div>
        <div className="grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <StyledSelect id="record-event" value={eventID} label="Event" onChange={(v) => setParams(v ? { event_id: v } : {})} options={[{ value: '', label: 'Choose an event' }, ...eventOptions.map((event) => ({ value: event.id, label: event.name }))]} />

          <StyledSelect id="record-category" value={categoryID} label="Category" onChange={setCategoryID} options={[{ value: '', label: 'All categories' }, ...categoryOptions.map((category) => ({ value: category.id, label: category.name }))]} />

          <StyledSelect id="record-slot" value={slot} label="Slot" onChange={setSlot} options={[{ value: '', label: 'All slots' }, ...slots.map((number) => ({ value: String(number), label: String(number) }))]} />

          <div className="flex flex-col gap-1">
            <label htmlFor="record-day" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">OD Day</label>
            <input
              id="record-day"
              type="date"
              value={odDate}
              onChange={(e) => setODDate(e.target.value)}
              className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
            />
          </div>

          <StyledSelect id="record-section" value={section} label="Section" onChange={setSection} options={[{ value: '', label: 'All sections' }, ...sections.map((value) => ({ value, label: value }))]} />

          <StyledSelect id="record-actor" value={actorUserID} label="Coordinator / Marked by" onChange={setActorUserID} options={[{ value: '', label: 'Everyone' }, ...actors.map(([id, name]) => ({ value: id, label: name }))]} />
        </div>
      </section>

      {/* Error banner */}
      {error && (
        <div className="flex items-center gap-3 text-sm text-status-error-fg bg-status-error-bg p-4 rounded-[10px] border border-status-error-fg/20 mb-6" role="alert">
          {error instanceof ApiFailure ? error.body.message : error instanceof Error ? error.message : 'Records could not be updated.'}
        </div>
      )}

      {/* Loading */}
      {records.isPending && eventID !== '' && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading records…
        </div>
      )}

      {/* No event selected */}
      {eventID === '' && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3">
          <Archive size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">Select an event</h2>
          <p className="text-foreground-muted">Choose an event from the filters above to see its OD records.</p>
        </div>
      )}

      {/* Records table */}
      {records.data && eventID !== '' && (
        <div className="flex flex-col gap-4 mb-6">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <p className="text-xs font-bold uppercase tracking-wider text-foreground-muted">Current result set</p>
              <p className="mt-1 text-sm text-foreground-muted">Showing <span className="font-semibold text-foreground">{records.data.data.length}</span> of <span className="font-semibold text-foreground">{records.data.total}</span> records</p>
            </div>
            {records.isFetching && <span className="text-xs font-semibold text-status-info-fg">Refreshing…</span>}
          </div>

          {records.data.data.length === 0 ? (
            <div className="bg-surface border border-border rounded-[10px] p-10 text-center">
              <p className="text-foreground-muted">No records match the current filters.</p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-2xl border border-border bg-surface shadow-sm">
              <div className="hidden md:block">
                <table className="w-full text-left border-collapse">
                  <thead className="bg-canvas border-b border-border">
                    <tr>
                      {['Student', 'Category', 'Slot', 'Day', 'Status', 'Reason', 'Actions'].map((col) => (
                        <th key={col} className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                          {col}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {records.data.data.map((row) => (
                      <tr
                        key={row.record_id}
                        aria-label={`${row.student.display_name} slot ${row.slot}`}
                        className="hover:bg-canvas/50 transition-colors"
                      >
                        <td className="py-3 px-4">
                          <div className="flex flex-col">
                            <span className="text-sm font-semibold">{row.student.display_name}</span>
                            <span className="text-xs text-foreground-muted font-mono">{row.student.roll_no}</span>
                          </div>
                        </td>
                        <td className="py-3 px-4 text-sm">{row.category.name}</td>
                        <td className="py-3 px-4 text-center">
                          <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-foreground/10 text-xs font-bold">
                            {row.slot}
                          </span>
                        </td>
                        <td className="py-3 px-4 text-sm text-foreground-muted">{row.od_date}</td>
                        <td className="py-3 px-4">
                          <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold ${
                            (row.status as string) === 'removed'
                              ? 'bg-status-error-bg text-status-error-fg'
                              : (row.status as string) === 'corrected'
                                ? 'bg-status-warning-bg text-status-warning-fg'
                                : 'bg-status-success-bg text-status-success-fg'
                          }`}>
                            {row.status}
                          </span>
                        </td>
                        <td className="py-3 px-4 text-sm text-foreground-muted max-w-[150px] truncate">{row.reason_note ?? '—'}</td>
                        <td className="py-3 px-4">
                          <div className="flex items-center gap-2">
                            <button
                              type="button"
                              disabled={row.status === 'removed'}
                              onClick={() => openAction(row, 'correct')}
                              className="text-xs font-medium px-2.5 py-1.5 rounded-[10px] border border-border hover:bg-canvas active:scale-95 transition-all disabled:opacity-40 disabled:cursor-not-allowed"
                            >
                              Correct
                            </button>
                            <button
                              type="button"
                              disabled={row.status === 'removed'}
                              onClick={() => openAction(row, 'remove')}
                              className="text-xs font-medium px-2.5 py-1.5 rounded-[10px] border border-status-error-fg/30 text-status-error-fg hover:bg-status-error-bg active:scale-95 transition-all disabled:opacity-40 disabled:cursor-not-allowed"
                            >
                              Remove
                            </button>
                            <button
                              type="button"
                              onClick={() => setAuditRecordID(row.record_id)}
                              className="text-xs font-medium px-2.5 py-1.5 rounded-[10px] border border-border hover:bg-canvas active:scale-95 transition-all"
                            >
                              Audit
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="grid gap-3 p-3 md:hidden">
                {records.data.data.map((row) => (
                  <article key={row.record_id} className="rounded-xl border border-border bg-canvas p-4">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <h3 className="break-words text-sm font-bold">{row.student.display_name}</h3>
                        <p className="mt-1 break-all font-mono text-xs text-foreground-muted">{row.student.roll_no}</p>
                      </div>
                      <StatusBadge status={row.status as string} />
                    </div>
                    <dl className="mt-4 grid grid-cols-2 gap-3 border-t border-border pt-3 text-sm">
                      <div><dt className="text-xs text-foreground-muted">Category</dt><dd className="mt-1 font-semibold">{row.category.name}</dd></div>
                      <div><dt className="text-xs text-foreground-muted">Slot / day</dt><dd className="mt-1 font-semibold">{row.slot} · {row.od_date}</dd></div>
                    </dl>
                    {row.reason_note && <p className="mt-3 break-words text-xs leading-5 text-foreground-muted">{row.reason_note}</p>}
                    <div className="mt-4 grid grid-cols-3 gap-2 border-t border-border pt-3">
                      <Button variant="outline" size="sm" disabled={row.status === 'removed'} onClick={() => openAction(row, 'correct')}><NotePencil size={15} aria-hidden="true" /> Correct</Button>
                      <Button variant="outline" size="sm" disabled={row.status === 'removed'} onClick={() => openAction(row, 'remove')} className="text-status-error-fg"><Trash size={15} aria-hidden="true" /> Remove</Button>
                      <Button variant="outline" size="sm" onClick={() => setAuditRecordID(row.record_id)}><ClockCounterClockwise size={15} aria-hidden="true" /> Audit</Button>
                    </div>
                  </article>
                ))}
              </div>
            </div>
          )}

          {/* Pagination */}
          {records.data.total > records.data.page_size && (
            <nav className="flex items-center justify-between bg-surface border border-border rounded-[10px] px-4 py-3 shadow-sm" aria-label="Records pages">
              <button
                type="button"
                className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
                disabled={page <= 1}
                onClick={() => setPage((current) => current - 1)}
              >
                Previous
              </button>
              <span className="text-sm font-medium text-foreground-muted">
                Page {page} of {totalPages} · {records.data.total} records
              </span>
              <button
                type="button"
                className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
                disabled={page >= totalPages}
                onClick={() => setPage((current) => current + 1)}
              >
                Next
              </button>
            </nav>
          )}
        </div>
      )}

      {/* Student Record History */}
      <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm">
        <h2 className="text-lg font-bold tracking-tight mb-4">Student record history</h2>
        <div className="mb-4 flex flex-col items-stretch gap-3 sm:flex-row sm:items-end">
          <div className="flex min-w-0 flex-1 flex-col gap-1 sm:max-w-xs">
            <label htmlFor="student-history-id" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Student ID</label>
            <input
              id="student-history-id"
              value={studentID}
              onChange={(e) => setStudentID(e.target.value)}
              placeholder="CB.SC.U4CSE24001"
              className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
            />
          </div>
          <button
            type="button"
            onClick={() => setHistoryID(studentID.trim())}
            disabled={studentID.trim() === ''}
            className="inline-flex min-h-11 items-center justify-center gap-2 bg-foreground text-canvas px-4 py-2 rounded-[10px] text-sm font-semibold active:scale-95 transition-all disabled:opacity-50 whitespace-nowrap sm:w-auto"
          >
            <MagnifyingGlass size={16} aria-hidden="true" /> View history
          </button>
        </div>

        {history.data && (
          <div className="flex flex-col gap-2">
            {history.data.data.length === 0 ? (
              <p className="text-sm text-foreground-muted italic">No records found for this student.</p>
            ) : (
              <ul className="flex flex-col divide-y divide-border">
                {history.data.data.map((row) => (
                  <li key={row.record_id} className="py-3 flex items-center justify-between">
                    <div>
                      <span className="text-sm font-medium">{row.od_date}</span>
                      <span className="text-sm text-foreground-muted"> · {row.category.name}, slot {row.slot}</span>
                    </div>
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold ${
                      row.status === 'removed'
                        ? 'bg-status-error-bg text-status-error-fg'
                        : 'bg-status-success-bg text-status-success-fg'
                    }`}>
                      {row.status}
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {history.data.total > history.data.page_size && (
              <nav className="flex items-center justify-between mt-3" aria-label="History pages">
                <button type="button" disabled={historyPage <= 1} onClick={() => setHistoryPage((c) => c - 1)} className="text-sm font-medium px-3 py-1.5 rounded-[10px] hover:bg-canvas border border-border active:scale-95 transition-all disabled:opacity-50">Previous</button>
                <span className="text-xs text-foreground-muted">Page {historyPage} of {historyTotalPages}</span>
                <button type="button" disabled={historyPage >= historyTotalPages} onClick={() => setHistoryPage((c) => c + 1)} className="text-sm font-medium px-3 py-1.5 rounded-[10px] hover:bg-canvas border border-border active:scale-95 transition-all disabled:opacity-50">Next</button>
              </nav>
            )}
          </div>
        )}
      </section>

      {/* Correct / Remove Modal */}
      <AnimatePresence>
        {selected && mode && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-foreground/20 backdrop-blur-sm overflow-y-auto"
          >
            <motion.div
              initial={{ opacity: 0, y: 20, scale: 0.95 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: 20, scale: 0.95 }}
              transition={{ type: 'spring', damping: 25, stiffness: 300 }}
              className="w-full max-w-lg bg-surface border border-border rounded-[10px] shadow-xl p-6 my-auto"
              role="dialog"
              aria-modal="true"
              aria-label={mode === 'correct' ? 'Correct record' : 'Remove record'}
            >
              <h2 className="text-xl font-bold tracking-tight mb-1">{mode === 'correct' ? 'Correct record' : 'Remove record'}</h2>
              <p className="text-sm text-foreground-muted mb-6">
                {mode === 'correct' ? 'Update fields and provide a reason for the correction.' : 'Permanently remove this OD record with a reason.'}
              </p>
              <form onSubmit={(e) => { e.preventDefault(); mutate.mutate() }} className="flex flex-col gap-4">
                {mode === 'correct' && (
                  <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
                    <StyledSelect id="correction-category" value={correctionCategoryID} label="Category" onChange={setCorrectionCategoryID} options={categoryOptions.map((category) => ({ value: category.id, label: category.name }))} />
                    <StyledSelect id="correction-slot" value={correctionSlot} label="Slot" onChange={setCorrectionSlot} options={slots.map((number) => ({ value: String(number), label: String(number) }))} />
                    <div className="flex flex-col gap-1">
                      <label htmlFor="correction-day" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">OD Day</label>
                      <input
                        id="correction-day"
                        type="date"
                        value={correctionDate}
                        onChange={(e) => setCorrectionDate(e.target.value)}
                        className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
                      />
                    </div>
                  </div>
                )}
                <div className="flex flex-col gap-1">
                  <label htmlFor="record-reason" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Reason note <span className="text-status-error-fg">*</span></label>
                  <textarea
                    id="record-reason"
                    required
                    maxLength={2000}
                    value={reasonNote}
                    onChange={(e) => setReasonNote(e.target.value)}
                    rows={3}
                    placeholder="Describe why this record is being changed…"
                    className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all resize-none"
                  />
                </div>
                <div className="flex flex-col-reverse sm:flex-row gap-3 pt-2 border-t border-border">
                  <button
                    type="button"
                    onClick={() => { setSelected(null); setMode(null) }}
                    className="flex-1 border border-border bg-canvas hover:bg-surface text-foreground px-4 py-2.5 rounded-[10px] font-medium active:scale-95 transition-all"
                  >
                    Cancel
                  </button>
                  <button
                    type="submit"
                    disabled={reasonNote.trim() === '' || mutate.isPending}
                    className={`flex-1 px-4 py-2.5 rounded-[10px] font-semibold active:scale-95 transition-all disabled:opacity-50 ${
                      mode === 'remove'
                        ? 'bg-status-error-fg text-white'
                        : 'bg-foreground text-canvas'
                    }`}
                  >
                    {mutate.isPending
                      ? (mode === 'correct' ? 'Saving…' : 'Removing…')
                      : (mode === 'correct' ? 'Save correction' : 'Confirm remove')}
                  </button>
                </div>
              </form>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Audit Modal */}
      <AnimatePresence>
        {auditRecordID && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-foreground/20 backdrop-blur-sm overflow-y-auto"
          >
            <motion.div
              initial={{ opacity: 0, y: 20, scale: 0.95 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: 20, scale: 0.95 }}
              transition={{ type: 'spring', damping: 25, stiffness: 300 }}
              className="w-full max-w-lg bg-surface border border-border rounded-[10px] shadow-xl p-6 my-auto"
              role="dialog"
              aria-modal="true"
              aria-label="Record audit"
            >
              <div className="flex items-center justify-between mb-4">
                <h2 className="text-xl font-bold tracking-tight">Record audit</h2>
                <button
                  type="button"
                  onClick={() => setAuditRecordID('')}
                  className="text-sm font-medium px-3 py-1.5 rounded-[10px] border border-border hover:bg-canvas active:scale-95 transition-all"
                >
                  Close
                </button>
              </div>
              {audit.isPending && (
                <div className="flex items-center gap-2 text-sm text-foreground-muted" role="status">
                  <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
                  Loading audit…
                </div>
              )}
              {audit.data && (
                <ul className="flex flex-col divide-y divide-border">
                  {audit.data.data.map((entry) => (
                    <li key={`${entry.action}-${entry.occurred_at}`} className="py-3 text-sm">
                      <div className="flex items-center justify-between mb-1">
                        <span className="font-semibold">{entry.action}</span>
                        <span className="text-xs text-foreground-muted">{entry.occurred_at}</span>
                      </div>
                      <p className="text-xs text-foreground-muted">by {entry.actor_email ?? entry.actor_role ?? 'system'}</p>
                      {entry.payload && Object.keys(entry.payload).length > 0 && (
                        <pre className="text-xs bg-canvas rounded-[10px] p-2 mt-2 border border-border overflow-x-auto">{JSON.stringify(entry.payload, null, 2)}</pre>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </PortalShell>
  )
}

function SummaryCard({ icon, label, value }: { icon: ReactNode; label: string; value: ReactNode }) {
  return (
    <div className="flex items-center gap-3 rounded-2xl border border-border bg-surface p-4 shadow-sm">
      <span className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-accent text-primary">{icon}</span>
      <div className="min-w-0">
        <p className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">{label}</p>
        <p className="mt-1 text-xl font-bold tracking-tight">{value}</p>
      </div>
    </div>
  )
}
