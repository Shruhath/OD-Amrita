import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Branch, CreateEventInput, EventRecord, ValidationError } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { formatDate, messageOf, titleCase } from './format'

const branchKey = ['admin', 'branches'] as const
const eventKeyRoot = ['admin', 'events', 'all'] as const
const pageSize = 20
const eventKey = (page: number) => [...eventKeyRoot, page, pageSize] as const

export default function EventsPortal() {
  const { api, identity, signOut } = useAuth()
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
    <div className="portal-shell">
      <header className="topbar">
        <div>
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">OD-Amrita</span>
        </div>
        <nav className="portal-nav" aria-label="Administration sections">
          <Link to="/portal/events" aria-current="page">Events</Link>
          <Link to="/portal/teachers">Teachers</Link>
          <Link to="/portal/students">Students</Link>
        </nav>
        <div className="account-actions">
          <span>{identity?.name ?? identity?.email}</span>
          <button className="text-button" type="button" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>

      <main className="workspace">
        <div className="page-heading">
          <div>
            <p className="eyebrow">Administration · Events</p>
            <h1>Events</h1>
            <p>Plan university events across branches and retain a clear historical record.</p>
          </div>
          <button className="primary" type="button" disabled={!canCreate} onClick={() => setCreating(true)}>
            New event
          </button>
        </div>

        {branches.isPending && <p role="status" aria-label="Loading branches">Loading branches…</p>}
        {events.isPending && <p role="status" aria-label="Loading events">Loading events…</p>}

        {branches.isError && (
          <ErrorState message={messageOf(branches.error, 'Branches could not be loaded')} retry={() => void branches.refetch()} />
        )}
        {events.isError && (
          <ErrorState message={messageOf(events.error, 'Events could not be loaded')} retry={() => void events.refetch()} />
        )}

        {branches.isSuccess && branchData.length === 0 && (
          <section className="empty-state">
            <h2>No branches available</h2>
            <p>Branches must be configured before an event can be created.</p>
          </section>
        )}

        {branches.isSuccess && events.isSuccess && branchData.length > 0 && eventData.length === 0 && (
          <section className="empty-state">
            <h2>No events yet</h2>
            <p>Create the first event to begin the calendar.</p>
          </section>
        )}

        {events.isSuccess && eventData.length > 0 && (
          <>
            <div className="table-wrap">
              <table>
                <caption className="sr-only">All active and archived events</caption>
                <thead><tr><th>Event</th><th>Date</th><th>Branches</th><th>Status</th><th><span className="sr-only">Actions</span></th></tr></thead>
                <tbody>
                  {eventData.map((event) => (
                    <tr key={event.id} className={event.status === 'archived' ? 'archived-row' : undefined}>
                      <th scope="row"><Link to={`/portal/events/${event.id}`}>{event.name}</Link></th>
                      <td>{formatDate(event.event_date)}</td>
                      <td><div className="branch-list">{event.branches.map((branch) => <span className="branch-chip" key={branch.id}>{branch.name} <small>{branch.code}</small></span>)}</div></td>
                      <td><span className={`status-badge ${event.status}`}><span aria-hidden="true">{event.status === 'active' ? '●' : '✓'}</span><span>{titleCase(event.status)}</span></span></td>
                      <td className="row-action">
                        {event.status === 'active' && (
                          <button type="button" className="secondary" onClick={() => { setArchiveError(null); setArchiveTarget(event) }}>Archive</button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <nav className="pagination" aria-label="Events pages">
              <button type="button" disabled={page === 1 || events.isFetching} onClick={() => setPage((current) => Math.max(1, current - 1))}>Previous</button>
              <span aria-live="polite">Page {page} of {totalPages}</span>
              <button type="button" disabled={page >= totalPages || events.isFetching} onClick={() => setPage((current) => Math.min(totalPages, current + 1))}>Next</button>
            </nav>
          </>
        )}

        {archiveError && <p className="notice error" role="alert">{archiveError}</p>}
      </main>

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
        <Dialog title={`Archive ${archiveTarget.name}`} onClose={() => !archive.isPending && setArchiveTarget(null)} initialFocus="cancel">
          <p>This preserves <strong>{archiveTarget.name}</strong> as read-only history. It cannot be reactivated.</p>
          <div className="dialog-actions">
            <button type="button" data-dialog-cancel disabled={archive.isPending} onClick={() => setArchiveTarget(null)}>Cancel</button>
            <button className="danger" type="button" disabled={archive.isPending} onClick={() => archive.mutate(archiveTarget.id)}>Archive event</button>
          </div>
          {archive.isPending && <p role="status">Archiving…</p>}
        </Dialog>
      )}
    </div>
  )
}

function CreateDialog({ branches, onClose, onCreated }: { branches: Branch[]; onClose(): void; onCreated(event: EventRecord): void }) {
  const { api } = useAuth()
  const [name, setName] = useState('')
  const [date, setDate] = useState('')
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
    if (selected.length === 0) errors.branch_ids = 'Select at least one branch'
    setFieldErrors(errors)
    setFormError(null)
    if (Object.keys(errors).length > 0) return

    create.mutate({ name: name.trim(), event_date: date, branch_ids: selected }, {
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
    <Dialog title="New event" onClose={() => !create.isPending && onClose()} initialFocus="first">
      <form onSubmit={submit} noValidate>
        <label htmlFor="event-name">Event name</label>
        <input id="event-name" value={name} onChange={(event) => setName(event.target.value)} aria-invalid={Boolean(fieldErrors.name)} aria-describedby={fieldErrors.name ? 'event-name-error' : undefined} />
        {fieldErrors.name && <p className="field-error" id="event-name-error">{fieldErrors.name}</p>}

        <label htmlFor="event-date">Event date</label>
        <input id="event-date" type="date" value={date} onChange={(event) => setDate(event.target.value)} aria-invalid={Boolean(fieldErrors.event_date)} aria-describedby={fieldErrors.event_date ? 'event-date-error' : undefined} />
        {fieldErrors.event_date && <p className="field-error" id="event-date-error">{fieldErrors.event_date}</p>}

        <fieldset aria-describedby={fieldErrors.branch_ids ? 'branch-error' : undefined}>
          <legend>Branches</legend>
          <div className="branch-options">
            {branches.map((branch) => (
              <label key={branch.id}>
                <input
                  type="checkbox"
                  checked={selected.includes(branch.id)}
                  onChange={() => toggle(branch.id)}
                  onKeyDown={(event: KeyboardEvent<HTMLInputElement>) => {
                    if (event.key === ' ') { event.preventDefault(); toggle(branch.id) }
                  }}
                />
                <span><strong>{branch.name}</strong><small>{branch.code}</small></span>
              </label>
            ))}
          </div>
        </fieldset>
        {fieldErrors.branch_ids && <p className="field-error" id="branch-error">{fieldErrors.branch_ids}</p>}
        {formError && <p className="notice error" role="alert">{formError}</p>}

        <div className="dialog-actions">
          <button type="button" data-dialog-cancel disabled={create.isPending} onClick={onClose}>Cancel</button>
          <button className="primary" type="submit" disabled={!canSubmit}>{create.isPending ? 'Creating…' : 'Create event'}</button>
        </div>
      </form>
    </Dialog>
  )
}

function Dialog({ title, onClose, initialFocus, children }: { title: string; onClose(): void; initialFocus: 'first' | 'cancel'; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const onCloseRef = useRef(onClose)
  useEffect(() => { onCloseRef.current = onClose }, [onClose])
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    const node = ref.current
    if (!node) return
    const focusable = () => Array.from(node.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled])'))
    const target = initialFocus === 'cancel' ? node.querySelector<HTMLElement>('[data-dialog-cancel]') : focusable()[0]
    target?.focus()
    const keydown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); onCloseRef.current(); return }
      if (event.key !== 'Tab') return
      const items = focusable()
      if (items.length === 0) return
      const current = items.indexOf(document.activeElement as HTMLElement)
      const next = event.shiftKey ? (current <= 0 ? items.length - 1 : current - 1) : (current >= items.length - 1 ? 0 : current + 1)
      event.preventDefault()
      items[next]?.focus()
    }
    document.addEventListener('keydown', keydown)
    return () => { document.removeEventListener('keydown', keydown); previous?.focus() }
  }, [initialFocus])

  return <div className="dialog-backdrop"><div className="dialog" ref={ref} role="dialog" aria-modal="true" aria-labelledby="dialog-title"><h2 id="dialog-title">{title}</h2>{children}</div></div>
}

function ErrorState({ message, retry }: { message: string; retry(): void }) {
  return <div className="notice error" role="alert"><span>{message}</span><button type="button" onClick={retry}>Try again</button></div>
}

function orderedEvents(events: EventRecord[]) {
  return [...events].sort((left, right) =>
    right.event_date.localeCompare(left.event_date)
    || right.created_at.localeCompare(left.created_at)
    || right.id.localeCompare(left.id))
}
