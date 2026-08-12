import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { motion, AnimatePresence } from 'motion/react'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { Check } from '@phosphor-icons/react/dist/icons/Check'
import { Circle } from '@phosphor-icons/react/dist/icons/Circle'
import { MagnifyingGlass } from '@phosphor-icons/react/dist/icons/MagnifyingGlass'
import type { CaptureCreditUsage, CategoryRecord, CoordinatorAssignment, EventRecord, ValidationError } from '../../../api/client'
import { BranchChip } from '../../../components/BranchChip'
import { PortalShell } from '../../../components/PortalShell'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { EventAttendanceSection } from './EventAttendanceSection'
import { formatDateRange, messageOf, titleCase } from './format'

const eventKey = (eventID: string) => ['admin', 'events', eventID] as const
const categoryKey = (eventID: string) => ['admin', 'events', eventID, 'categories'] as const
const coordinatorKey = (eventID: string) => ['admin', 'events', eventID, 'coordinators'] as const
const creditKey = (eventID: string) => ['admin', 'events', eventID, 'capture-credits'] as const

export default function EventDetailPortal() {
  const { event_id: eventID = '' } = useParams()
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const nameInput = useRef<HTMLInputElement>(null)
  const coordinatorInput = useRef<HTMLInputElement>(null)
  const [name, setName] = useState('')
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [coordinatorEmail, setCoordinatorEmail] = useState('')
  const [coordinatorCategoryIDs, setCoordinatorCategoryIDs] = useState<string[]>([])
  const [coordinatorFieldError, setCoordinatorFieldError] = useState<string | null>(null)
  const [coordinatorFormError, setCoordinatorFormError] = useState<string | null>(null)
  const [creditLimit, setCreditLimit] = useState('5')
  const [creditFieldError, setCreditFieldError] = useState<string | null>(null)
  const [creditFormError, setCreditFormError] = useState<string | null>(null)
  const [resetCandidate, setResetCandidate] = useState<CaptureCreditUsage | null>(null)

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
  const currentCoordinatorKey = coordinatorKey(eventID)
  const coordinators = useQuery({
    queryKey: currentCoordinatorKey,
    queryFn: () => api.coordinators(eventID),
    enabled: Boolean(eventID),
  })
  const currentCreditKey = creditKey(eventID)
  const credits = useQuery({
    queryKey: currentCreditKey,
    queryFn: () => api.captureCredits(eventID),
    enabled: Boolean(eventID),
  })

  const create = useMutation({
    mutationFn: (value: string) => api.createCategory(eventID, { name: value }),
    onSuccess: (created) => {
      if (categories.isSuccess) {
        queryClient.setQueryData(currentKey, (current: { data: CategoryRecord[] } | undefined) => ({
          data: orderedCategories(mergeByID(current?.data ?? [], created)),
        }))
      }
      void queryClient.invalidateQueries({ queryKey: currentKey })
      setName('')
      setFieldError(null)
      setFormError(null)
      nameInput.current?.focus()
    },
    onError: (cause) => {
      const detail = fieldDetail(cause, 'name')
      if (detail) {
        setFieldError(detail)
        setFormError(null)
        return
      }
      setFieldError(null)
      setFormError(messageOf(cause, 'The category could not be added'))
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        markEventArchived(queryClient, currentEventKey)
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const createCoordinator = useMutation({
    mutationFn: (email: string) => api.createCoordinator(eventID, { email, category_ids: coordinatorCategoryIDs }),
    onSuccess: (created) => {
      if (coordinators.isSuccess) {
        queryClient.setQueryData(currentCoordinatorKey, (current: { data: CoordinatorAssignment[] } | undefined) => ({
          data: orderedCoordinators(mergeCoordinatorByID(current?.data ?? [], created)),
        }))
      }
      void queryClient.invalidateQueries({ queryKey: currentCoordinatorKey })
      setCoordinatorEmail('')
      setCoordinatorCategoryIDs([])
      setCoordinatorFieldError(null)
      setCoordinatorFormError(null)
      coordinatorInput.current?.focus()
    },
    onError: (cause) => {
      const detail = fieldDetail(cause, 'email')
      if (detail) {
        setCoordinatorFieldError(detail)
        setCoordinatorFormError(null)
        return
      }
      setCoordinatorFieldError(null)
      setCoordinatorFormError(messageOf(cause, 'The coordinator could not be assigned'))
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        markEventArchived(queryClient, currentEventKey)
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const updateCategoryVisibility = useMutation({
    mutationFn: ({ categoryID, visible }: { categoryID: string; visible: boolean }) => (
      api.updateCategoryVisibility(eventID, categoryID, { visible_on_public_dashboard: visible })
    ),
    onSuccess: (updated) => {
      queryClient.setQueryData(currentKey, (current: { data: CategoryRecord[] } | undefined) => ({
        data: orderedCategories(mergeByID(current?.data ?? [], updated)),
      }))
      void queryClient.invalidateQueries({ queryKey: currentKey })
    },
    onError: (cause) => {
      setFormError(messageOf(cause, 'The category visibility could not be saved'))
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        markEventArchived(queryClient, currentEventKey)
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const updateCreditConfig = useMutation({
    mutationFn: (limit: number) => api.updateCaptureCreditConfig(eventID, { limit }),
    onSuccess: () => {
      setCreditFieldError(null)
      setCreditFormError(null)
      void queryClient.invalidateQueries({ queryKey: currentCreditKey })
      void queryClient.invalidateQueries({ queryKey: ['mark-status'] })
    },
    onError: (cause) => {
      const detail = fieldDetail(cause, 'limit')
      if (detail) {
        setCreditFieldError(detail)
        setCreditFormError(null)
        return
      }
      setCreditFieldError(null)
      setCreditFormError(messageOf(cause, 'The fallback credit cap could not be saved'))
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        markEventArchived(queryClient, currentEventKey)
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const resetCredits = useMutation({
    mutationFn: (userID: string) => api.resetCaptureCredits(eventID, userID),
    onSuccess: (updated) => {
      if (credits.isSuccess) {
        queryClient.setQueryData(currentCreditKey, (current: typeof credits.data | undefined) => (
          current ? { ...current, data: current.data.map((row) => (row.user_id === updated.user_id ? updated : row)) } : current
        ))
      }
      setResetCandidate(null)
      setCreditFormError(null)
      void queryClient.invalidateQueries({ queryKey: currentCreditKey })
      void queryClient.invalidateQueries({ queryKey: ['mark-status'] })
    },
    onError: (cause) => {
      setCreditFormError(messageOf(cause, 'Fallback credits could not be reset'))
      if (cause instanceof ApiFailure && cause.body.code === 'EVENT_ARCHIVED') {
        markEventArchived(queryClient, currentEventKey)
        void queryClient.invalidateQueries({ queryKey: currentEventKey })
      }
    },
  })

  const event = events.data
  const categoryData = categories.data?.data ?? []
  const coordinatorData = coordinators.data?.data ?? []
  const creditData = credits.data?.data ?? []
  const canSubmit = name.trim().length > 0 && !create.isPending
  const canSubmitCoordinator = coordinatorEmail.trim().length > 0 && coordinatorCategoryIDs.length > 0 && !createCoordinator.isPending
  const notFound = events.isError && events.error instanceof ApiFailure
    && events.error.body.code === 'EVENT_NOT_FOUND'

  useEffect(() => {
    if (credits.isSuccess && !updateCreditConfig.isPending) {
      setCreditLimit(String(credits.data.config.limit))
    }
  }, [credits.isSuccess, credits.data?.config.limit, updateCreditConfig.isPending])

  const changeName = (value: string) => {
    setName(value)
    if (fieldError) setFieldError(null)
    if (formError) setFormError(null)
  }

  const changeCoordinatorEmail = (value: string) => {
    setCoordinatorEmail(value)
    if (coordinatorFieldError) setCoordinatorFieldError(null)
    if (coordinatorFormError) setCoordinatorFormError(null)
  }

  const toggleCoordinatorCategory = (categoryID: string) => {
    setCoordinatorCategoryIDs((current) => (
      current.includes(categoryID)
        ? current.filter((id) => id !== categoryID)
        : [...current, categoryID]
    ))
    if (coordinatorFormError) setCoordinatorFormError(null)
  }

  const changeCreditLimit = (value: string) => {
    setCreditLimit(value)
    if (creditFieldError) setCreditFieldError(null)
    if (creditFormError) setCreditFormError(null)
  }

  const submit = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    const trimmed = name.trim()
    if (!trimmed || create.isPending) return
    setFieldError(null)
    setFormError(null)
    create.mutate(trimmed)
  }

  const submitCoordinator = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    const trimmed = coordinatorEmail.trim()
    if (!trimmed || coordinatorCategoryIDs.length === 0 || createCoordinator.isPending) return
    setCoordinatorFieldError(null)
    setCoordinatorFormError(null)
    createCoordinator.mutate(trimmed)
  }

  const submitCreditLimit = (submitEvent: FormEvent) => {
    submitEvent.preventDefault()
    const trimmed = creditLimit.trim()
    const parsed = Number(trimmed)
    if (trimmed === '' || !Number.isInteger(parsed) || parsed < 0) {
      setCreditFieldError('Enter a whole number of 0 or more.')
      setCreditFormError(null)
      return
    }
    updateCreditConfig.mutate(parsed)
  }

  return (
    <PortalShell>
      {/* Breadcrumb */}
      <nav aria-label="Breadcrumb" className="mb-6">
        <Link
          to="/portal/events"
          className="inline-flex items-center gap-1.5 text-sm font-medium text-foreground-muted hover:text-foreground transition-colors"
        >
          <span aria-hidden="true">←</span> All events
        </Link>
      </nav>

      {events.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading event…
        </div>
      )}

      {notFound && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3">
          <MagnifyingGlass size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h1 className="text-2xl font-bold tracking-tight">Event not found</h1>
          <p className="text-foreground-muted">This event does not exist. Return to the events list to continue.</p>
          <Link to="/portal/events" className="primary mt-4">Back to Events</Link>
        </div>
      )}

      {events.isError && !notFound && (
        <ErrorState message={messageOf(events.error, 'The event could not be loaded')} retry={() => void events.refetch()} />
      )}

      {event && (
        <motion.div
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ type: 'spring', damping: 25, stiffness: 300 }}
          className="flex flex-col gap-6"
        >
          {/* Hero Panel */}
          <div className="bg-surface border border-border rounded-[10px] p-6 md:p-8 shadow-sm">
            <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-2">Administration · Events</p>
            <h1 className="text-3xl font-bold tracking-tight mb-4">{event.name}</h1>
            <div className="flex flex-wrap items-center gap-4">
              <span className="text-sm text-foreground-muted font-medium">{formatDateRange(event.event_date, event.end_date)}</span>
              <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-xs font-bold uppercase tracking-wider ${
                event.status === 'active'
                  ? 'bg-status-success-bg text-status-success-fg border border-status-success-fg/20'
                  : 'bg-canvas text-foreground-muted border border-border'
              }`}>
                {event.status === 'active'
                  ? <Circle size={10} weight="fill" aria-hidden="true" className="animate-pulse" />
                  : <Check size={14} weight="bold" aria-hidden="true" />}
                {titleCase(event.status)}
              </span>
              <div className="flex flex-wrap gap-2">
                {event.branches.map((branch) => <BranchChip branch={branch} key={branch.id} />)}
              </div>
            </div>
          </div>

          {/* Bento Grid */}
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* Categories Panel */}
            <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm flex flex-col gap-4">
              <div>
                <h2 className="text-lg font-bold tracking-tight">Categories</h2>
                <p className="text-sm text-foreground-muted mt-1">Scope OD records to a specific activity within this event.</p>
              </div>

              {categories.isPending && (
                <p className="text-sm text-foreground-muted flex items-center gap-2" role="status">
                  <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
                  Loading…
                </p>
              )}
              {categories.isError && (
                <ErrorState message={messageOf(categories.error, 'Categories could not be loaded')} retry={() => void categories.refetch()} />
              )}

              {categories.isSuccess && categoryData.length === 0 && (
                <p className="text-sm text-foreground-muted italic">No categories yet</p>
              )}

              {categoryData.length > 0 && (
                <ul className="flex flex-col gap-2" aria-label="Categories">
                  <AnimatePresence initial={false}>
                    {categoryData.map((category) => (
                      <motion.li
                        key={category.id}
                        initial={{ opacity: 0, height: 0 }}
                        animate={{ opacity: 1, height: 'auto' }}
                        exit={{ opacity: 0, height: 0 }}
                        className="flex items-center justify-between gap-3 p-3 bg-canvas rounded-[10px] border border-border text-sm font-medium"
                      >
                        <div className="flex items-center gap-3 min-w-0">
                          <span className="w-1.5 h-1.5 rounded-full bg-foreground-muted shrink-0" aria-hidden="true" />
                          <span className="truncate">{category.name}</span>
                        </div>
                        <label className="flex items-center gap-2 text-xs font-semibold text-foreground-muted shrink-0">
                          <input
                            type="checkbox"
                            className="h-4 w-4 accent-foreground"
                            checked={category.visible_on_public_dashboard}
                            disabled={event.status === 'archived' || updateCategoryVisibility.isPending}
                            onChange={(changeEvent) => updateCategoryVisibility.mutate({
                              categoryID: category.id,
                              visible: changeEvent.target.checked,
                            })}
                          />
                          Public
                        </label>
                      </motion.li>
                    ))}
                  </AnimatePresence>
                </ul>
              )}

              {event.status === 'active' && (
                <form onSubmit={submit} noValidate className="flex flex-col gap-2 mt-2 pt-4 border-t border-border">
                  <label htmlFor="category-name" className="text-sm font-semibold">Category name</label>
                  <div className="flex gap-2">
                    <input
                      id="category-name"
                      ref={nameInput}
                      value={name}
                      onChange={(e) => changeName(e.target.value)}
                      aria-invalid={fieldError ? 'true' : undefined}
                      aria-describedby={fieldError ? 'category-name-error' : undefined}
                      className={`flex-1 bg-canvas border focus:ring-1 rounded-[10px] px-3 py-2 text-sm outline-none transition-all ${fieldError ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
                      placeholder="e.g. Day 1 Morning"
                    />
                    <button
                      className="bg-primary text-primary-foreground px-4 py-2 rounded-[10px] text-sm font-semibold active:scale-95 transition-all disabled:opacity-50"
                      type="submit"
                      disabled={!canSubmit}
                    >
                      {create.isPending ? 'Adding category…' : 'Add category'}
                    </button>
                  </div>
                  {fieldError && <p className="text-sm font-medium text-status-error-fg" id="category-name-error">{fieldError}</p>}
                  {formError && <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">{formError}</p>}
                </form>
              )}
              {event.status === 'archived' && (
                <p className="text-xs text-foreground-muted italic mt-2">This event is archived. Its categories are read-only.</p>
              )}
            </section>

            {/* Coordinators Panel */}
            <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm flex flex-col gap-4">
              <div>
                <h2 className="text-lg font-bold tracking-tight">Coordinators</h2>
                <p className="text-sm text-foreground-muted mt-1">Assigned coordinators can mark attendance for this event.</p>
              </div>

              {coordinators.isPending && (
                <p className="text-sm text-foreground-muted flex items-center gap-2" role="status">
                  <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
                  Loading…
                </p>
              )}
              {coordinators.isError && (
                <ErrorState message={messageOf(coordinators.error, 'Coordinators could not be loaded')} retry={() => void coordinators.refetch()} />
              )}

              {coordinators.isSuccess && coordinatorData.length === 0 && (
                <p className="text-sm text-foreground-muted italic">No coordinators assigned.</p>
              )}

              {coordinatorData.length > 0 && (
                <ul className="flex flex-col gap-2" aria-label="Coordinators">
                  <AnimatePresence initial={false}>
                    {coordinatorData.map((coordinator) => (
                      <motion.li
                        key={coordinator.id}
                        initial={{ opacity: 0, height: 0 }}
                        animate={{ opacity: 1, height: 'auto' }}
                        exit={{ opacity: 0, height: 0 }}
                        className="flex items-center justify-between p-3 bg-canvas rounded-[10px] border border-border"
                      >
                        <div className="flex items-center gap-3">
                          <div className="w-8 h-8 rounded-full bg-foreground/10 flex items-center justify-center text-xs font-bold shrink-0">
                            {(coordinator.name ?? coordinator.email).slice(0, 2).toUpperCase()}
                          </div>
                          <div className="flex flex-col">
                            <span className="text-sm font-semibold">{coordinator.name ?? coordinator.email}</span>
                            {coordinator.name && <span className="text-xs text-foreground-muted">{coordinator.email}</span>}
                            {coordinator.categories.length > 0 && (
                              <span className="text-xs text-foreground-muted mt-0.5">
                                {coordinator.categories.map((category) => category.name).join(', ')}
                              </span>
                            )}
                          </div>
                        </div>
                        <time className="text-xs text-foreground-muted" dateTime={coordinator.created_at}>
                          {new Date(coordinator.created_at).toLocaleDateString()}
                        </time>
                      </motion.li>
                    ))}
                  </AnimatePresence>
                </ul>
              )}

              {event.status === 'active' && (
                <form onSubmit={submitCoordinator} noValidate className="flex flex-col gap-2 mt-2 pt-4 border-t border-border">
                  <label htmlFor="coordinator-email" className="text-sm font-semibold">Coordinator email</label>
                  <div className="flex gap-2">
                    <input
                      id="coordinator-email"
                      ref={coordinatorInput}
                      value={coordinatorEmail}
                      type="email"
                      onChange={(e) => changeCoordinatorEmail(e.target.value)}
                      aria-invalid={coordinatorFieldError ? 'true' : undefined}
                      aria-describedby={coordinatorFieldError ? 'coordinator-email-error' : undefined}
                      className={`flex-1 bg-canvas border focus:ring-1 rounded-[10px] px-3 py-2 text-sm outline-none transition-all ${coordinatorFieldError ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
                      placeholder="name@amrita.edu"
                    />
                    <button
                      className="bg-primary text-primary-foreground px-4 py-2 rounded-[10px] text-sm font-semibold active:scale-95 transition-all disabled:opacity-50"
                      type="submit"
                      disabled={!canSubmitCoordinator}
                    >
                      {createCoordinator.isPending ? 'Assigning coordinator…' : 'Assign coordinator'}
                    </button>
                  </div>
                  <fieldset className="flex flex-col gap-2 pt-2">
                    <legend className="text-sm font-semibold">Categories</legend>
                    {categoryData.length === 0 && (
                      <p className="text-xs text-foreground-muted">Create a category before assigning a coordinator.</p>
                    )}
                    {categoryData.length > 0 && (
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                        {categoryData.map((category) => (
                          <label
                            key={category.id}
                            className="flex items-center gap-2 rounded-[10px] border border-border bg-canvas px-3 py-2 text-sm font-medium"
                          >
                            <input
                              type="checkbox"
                              className="h-4 w-4 accent-foreground"
                              checked={coordinatorCategoryIDs.includes(category.id)}
                              onChange={() => toggleCoordinatorCategory(category.id)}
                            />
                            <span className="truncate">{category.name}</span>
                          </label>
                        ))}
                      </div>
                    )}
                  </fieldset>
                  {coordinatorFieldError && <p className="text-sm font-medium text-status-error-fg" id="coordinator-email-error">{coordinatorFieldError}</p>}
                  {coordinatorFormError && <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">{coordinatorFormError}</p>}
                </form>
              )}
              {event.status === 'archived' && (
                <p className="text-xs text-foreground-muted italic mt-2">This event is archived. Its coordinator assignments are read-only.</p>
              )}
              {event.status === 'archived' && coordinatorFormError && (
                <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">
                  {coordinatorFormError}
                </p>
              )}
            </section>
          </div>

          {/* Fallback Credits Panel */}
          <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm" aria-label="Fallback credits">
            <div className="mb-4">
              <h2 className="text-lg font-bold tracking-tight">Fallback Credits</h2>
              <p className="text-sm text-foreground-muted mt-1">Cap on fallback marks a coordinator can use per day. Usage below is for today only.</p>
            </div>

            {credits.isPending && (
              <p className="text-sm text-foreground-muted flex items-center gap-2" role="status">
                <span className="animate-spin inline-block w-3 h-3 border-2 border-current border-t-transparent rounded-full" />
                Loading…
              </p>
            )}
            {credits.isError && (
              <ErrorState message={messageOf(credits.error, 'Fallback credits could not be loaded')} retry={() => void credits.refetch()} />
            )}

            {credits.isSuccess && (
              <div className="flex flex-col gap-4">
                <div className="flex items-center gap-4 p-4 bg-canvas rounded-[10px] border border-border">
                  <div className="text-4xl font-black tracking-tight">{credits.data.config.limit}</div>
                  <div>
                    <p className="text-sm font-semibold">Cap: {credits.data.config.limit} credits per coordinator per day</p>
                    <p className="text-xs text-foreground-muted">Resets at midnight IST</p>
                  </div>
                </div>

                {creditData.length > 0 && (
                  <ul className="flex flex-col gap-2" aria-label="Fallback credit usage">
                    {creditData.map((row) => (
                      <CreditUsageRow
                        key={row.user_id}
                        row={row}
                        readOnly={event.status === 'archived'}
                        resetPending={resetCredits.isPending}
                        onReset={() => setResetCandidate(row)}
                      />
                    ))}
                  </ul>
                )}

                {event.status === 'active' && (
                  <form onSubmit={submitCreditLimit} noValidate className="flex flex-col gap-2 pt-4 border-t border-border">
                    <label htmlFor="fallback-credit-limit" className="text-sm font-semibold">Fallback credit cap</label>
                    <div className="flex gap-2">
                      <input
                        id="fallback-credit-limit"
                        type="number"
                        min="0"
                        value={creditLimit}
                        onChange={(e) => changeCreditLimit(e.target.value)}
                        aria-invalid={creditFieldError ? 'true' : undefined}
                        aria-describedby={creditFieldError ? 'fallback-credit-limit-error' : undefined}
                        className={`w-28 bg-canvas border focus:ring-1 rounded-[10px] px-3 py-2 text-sm outline-none transition-all ${creditFieldError ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg' : 'border-border focus:border-foreground focus:ring-foreground'}`}
                      />
                      <button
                        className="bg-primary text-primary-foreground px-4 py-2 rounded-[10px] text-sm font-semibold active:scale-95 transition-all disabled:opacity-50"
                        type="submit"
                        disabled={updateCreditConfig.isPending}
                      >
                        {updateCreditConfig.isPending ? 'Saving…' : 'Save cap'}
                      </button>
                    </div>
                    {creditFieldError && <p className="text-sm font-medium text-status-error-fg" id="fallback-credit-limit-error">{creditFieldError}</p>}
                    {creditFormError && <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">{creditFormError}</p>}
                  </form>
                )}
                {event.status === 'archived' && (
                  <p className="text-xs text-foreground-muted italic">This event is archived. Fallback credits are read-only.</p>
                )}
              </div>
            )}
          </section>

          {/* Attendance Section */}
          <EventAttendanceSection eventID={eventID} categories={categoryData} eventDate={event.event_date} endDate={event.end_date} />
        </motion.div>
      )}

      {/* Reset Credits Modal */}
      <AnimatePresence>
        {resetCandidate && (
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-foreground/20 backdrop-blur-sm"
            role="presentation"
          >
            <motion.div
              initial={{ opacity: 0, y: 20, scale: 0.95 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, y: 20, scale: 0.95 }}
              transition={{ type: 'spring', damping: 25, stiffness: 300 }}
              className="w-full max-w-md bg-surface border border-border rounded-[10px] shadow-xl p-6"
              role="dialog"
              aria-modal="true"
              aria-labelledby="reset-credit-title"
            >
              <h2 id="reset-credit-title" className="text-xl font-bold tracking-tight mb-2">Reset fallback credits</h2>
              <p className="text-foreground-muted text-sm mb-6">
                Reset <strong className="text-foreground">{resetCandidate.name ?? resetCandidate.email}</strong> to 0 used credits for today.
              </p>
              <div className="flex flex-col-reverse sm:flex-row gap-3">
                <button
                  type="button"
                  className="flex-1 border border-border bg-canvas hover:bg-surface text-foreground px-4 py-2.5 rounded-[10px] font-medium active:scale-95 transition-all disabled:opacity-50"
                  disabled={resetCredits.isPending}
                  onClick={() => setResetCandidate(null)}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className="flex-1 bg-primary text-primary-foreground px-4 py-2.5 rounded-[10px] font-semibold active:scale-95 transition-all disabled:opacity-50"
                  disabled={resetCredits.isPending}
                  onClick={() => resetCredits.mutate(resetCandidate.user_id)}
                >
                  {resetCredits.isPending ? 'Resetting…' : 'Confirm reset'}
                </button>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </PortalShell>
  )
}

function CreditUsageRow({
  row,
  readOnly,
  resetPending,
  onReset,
}: {
  row: CaptureCreditUsage
  readOnly: boolean
  resetPending: boolean
  onReset(): void
}) {
  return (
    <li className="flex items-center justify-between p-3 bg-canvas rounded-[10px] border border-border">
      <div className="flex items-center gap-3">
        <div className="w-8 h-8 rounded-full bg-foreground/10 flex items-center justify-center text-xs font-bold shrink-0">
          {(row.name ?? row.email).slice(0, 2).toUpperCase()}
        </div>
        <div className="flex flex-col">
          <span className="text-sm font-semibold">{row.name ?? row.email}</span>
          {row.name && <span className="text-xs text-foreground-muted">{row.email}</span>}
          <span className="text-xs text-foreground-muted mt-0.5">{row.consumed} used · {row.remaining} remaining</span>
        </div>
      </div>
      <div className="flex items-center gap-3 shrink-0">
        {row.exhausted && (
          <span className="text-xs font-bold px-2 py-1 bg-status-error-bg text-status-error-fg rounded-md">Exhausted</span>
        )}
        {!readOnly && (
          <button
            type="button"
            className="text-xs font-medium px-3 py-1.5 rounded-[10px] border border-border hover:bg-surface active:scale-95 transition-all disabled:opacity-50"
            disabled={resetPending}
            onClick={onReset}
            aria-label={`Reset ${row.email}`}
          >
            Reset
          </button>
        )}
      </div>
    </li>
  )
}

function ErrorState({ message, retry }: { message: string; retry(): void }) {
  return (
    <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20" role="alert">
      <span className="font-medium text-sm">{message}</span>
      <button
        type="button"
        onClick={retry}
        className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
      >
        Try again
      </button>
    </div>
  )
}

function fieldDetail(cause: unknown, field: string) {
  if (!(cause instanceof ApiFailure)) return null
  if (cause.body.code !== 'VALIDATION_FAILED') return null
  if (!('details' in cause.body) || !Array.isArray(cause.body.details)) return null
  const match = (cause.body as ValidationError).details.find((detail) => detail.field === field)
  return match ? match.message : null
}

function orderedCategories(categories: CategoryRecord[]) {
  return [...categories].sort((left, right) => {
    if (left.name !== right.name) return left.name < right.name ? -1 : 1
    if (left.id === right.id) return 0
    return left.id < right.id ? -1 : 1
  })
}

function orderedCoordinators(coordinators: CoordinatorAssignment[]) {
  return [...coordinators].sort((left, right) => {
    if (left.created_at !== right.created_at) return left.created_at < right.created_at ? -1 : 1
    if (left.id === right.id) return 0
    return left.id < right.id ? -1 : 1
  })
}

function mergeByID(categories: CategoryRecord[], created: CategoryRecord) {
  return categories.some((category) => category.id === created.id)
    ? categories.map((category) => (category.id === created.id ? created : category))
    : [...categories, created]
}

function mergeCoordinatorByID(coordinators: CoordinatorAssignment[], created: CoordinatorAssignment) {
  return coordinators.some((coordinator) => coordinator.id === created.id)
    ? coordinators.map((coordinator) => (coordinator.id === created.id ? created : coordinator))
    : [...coordinators, created]
}

function markEventArchived(
  queryClient: ReturnType<typeof useQueryClient>,
  key: ReturnType<typeof eventKey>,
) {
  queryClient.setQueryData(key, (current: EventRecord | undefined) => (
    current ? { ...current, status: 'archived' as const, archived_at: current.archived_at ?? new Date().toISOString() } : current
  ))
}
