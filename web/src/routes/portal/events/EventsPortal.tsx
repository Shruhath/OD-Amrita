import { useState, type FormEvent, type KeyboardEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Branch, CreateEventInput, EventRecord, ValidationError } from '../../../api/client'
import { BranchChip } from '../../../components/BranchChip'
import { PortalShell } from '../../../components/PortalShell'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { formatDateRange, messageOf, titleCase } from './format'
import { Dialog as ShadcnDialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../../../components/ui/dialog'

const branchKey = ['admin', 'branches'] as const
const eventKeyRoot = ['admin', 'events', 'all'] as const
const pageSize = 20
const eventKey = (page: number) => [...eventKeyRoot, page, pageSize] as const

export default function EventsPortal() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [page, setPage] = useState(1)
  const [archiveTarget, setArchiveTarget] = useState<EventRecord | null>(null)
  const [archiveError, setArchiveError] = useState<string | null>(null)

  const branches = useQuery({ queryKey: branchKey, queryFn: api.branches })
  const currentEventKey = eventKey(page)
  const events = useQuery({ queryKey: currentEventKey, queryFn: () => api.events(page, pageSize) })
  const archive = useMutation({
    mutationFn: (eventID: string) => api.archive(eventID),
    onSuccess: (archived) => {
      queryClient.setQueryData(currentEventKey, (current: Awaited<ReturnType<typeof api.events>> | undefined) =>
        current ? { ...current, data: current.data.map((item) => item.id === archived.id ? archived : item) } : current)
      setArchiveTarget(null)
      setArchiveError(null)
    },
    onError: (cause) => setArchiveError(messageOf(cause, 'The event could not be archived')),
  })

  const branchData = branches.data?.data ?? []
  const eventData = events.data?.data ?? []
  const canCreate = branches.isSuccess && branchData.length > 0
  const totalPages = Math.max(1, Math.ceil((events.data?.total ?? 0) / (events.data?.page_size ?? pageSize)))

  return (
    <PortalShell>
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-8">
        <div>
          <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Administration · Events</p>
          <h1 className="text-3xl font-bold tracking-tight mb-2">Events</h1>
          <p className="text-foreground-muted">Plan university events across branches and retain a clear historical record.</p>
        </div>
        <button 
          className="bg-foreground text-canvas px-6 py-3 rounded-xl font-medium active:scale-95 transition-transform disabled:opacity-50 disabled:active:scale-100" 
          type="button" 
          disabled={!canCreate} 
          onClick={() => setCreating(true)}
        >
          New event
        </button>
      </div>

      {branches.isPending && <p className="text-sm text-status-info-fg bg-status-info-bg p-4 rounded-xl flex items-center gap-2 mb-6" role="status"><span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" /> Loading branches…</p>}
      {events.isPending && <p className="text-sm text-status-info-fg bg-status-info-bg p-4 rounded-xl flex items-center gap-2 mb-6" role="status"><span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" /> Loading events…</p>}

      {branches.isError && (
        <ErrorState message={messageOf(branches.error, 'Branches could not be loaded')} retry={() => void branches.refetch()} />
      )}
      {events.isError && (
        <ErrorState message={messageOf(events.error, 'Events could not be loaded')} retry={() => void events.refetch()} />
      )}

      {branches.isSuccess && branchData.length === 0 && (
        <section className="bg-surface border border-border p-8 rounded-2xl flex flex-col items-center text-center gap-2 mb-8">
          <h2 className="text-xl font-bold tracking-tight">No branches available</h2>
          <p className="text-foreground-muted">Branches must be configured before an event can be created.</p>
        </section>
      )}

      {branches.isSuccess && events.isSuccess && branchData.length > 0 && eventData.length === 0 && (
        <section className="bg-surface border border-border p-8 rounded-2xl flex flex-col items-center text-center gap-2 mb-8">
          <h2 className="text-xl font-bold tracking-tight">No events yet</h2>
          <p className="text-foreground-muted">Create the first event to begin the calendar.</p>
        </section>
      )}

      {events.isSuccess && eventData.length > 0 && (
        <div className="flex flex-col gap-6">
          <div className="flex flex-col gap-3" role="table" aria-label="Events">
            <div role="row" className="sr-only">
              <span role="columnheader">Event</span>
              <span role="columnheader">Status</span>
              <span role="columnheader">Date</span>
              <span role="columnheader">Branches</span>
              <span role="columnheader">Actions</span>
            </div>
            {eventData.map((event) => {
              const isActive = event.status === 'active'
              return (
                <div 
                  key={event.id}
                  role="row"
                  className={`bg-surface border border-border rounded-2xl p-5 shadow-sm transition-all hover:border-foreground/20 hover:shadow-md flex flex-col md:flex-row md:items-center justify-between gap-4 ${!isActive ? 'opacity-70' : ''}`}
                >
                  <div className="flex flex-col gap-2 min-w-0 flex-1">
                    <div className="flex items-center gap-3 flex-wrap">
                      <Link 
                        to={`/portal/events/${event.id}`}
                        className="font-bold text-lg text-foreground hover:underline underline-offset-4 decoration-border tracking-tight flex items-center gap-2"
                      >
                        <svg className="w-5 h-5 text-foreground-muted shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
                        </svg>
                        <span>{event.name}</span>
                      </Link>

                      <span className={`inline-flex items-center gap-1.5 px-3 py-0.5 rounded-full text-xs font-semibold tracking-wide border ${
                        isActive 
                          ? 'bg-status-success-bg/80 text-status-success-fg border-status-success-fg/30' 
                          : 'bg-canvas text-foreground-muted border-border'
                      }`}>
                        {isActive ? (
                          <span className="w-2 h-2 rounded-full bg-status-success-fg animate-pulse shrink-0" />
                        ) : (
                          <svg className="w-3 h-3 text-foreground-muted shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2.5">
                            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                          </svg>
                        )}
                        <span>{titleCase(event.status)}</span>
                      </span>
                    </div>

                    <div className="flex items-center gap-4 text-xs font-medium text-foreground-muted flex-wrap">
                      <div className="inline-flex items-center gap-1.5">
                        <svg className="w-4 h-4 opacity-70 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
                        </svg>
                        <span>{formatDateRange(event.event_date, event.end_date)}</span>
                      </div>

                      <div className="inline-flex items-center gap-1.5">
                        <span className="text-foreground-muted/60">•</span>
                        <div className="flex flex-wrap gap-1">
                          {event.branches.map((branch) => <BranchChip branch={branch} key={branch.id} />)}
                        </div>
                      </div>
                    </div>
                  </div>

                  <div className="flex items-center gap-2 pt-2 md:pt-0 border-t md:border-t-0 border-border/60 justify-end">
                    <Link 
                      to={`/portal/events/${event.id}`}
                      className="inline-flex items-center gap-1.5 text-xs font-semibold px-4 py-2 rounded-xl border border-border bg-canvas hover:bg-surface active:scale-95 transition-all text-foreground"
                    >
                      <span>Manage Event</span>
                      <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                        <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                      </svg>
                    </Link>
                    {isActive && (
                      <button 
                        type="button" 
                        className="inline-flex items-center gap-1.5 text-xs font-semibold px-3 py-2 rounded-xl border border-border/80 hover:border-status-error-fg/40 hover:bg-status-error-bg/20 hover:text-status-error-fg active:scale-95 transition-all"
                        onClick={() => { setArchiveError(null); setArchiveTarget(event) }}
                      >
                        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                          <path strokeLinecap="round" strokeLinejoin="round" d="M5 8h14M5 8a2 2 0 012-2h10a2 2 0 012 2m-14 0v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4" />
                        </svg>
                        <span>Archive</span>
                      </button>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
          
          <nav className="flex items-center justify-between bg-surface border border-border rounded-2xl px-4 py-3 shadow-sm" aria-label="Events pages">
            <button 
              type="button" 
              className="inline-flex items-center gap-1.5 text-sm font-medium px-4 py-2 rounded-xl hover:bg-canvas active:scale-95 transition-all disabled:opacity-40 disabled:pointer-events-none" 
              disabled={page === 1 || events.isFetching} 
              onClick={() => setPage((current) => Math.max(1, current - 1))}
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15 19l-7-7 7-7" />
              </svg>
              <span>Previous</span>
            </button>
            <span aria-live="polite" className="text-sm font-medium text-foreground-muted">Page {page} of {totalPages}</span>
            <button 
              type="button" 
              className="inline-flex items-center gap-1.5 text-sm font-medium px-4 py-2 rounded-xl hover:bg-canvas active:scale-95 transition-all disabled:opacity-40 disabled:pointer-events-none" 
              disabled={page >= totalPages || events.isFetching} 
              onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
            >
              <span>Next</span>
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
              </svg>
            </button>
          </nav>
        </div>
      )}

      {archiveError && <p className="mt-6 text-sm text-status-error-fg bg-status-error-bg p-4 rounded-xl border border-status-error-fg/20" role="alert">{archiveError}</p>}

      {creating && (
        <CreateDialog
          branches={branchData}
          onClose={() => setCreating(false)}
          onCreated={(created) => {
            const firstPageKey = eventKey(1)
            queryClient.setQueryData(firstPageKey, (current: Awaited<ReturnType<typeof api.events>> | undefined) => current
              ? { ...current, data: orderedEvents([created, ...current.data]).slice(0, current.page_size), total: current.total + 1 }
              : { data: [created], page: 1, page_size: 20, total: 1 })
            void queryClient.invalidateQueries({ queryKey: eventKeyRoot })
            setPage(1)
            setCreating(false)
          }}
        />
      )}
      {archiveTarget && (
        <ShadcnDialog open onOpenChange={(open) => { if (!open && !archive.isPending) setArchiveTarget(null) }}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Archive {archiveTarget.name}</DialogTitle>
              <DialogDescription>This preserves <strong>{archiveTarget.name}</strong> as read-only history. It cannot be reactivated.</DialogDescription>
            </DialogHeader>
          <div className="dialog-actions">
            <button type="button" data-dialog-cancel disabled={archive.isPending} onClick={() => setArchiveTarget(null)}>Cancel</button>
            <button className="danger" type="button" disabled={archive.isPending} onClick={() => archive.mutate(archiveTarget.id)}>Archive event</button>
          </div>
          {archive.isPending && <p role="status">Archiving…</p>}
          </DialogContent>
        </ShadcnDialog>
      )}
    </PortalShell>
  )
}

function CreateDialog({ branches, onClose, onCreated }: { branches: Branch[]; onClose(): void; onCreated(event: EventRecord): void }) {
  const { api } = useAuth()
  const [name, setName] = useState('')
  const [date, setDate] = useState('')
  // The end date FOLLOWS the start until the admin moves it themselves. Making
  // it a second required control would cost every ordinary one-day event two
  // extra clicks for a range almost none of them use.
  const [endDate, setEndDate] = useState('')
  const [endDateTouched, setEndDateTouched] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string | null>(null)
  const create = useMutation({ mutationFn: (input: CreateEventInput) => api.create(input), onSuccess: onCreated })
  const canSubmit = Boolean(name.trim() && date && selected.length > 0) && !create.isPending

  const toggle = (id: string) => setSelected((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const errors: Record<string, string> = {}
    if (!name.trim()) errors.name = 'Enter an event name'
    if (!date) errors.event_date = 'Choose an event date'
    if (endDate && endDate < date) errors.end_date = 'The end date cannot be before the start date'
    if (selected.length === 0) errors.branch_ids = 'Select at least one branch'
    setFieldErrors(errors)
    setFormError(null)
    if (Object.keys(errors).length > 0) return

    // Always sent, because the control always shows a value the admin can see.
    // Omitting it would let the server default silently to a day that differs
    // from the one on screen.
    create.mutate({ name: name.trim(), event_date: date, end_date: endDate || date, branch_ids: selected }, {
      onError(cause) {
        setFormError(messageOf(cause, 'The event could not be created'))
        if (cause instanceof ApiFailure && cause.body.code === 'VALIDATION_FAILED' && 'details' in cause.body && Array.isArray(cause.body.details)) {
          const mapped: Record<string, string> = {}
          for (const detail of (cause.body as ValidationError).details) mapped[detail.field.replace(/\[\d+\]$/, '')] = detail.message
          setFieldErrors(mapped)
        }
      },
    })
  }

  return (
    <ShadcnDialog open onOpenChange={(open) => { if (!open && !create.isPending) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New event</DialogTitle>
          <DialogDescription>Create an event and assign it to one or more branches.</DialogDescription>
        </DialogHeader>
      <form onSubmit={submit} noValidate className="flex flex-col gap-6">
        <div className="flex flex-col gap-2">
          <label htmlFor="event-name" className="font-semibold text-sm">Event name</label>
          <input 
            id="event-name" 
            className={`w-full bg-canvas border focus:ring-1 rounded-xl p-3 outline-none transition-all ${fieldErrors.name ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
            value={name} 
            onChange={(event) => setName(event.target.value)} 
            aria-invalid={Boolean(fieldErrors.name)} 
            aria-describedby={fieldErrors.name ? 'event-name-error' : undefined} 
          />
          {fieldErrors.name && <p className="text-sm font-medium text-status-error-fg mt-1" id="event-name-error">{fieldErrors.name}</p>}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          <div className="flex flex-col gap-2">
            <label htmlFor="event-date" className="font-semibold text-sm">Start date</label>
            <input
              id="event-date"
              className={`w-full bg-canvas border focus:ring-1 rounded-xl p-3 outline-none transition-all ${fieldErrors.event_date ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
              type="date"
              value={date}
              onChange={(event) => {
                setDate(event.target.value)
                if (!endDateTouched) setEndDate(event.target.value)
              }}
              aria-invalid={Boolean(fieldErrors.event_date)}
              aria-describedby={fieldErrors.event_date ? 'event-date-error' : undefined}
            />
            {fieldErrors.event_date && <p className="text-sm font-medium text-status-error-fg mt-1" id="event-date-error">{fieldErrors.event_date}</p>}
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="event-end-date" className="font-semibold text-sm">End date</label>
            <input
              id="event-end-date"
              className={`w-full bg-canvas border focus:ring-1 rounded-xl p-3 outline-none transition-all ${fieldErrors.end_date ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
              type="date"
              value={endDate}
              min={date || undefined}
              onChange={(event) => {
                setEndDateTouched(true)
                setEndDate(event.target.value)
              }}
              aria-invalid={Boolean(fieldErrors.end_date)}
              aria-describedby={fieldErrors.end_date ? 'event-end-date-error' : 'event-end-date-hint'}
            />
            {fieldErrors.end_date
              ? <p className="text-sm font-medium text-status-error-fg mt-1" id="event-end-date-error">{fieldErrors.end_date}</p>
              : <p className="text-xs text-foreground-muted mt-1" id="event-end-date-hint">Leave it on the start date for a single-day event.</p>}
          </div>
        </div>

        <fieldset aria-describedby={fieldErrors.branch_ids ? 'branch-error' : undefined} className="flex flex-col gap-3">
          <legend className="font-semibold text-sm">Branches</legend>
          <div className="flex flex-wrap gap-2">
            {branches.map((branch) => (
              <label 
                key={branch.id} 
                title={branch.name}
                className={`relative flex items-center justify-center min-w-16 px-3 py-2 rounded-xl cursor-pointer border select-none transition-all active:scale-95 ${selected.includes(branch.id) ? 'bg-foreground text-canvas border-foreground font-bold' : 'bg-canvas text-foreground border-border hover:bg-surface font-medium'}`}
              >
                <input
                  type="checkbox"
                  className="absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0"
                  checked={selected.includes(branch.id)}
                  onChange={() => toggle(branch.id)}
                  onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                    if (event.key === ' ') { event.preventDefault(); toggle(branch.id) }
                  }}
                />
                <span className="pointer-events-none">{branch.code}<span className="sr-only">{branch.name}</span></span>
              </label>
            ))}
          </div>
          {fieldErrors.branch_ids && <p className="text-sm font-medium text-status-error-fg mt-1" id="branch-error">{fieldErrors.branch_ids}</p>}
        </fieldset>
        {formError && <p className="text-sm text-status-error-fg bg-status-error-bg p-4 rounded-xl border border-status-error-fg/20" role="alert">{formError}</p>}

        <div className="flex flex-col sm:flex-row justify-end gap-3 mt-4 pt-6 border-t border-border">
          <button 
            type="button" 
            className="border border-border bg-canvas hover:bg-surface text-foreground px-6 py-2.5 rounded-xl font-medium active:scale-95 transition-all" 
            data-dialog-cancel 
            disabled={create.isPending} 
            onClick={onClose}
          >
            Cancel
          </button>
          <button 
            type="submit" 
            className="bg-foreground text-canvas px-6 py-2.5 rounded-xl font-medium active:scale-95 transition-transform disabled:opacity-50 disabled:active:scale-100" 
            disabled={!canSubmit}
          >
            {create.isPending ? 'Creating…' : 'Create event'}
          </button>
        </div>
      </form>
      </DialogContent>
    </ShadcnDialog>
  )
}

function ErrorState({ message, retry }: { message: string; retry(): void }) {
  return (
    <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-xl border border-status-error-fg/20 mb-6" role="alert">
      <span className="font-medium text-sm">{message}</span>
      <button 
        type="button" 
        onClick={retry}
        className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-lg border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
      >
        Try again
      </button>
    </div>
  )
}

function orderedEvents(events: EventRecord[]) {
  return [...events].sort((left, right) =>
    right.event_date.localeCompare(left.event_date)
    || right.created_at.localeCompare(left.created_at)
    || right.id.localeCompare(left.id))
}
