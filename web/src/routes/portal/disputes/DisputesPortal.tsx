import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Dispute, DisputeCollection } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'
import { AnimatePresence, motion } from 'motion/react'
import { CheckCircle } from '@phosphor-icons/react'

const pageSize = 20

export default function DisputesPortal() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [openID, setOpenID] = useState<string | null>(null)
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [failures, setFailures] = useState<Record<string, string>>({})
  const [page, setPage] = useState(1)

  const queryKey = ['portal', 'disputes', 'open', page, pageSize] as const

  const disputes = useQuery({
    queryKey,
    queryFn: () => api.disputes(page, pageSize, 'open'),
  })
  const rows = useMemo(() => disputes.data?.data ?? [], [disputes.data])
  const total = disputes.data?.total ?? 0
  const lastPage = Math.max(1, Math.ceil(total / pageSize))

  const setFailureFor = (disputeID: string, message: string | null) => {
    setFailures((current) => {
      const next = { ...current }
      if (message === null) delete next[disputeID]
      else next[disputeID] = message
      return next
    })
  }

  const resolve = useMutation({
    mutationFn: (row: Dispute) => api.resolveDispute(row.dispute_id, { resolution_note: (notes[row.dispute_id] ?? '').trim() }),
    onSuccess(resolved) {
      setFailureFor(resolved.dispute_id, null)
      queryClient.setQueryData(queryKey, (current: DisputeCollection | undefined) => {
        if (!current) return current
        return { ...current, data: current.data.map((row) => row.dispute_id === resolved.dispute_id ? resolved : row) }
      })
    },
    onError(cause, row) {
      setFailureFor(row.dispute_id, resolveFailureMessage(cause))
    },
  })

  const submitResolution = (row: Dispute) => {
    const note = (notes[row.dispute_id] ?? '').trim()
    if (note === '') {
      setFailureFor(row.dispute_id, 'A resolution note is required before marking a dispute resolved.')
      return
    }
    setFailureFor(row.dispute_id, null)
    resolve.mutate(row)
  }

  return (
    <PortalShell>
      {/* Page Header */}
      <div className="mb-8">
        <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Attendance review</p>
        <h1 className="text-3xl font-bold tracking-tight mb-2">Disputes</h1>
        <p className="text-foreground-muted">Review missed-slot reports from students and record the decision for each open item.</p>
      </div>

      {/* Loading */}
      {disputes.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading disputes…
        </div>
      )}

      {/* Error */}
      {disputes.isError && (
        <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-6" role="alert">
          <span className="font-medium text-sm">{listFailureMessage(disputes.error)}</span>
          <button
            type="button"
            onClick={() => void disputes.refetch()}
            className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
          >
            Try again
          </button>
        </div>
      )}

      {/* Empty state */}
      {disputes.isSuccess && rows.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3">
          <CheckCircle size={40} weight="duotone" aria-hidden="true" className="text-status-success-fg" />
          <h2 className="text-xl font-bold tracking-tight">No open disputes</h2>
          <p className="text-foreground-muted">Open missed-slot reports will appear here newest first.</p>
        </div>
      )}

      {/* Dispute List */}
      {rows.length > 0 && (
        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between mb-2">
            <p className="text-sm font-medium text-foreground-muted">
              <span className="font-bold text-foreground">{total}</span> open dispute{total !== 1 ? 's' : ''}
            </p>
            <p className="text-xs text-foreground-muted">Resolving records the decision — it does not correct the OD record itself.</p>
          </div>

          <ul className="flex flex-col gap-3">
            <AnimatePresence initial={false}>
              {rows.map((row) => {
                const expanded = openID === row.dispute_id
                const displayReference = row.reference
                const isResolved = row.status !== 'open'

                return (
                  <motion.li
                    key={row.dispute_id}
                    layout
                    initial={{ opacity: 0, y: 8 }}
                    animate={{ opacity: 1, y: 0 }}
                    className={`bg-surface border rounded-[10px] shadow-sm overflow-hidden transition-all ${
                      isResolved ? 'border-border opacity-70' : 'border-border hover:border-foreground/20'
                    } ${!isResolved && !expanded ? 'border-l-4 border-l-status-warning-fg' : ''}`}
                  >
                    {/* Row Summary (Inbox Item) */}
                    <button
                      type="button"
                      className="w-full flex flex-col sm:flex-row sm:items-center gap-3 p-4 text-left hover:bg-canvas/50 transition-colors active:bg-canvas"
                      onClick={() => setOpenID(expanded ? null : row.dispute_id)}
                      aria-expanded={expanded}
                    >
                      <div className="flex-1 flex flex-col gap-1">
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="font-bold text-sm">{displayReference}</span>
                          <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold ${
                            row.status === 'open'
                              ? 'bg-status-warning-bg text-status-warning-fg'
                              : 'bg-status-success-bg text-status-success-fg'
                          }`}>
                            {titleCase(row.status)}
                          </span>
                        </div>
                        <div className="flex items-center gap-2 text-sm text-foreground-muted flex-wrap">
                          <span className="font-mono">{row.student.roll_no}</span>
                          <span>·</span>
                          <span>Slot {row.slot}</span>
                          <span>·</span>
                          <span>{row.event_name}</span>
                        </div>
                      </div>
                      <span className={`text-foreground-muted transition-transform duration-200 shrink-0 ${expanded ? 'rotate-180' : ''}`} aria-hidden="true">
                        ▼
                      </span>
                    </button>

                    {/* Expanded Detail */}
                    <AnimatePresence>
                      {expanded && (
                        <motion.section
                          initial={{ opacity: 0, height: 0 }}
                          animate={{ opacity: 1, height: 'auto' }}
                          exit={{ opacity: 0, height: 0 }}
                          transition={{ duration: 0.2 }}
                          className="border-t border-border"
                          role="region"
                          aria-label={`Dispute ${displayReference}`}
                        >
                          <div className="p-4 md:p-6 flex flex-col gap-6">
                            {/* Details grid */}
                            <dl className="grid grid-cols-2 md:grid-cols-3 gap-4">
                              {[
                                { label: 'Roll number', value: row.student.roll_no },
                                { label: 'Student name', value: row.student.display_name },
                                { label: 'Branch/section', value: `${row.student.branch}-${row.student.section}` },
                                { label: 'Batch', value: row.student.batch },
                                { label: 'Event', value: row.event_name },
                                { label: 'Category', value: row.category_name },
                                { label: 'Day', value: formatDate(row.od_date) },
                                { label: 'Slot', value: String(row.slot) },
                                { label: 'Coordinator', value: row.claimed_coordinator_name ?? 'Not provided' },
                                { label: 'Email', value: row.submitted_email },
                                { label: 'Submitted', value: formatDateTime(row.submitted_at) },
                                { label: 'Student note', value: row.student_note ?? 'No note provided' },
                              ].map(({ label, value }) => (
                                <div key={label} className="flex flex-col gap-0.5">
                                  <dt className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">{label}</dt>
                                  <dd className="text-sm font-medium">{value}</dd>
                                </div>
                              ))}
                            </dl>

                            {/* Resolution form or resolved notice */}
                            {row.status === 'open' ? (
                              <div className="flex flex-col gap-3 p-4 bg-canvas rounded-[10px] border border-border">
                                <label htmlFor={`resolution-${row.dispute_id}`} className="text-sm font-semibold">Resolution note <span className="text-status-error-fg">*</span></label>
                                <textarea
                                  id={`resolution-${row.dispute_id}`}
                                  rows={3}
                                  value={notes[row.dispute_id] ?? ''}
                                  onChange={(event) => {
                                    setNotes((current) => ({ ...current, [row.dispute_id]: event.target.value }))
                                    setFailureFor(row.dispute_id, null)
                                  }}
                                  placeholder="Enter the decision and any relevant notes…"
                                  className="bg-surface border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all resize-none"
                                />
                                {failures[row.dispute_id] && (
                                  <p className="text-sm text-status-error-fg bg-status-error-bg p-3 rounded-[10px] border border-status-error-fg/20" role="alert">
                                    {failures[row.dispute_id]}
                                  </p>
                                )}
                                <button
                                  className="self-start bg-foreground text-canvas px-5 py-2.5 rounded-[10px] font-semibold text-sm active:scale-95 transition-all disabled:opacity-50"
                                  type="button"
                                  disabled={resolve.isPending && resolve.variables?.dispute_id === row.dispute_id}
                                  onClick={() => submitResolution(row)}
                                >
                                  {resolve.isPending && resolve.variables?.dispute_id === row.dispute_id
                                    ? 'Resolving…'
                                    : 'Mark resolved'}
                                </button>
                              </div>
                            ) : (
                              <div className="p-4 bg-status-success-bg text-status-success-fg rounded-[10px] border border-status-success-fg/20" role="status">
                                <p className="text-sm font-semibold mb-1">Resolved</p>
                                <p className="text-sm">{row.resolution_note}</p>
                              </div>
                            )}
                          </div>
                        </motion.section>
                      )}
                    </AnimatePresence>
                  </motion.li>
                )
              })}
            </AnimatePresence>
          </ul>

          {/* Pagination */}
          {lastPage > 1 && (
            <nav className="flex items-center justify-between bg-surface border border-border rounded-[10px] px-4 py-3 shadow-sm mt-2" aria-label="Dispute pages">
              <button
                type="button"
                disabled={page <= 1}
                onClick={() => setPage((current) => Math.max(1, current - 1))}
                className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
              >
                Previous
              </button>
              <span className="text-sm font-medium text-foreground-muted">Page {page} of {lastPage} · {total} open</span>
              <button
                type="button"
                disabled={page >= lastPage}
                onClick={() => setPage((current) => Math.min(lastPage, current + 1))}
                className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
              >
                Next
              </button>
            </nav>
          )}
        </div>
      )}
    </PortalShell>
  )
}

function listFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure && error.status === 403) return 'You are not allowed to view dispute reports.'
  if (error instanceof ApiFailure) return error.body.message
  return 'Disputes could not be loaded.'
}

function resolveFailureMessage(error: unknown): string {
  if (error instanceof ApiFailure) {
    if (error.body.code === 'DISPUTE_ALREADY_RESOLVED') return 'This dispute was already resolved.'
    if (error.body.code === 'DISPUTE_NOT_FOUND') return 'This dispute was not found or is no longer available.'
    if (error.body.code === 'RBAC_FORBIDDEN') return 'You do not have permission to resolve a dispute from another branch.'
    if (error.body.code === 'VALIDATION_FAILED') return error.body.message
    return error.body.message
  }
  return 'The dispute could not be resolved.'
}

function formatDateTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString(undefined, {
    day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit',
  })
}

function formatDate(value: string): string {
  const parsed = new Date(`${value}T00:00:00`)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' })
}

function titleCase(value: string): string {
  return value.slice(0, 1).toUpperCase() + value.slice(1)
}
