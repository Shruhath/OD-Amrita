import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Dispute, DisputeCollection } from '../../../api/client'
import { ApiFailure } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'

const pageSize = 20
const queryKey = ['portal', 'disputes', 'open', 1, pageSize] as const

export default function DisputesPortal() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [openID, setOpenID] = useState<string | null>(null)
  const [notes, setNotes] = useState<Record<string, string>>({})
  const [failure, setFailure] = useState<string | null>(null)

  const disputes = useQuery({
    queryKey,
    queryFn: () => api.disputes(1, pageSize, 'open'),
  })
  const rows = useMemo(() => disputes.data?.data ?? [], [disputes.data])

  const resolve = useMutation({
    mutationFn: (row: Dispute) => api.resolveDispute(row.dispute_id, { resolution_note: (notes[row.dispute_id] ?? '').trim() }),
    onSuccess(resolved) {
      setFailure(null)
      queryClient.setQueryData(queryKey, (current: DisputeCollection | undefined) => {
        if (!current) return current
        return { ...current, data: current.data.map((row) => row.dispute_id === resolved.dispute_id ? resolved : row) }
      })
    },
    onError(cause) {
      setFailure(resolveFailureMessage(cause))
    },
  })

  const submitResolution = (row: Dispute) => {
    const note = (notes[row.dispute_id] ?? '').trim()
    if (note === '') {
      setFailure('A resolution note is required before marking a dispute resolved.')
      return
    }
    setFailure(null)
    resolve.mutate(row)
  }

  return (
    <PortalShell>
      <div className="page-heading">
        <div>
          <p className="eyebrow">Attendance review</p>
          <h1>Disputes</h1>
          <p>Review missed-slot reports from students and record the decision for each open item.</p>
        </div>
      </div>

      {disputes.isPending && <p role="status">Loading disputes…</p>}
      {disputes.isError && (
        <div className="notice error" role="alert">
          <span>{listFailureMessage(disputes.error)}</span>
          <button type="button" onClick={() => void disputes.refetch()}>Try again</button>
        </div>
      )}
      {failure && <p className="notice error" role="alert">{failure}</p>}

      {disputes.isSuccess && rows.length === 0 && (
        <section className="empty-state">
          <h2>No open disputes</h2>
          <p>Open missed-slot reports will appear here newest first.</p>
        </section>
      )}

      {rows.length > 0 && (
        <section className="detail-section">
          <h2>Open disputes</h2>
          <p>Resolving a dispute records the decision. It does not correct, change or fix the OD record.</p>
          <ul className="dispute-list">
            {rows.map((row) => {
              const expanded = openID === row.dispute_id
              const displayReference = row.reference || reference(row.dispute_id)
              return (
                <li key={row.dispute_id}>
                  <button
                    type="button"
                    className="dispute-summary"
                    onClick={() => setOpenID(expanded ? null : row.dispute_id)}
                    aria-expanded={expanded}
                  >
                    <strong>{displayReference}</strong>
                    <span>{row.student.roll_no} · Slot {row.slot}</span>
                    <span>{row.event_name}</span>
                    <span className={`status-badge ${row.status}`}>{titleCase(row.status)}</span>
                  </button>

                  {expanded && (
                    <section className="dispute-detail" role="region" aria-label={`Dispute ${displayReference}`}>
                      <dl>
                        <div><dt>Roll number</dt><dd>{row.student.roll_no}</dd></div>
                        <div><dt>Student name</dt><dd>{row.student.display_name}</dd></div>
                        <div><dt>Branch/section</dt><dd>{row.student.branch}-{row.student.section}</dd></div>
                        <div><dt>Batch</dt><dd>{row.student.batch}</dd></div>
                        <div><dt>Submitted email</dt><dd>{row.submitted_email}</dd></div>
                        <div><dt>Event</dt><dd>{row.event_name}</dd></div>
                        <div><dt>Category</dt><dd>{row.category_name}</dd></div>
                        <div><dt>Day</dt><dd>{formatDate(row.od_date || row.event_date)}</dd></div>
                        <div><dt>Slot </dt><dd>{row.slot} </dd></div>
                        <div><dt>Coordinator</dt><dd>{row.claimed_coordinator_name ?? 'Not provided'}</dd></div>
                        <div><dt>Student note</dt><dd>{row.student_note ?? 'No note provided'}</dd></div>
                        <div><dt>Status</dt><dd>{titleCase(row.status)}</dd></div>
                      </dl>

                      <p className="empty-note">Resolving this records the decision only. Correcting the OD record is a separate action.</p>

                      {row.status === 'open' ? (
                        <div className="resolution-form">
                          <label htmlFor={`resolution-${row.dispute_id}`}>Resolution note</label>
                          <textarea
                            id={`resolution-${row.dispute_id}`}
                            rows={3}
                            value={notes[row.dispute_id] ?? ''}
                            onChange={(event) => {
                              setNotes((current) => ({ ...current, [row.dispute_id]: event.target.value }))
                              setFailure(null)
                            }}
                          />
                          <button
                            className="primary"
                            type="button"
                            disabled={resolve.isPending}
                            onClick={() => submitResolution(row)}
                          >
                            Mark resolved
                          </button>
                        </div>
                      ) : (
                        <p className="notice success" role="status">Resolved: {row.resolution_note}</p>
                      )}
                    </section>
                  )}
                </li>
              )
            })}
          </ul>
        </section>
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

function formatDate(value: string): string {
  const parsed = new Date(`${value}T00:00:00`)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' })
}

function titleCase(value: string): string {
  return value.slice(0, 1).toUpperCase() + value.slice(1)
}

function reference(id: string): string {
  return id.replaceAll('-', '').slice(0, 8).toUpperCase()
}
