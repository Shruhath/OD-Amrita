import { useRef, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { CategoryRecord, EventRecord, ValidationError } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { formatDate, messageOf, titleCase } from './format'

// The event is read by ID through the contract's item endpoint.
//
// It used to be found by fetching page 1 of the admin events collection at the
// contract's maximum page size and filtering locally, which meant the 101st
// event rendered "Event not found" — for an event that exists, is active, and
// whose categories the very next query loaded successfully.
const eventKey = (eventID: string) => ['admin', 'events', eventID] as const
const categoryKey = (eventID: string) => ['admin', 'events', eventID, 'categories'] as const

export default function EventDetailPortal() {
  const { event_id: eventID = '' } = useParams()
  const { api, identity, signOut } = useAuth()
  const queryClient = useQueryClient()
  const nameInput = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)

  const currentEventKey = eventKey(eventID)
  const events = useQuery({
    queryKey: currentEventKey,
    queryFn: () => api.event(eventID),
    enabled: Boolean(eventID),
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
      //
      // Only when the list is already loaded. setQueryData marks a query
      // SUCCESSFUL, so writing into a list whose own fetch FAILED would clear
      // the error and render a one-item list as though it were the event's
      // complete set — a wrong answer presented with full confidence. Leaving
      // the failed query alone keeps the error visible; the invalidate below
      // refetches the truth.
      if (categories.isSuccess) {
        queryClient.setQueryData(currentKey, (current: { data: CategoryRecord[] } | undefined) => ({
          // De-duplicated by id: the refetch triggered by a previous add can
          // land AFTER this one succeeds and already contain this row, and
          // appending blindly renders the same category twice under a duplicate
          // React key until the next refetch happens to fix it.
          data: orderedCategories(mergeByID(current?.data ?? [], created)),
        }))
      }
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
      // EVENT_ARCHIVED means somebody archived this event while the page was
      // open, so the cached event is now a lie: the form and its enabled submit
      // stay on screen, and every retry earns the same 409. Refetching the event
      // makes the UI agree with the server — the add affordance disappears,
      // which is the documented archived behavior.
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const event = events.data
  const categoryData = categories.data?.data ?? []
  const canSubmit = name.trim().length > 0 && !create.isPending
  const notFound = events.isError && events.error instanceof ApiFailure
    && events.error.body.code === 'EVENT_NOT_FOUND'

  // Editing the field retracts the rejection that described its PREVIOUS value.
  // Leaving it in place keeps aria-invalid="true" and the old message wired to
  // the input through aria-describedby, so a screen reader announces "a category
  // with this name already exists" over a name the admin has since changed.
  const changeName = (value: string) => {
    setName(value)
    if (fieldError) setFieldError(null)
    if (formError) setFormError(null)
  }

  const submit = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    const trimmed = name.trim()
    if (!trimmed || create.isPending) return
    // Cleared at the START of every attempt, the way the events list dialog
    // does it, so a stale message never overlaps an in-flight request.
    setFieldError(null)
    setFormError(null)
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
        {/* A 404 is the server SAYING the event does not exist, which is a
            different claim from "the request failed" and must not offer a retry
            that can never succeed. Every other failure keeps the retry. */}
        {notFound && (
          <section className="empty-state">
            <h1>Event not found</h1>
            <p>This event does not exist. Return to the events list to continue.</p>
          </section>
        )}
        {events.isError && !notFound && (
          <ErrorState message={messageOf(events.error, 'The event could not be loaded')} retry={() => void events.refetch()} />
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
                    onChange={(changeEvent) => changeName(changeEvent.target.value)}
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

/**
 * The server orders by name then id; the client mirrors it, never invents it.
 *
 * Compared with `<`/`>` on the raw strings rather than localeCompare, because
 * the server's ORDER BY uses the DATABASE's collation. localeCompare applies
 * locale rules — it sorts "apple" before "Zebra"; a C/POSIX-collation cluster
 * puts "Zebra" first, since 'Z' (0x5A) precedes 'a' (0x61). The two only agree
 * by luck of the cluster's locale, and when they disagree the list visibly
 * reshuffles the moment the revalidating refetch lands. Byte-ish comparison is
 * the conservative choice: this ordering exists ONLY to place a newly added row
 * where the server would, and the refetch is always the final authority.
 */
function orderedCategories(categories: CategoryRecord[]) {
  return [...categories].sort((left, right) => {
    if (left.name !== right.name) return left.name < right.name ? -1 : 1
    if (left.id === right.id) return 0
    return left.id < right.id ? -1 : 1
  })
}

/** Appends a category unless the list already carries that id. */
function mergeByID(categories: CategoryRecord[], created: CategoryRecord) {
  return categories.some((category) => category.id === created.id)
    ? categories.map((category) => (category.id === created.id ? created : category))
    : [...categories, created]
}
