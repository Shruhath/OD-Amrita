import { useRef, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { CategoryRecord, EventRecord, ValidationError } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { formatDate, messageOf, titleCase } from './format'

// The event itself is read from the admin events collection: the contract
// deliberately exposes no per-event GET, and adding one was outside this
// change's ratified surface. A generous page size keeps the lookup accurate for
// the calendar sizes this portal serves; a dedicated read is the right fix when
// the event count outgrows it.
const eventLookupPageSize = 100
const eventLookupKey = ['admin', 'events', 'all', 1, eventLookupPageSize] as const
const categoryKey = (eventID: string) => ['admin', 'events', eventID, 'categories'] as const

export default function EventDetailPortal() {
  const { event_id: eventID = '' } = useParams()
  const { api, identity, signOut } = useAuth()
  const queryClient = useQueryClient()
  const nameInput = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)

  const events = useQuery({
    queryKey: eventLookupKey,
    queryFn: () => api.events(1, eventLookupPageSize),
  })
  const currentKey = categoryKey(eventID)
  const categories = useQuery({
    queryKey: currentKey,
    queryFn: () => api.categories(eventID),
    enabled: Boolean(eventID),
  })

  const create = useMutation({
    mutationFn: (value: string) => api.createCategory(eventID, { name: value }),
    onSuccess: (created) => {
      // Written into the cache in the SERVER'S order, then revalidated. A
      // client that appended in submit order would disagree with the API about
      // what "first" means the moment the page reloads.
      queryClient.setQueryData(currentKey, (current: { data: CategoryRecord[] } | undefined) => ({
        data: orderedCategories([...(current?.data ?? []), created]),
      }))
      void queryClient.invalidateQueries({ queryKey: currentKey })
      setName('')
      setFieldError(null)
      setFormError(null)
      // Journey 5 adds several categories in a row, so the field returns to the
      // admin rather than making them re-aim at it each time.
      nameInput.current?.focus()
    },
    onError: (cause) => {
      const detail = fieldDetail(cause, 'name')
      if (detail) {
        // A field rejection belongs on the field. Announcing it as a page-level
        // alert would leave the input looking valid to a screen reader.
        setFieldError(detail)
        setFormError(null)
        return
      }
      setFieldError(null)
      setFormError(messageOf(cause, 'The category could not be added'))
    },
  })

  const event = events.data?.data.find((item) => item.id === eventID)
  const categoryData = categories.data?.data ?? []
  const canSubmit = name.trim().length > 0 && !create.isPending

  const submit = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    const trimmed = name.trim()
    if (!trimmed || create.isPending) return
    create.mutate(trimmed)
  }

  return (
    <div className="portal-shell">
      <header className="topbar">
        <div>
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">OD-Amrita</span>
        </div>
        <div className="account-actions">
          <span>{identity?.name ?? identity?.email}</span>
          <button className="text-button" type="button" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>

      <main className="workspace">
        <nav aria-label="Breadcrumb">
          <Link className="text-button" to="/portal/events">← All events</Link>
        </nav>

        {events.isPending && <p role="status" aria-label="Loading event">Loading event…</p>}
        {events.isError && (
          <ErrorState message={messageOf(events.error, 'The event could not be loaded')} retry={() => void events.refetch()} />
        )}
        {events.isSuccess && !event && (
          <section className="empty-state">
            <h1>Event not found</h1>
            <p>This event is no longer listed. Return to the events list to continue.</p>
          </section>
        )}

        {event && (
          <>
            <div className="page-heading">
              <div>
                <p className="eyebrow">Administration · Events</p>
                <h1>{event.name}</h1>
                <EventSummary event={event} />
              </div>
            </div>

            <section className="detail-section">
              <h2>Categories</h2>
              <p>Categories scope coordinators and OD records to a specific activity within this event.</p>

              {categories.isPending && <p role="status" aria-label="Loading categories">Loading categories…</p>}
              {categories.isError && (
                <ErrorState
                  message={messageOf(categories.error, 'Categories could not be loaded')}
                  retry={() => void categories.refetch()}
                />
              )}

              {categories.isSuccess && categoryData.length === 0 && (
                <p className="empty-note">No categories yet</p>
              )}
              {categoryData.length > 0 && (
                <ul className="category-list" aria-label="Categories">
                  {categoryData.map((category) => <li key={category.id}>{category.name}</li>)}
                </ul>
              )}

              {/* Absent, not disabled: an archived aggregate can never accept
                  this write again, so advertising the action would be a lie. */}
              {event.status === 'active' && (
                <form className="category-form" onSubmit={submit} noValidate>
                  <label htmlFor="category-name">Category name</label>
                  <input
                    id="category-name"
                    ref={nameInput}
                    value={name}
                    onChange={(changeEvent) => setName(changeEvent.target.value)}
                    aria-invalid={fieldError ? 'true' : undefined}
                    aria-describedby={fieldError ? 'category-name-error' : undefined}
                  />
                  {fieldError && <p className="field-error" id="category-name-error">{fieldError}</p>}
                  <button className="primary" type="submit" disabled={!canSubmit}>Add category</button>
                </form>
              )}
              {event.status === 'archived' && (
                <p className="empty-note">This event is archived. Its categories are read-only.</p>
              )}
              {formError && <p className="notice error" role="alert">{formError}</p>}
            </section>
          </>
        )}
      </main>
    </div>
  )
}

function EventSummary({ event }: { event: EventRecord }) {
  return (
    <dl className="event-summary">
      <div><dt>Date</dt><dd>{formatDate(event.event_date)}</dd></div>
      <div>
        <dt>Status</dt>
        <dd>
          <span className={`status-badge ${event.status}`}>
            <span aria-hidden="true">{event.status === 'active' ? '●' : '✓'}</span>
            <span>{titleCase(event.status)}</span>
          </span>
        </dd>
      </div>
      <div>
        <dt>Branches</dt>
        <dd>
          <div className="branch-list">
            {event.branches.map((branch) => (
              <span className="branch-chip" key={branch.id}>{branch.name} <small>{branch.code}</small></span>
            ))}
          </div>
        </dd>
      </div>
    </dl>
  )
}

function ErrorState({ message, retry }: { message: string; retry(): void }) {
  return <div className="notice error" role="alert"><span>{message}</span><button type="button" onClick={retry}>Try again</button></div>
}

/**
 * Returns the server's message for one field, or null when the rejection is not
 * about a field at all (EVENT_ARCHIVED, EVENT_NOT_FOUND). The server is
 * authoritative on duplicates — it compares case-insensitively after trimming,
 * which the client cannot reliably reproduce.
 */
function fieldDetail(cause: unknown, field: string) {
  if (!(cause instanceof ApiFailure)) return null
  if (cause.body.code !== 'VALIDATION_FAILED') return null
  if (!('details' in cause.body) || !Array.isArray(cause.body.details)) return null
  const match = (cause.body as ValidationError).details.find((detail) => detail.field === field)
  return match ? match.message : null
}

/** The server orders by name then id; the client mirrors it, never invents it. */
function orderedCategories(categories: CategoryRecord[]) {
  return [...categories].sort(
    (left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id),
  )
}
