import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ApiFailure, type AuditLogPage } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { ArrowsClockwise } from '@phosphor-icons/react/dist/icons/ArrowsClockwise'
import { ClipboardText } from '@phosphor-icons/react/dist/icons/ClipboardText'
import { Funnel } from '@phosphor-icons/react/dist/icons/Funnel'

type AuditLogEntry = AuditLogPage['data'][number]

const pageSize = 50

export default function AuditLogPortal() {
  const { api } = useAuth()
  const [action, setAction] = useState('')
  const [actorAccount, setActorAccount] = useState('')
  const [targetTable, setTargetTable] = useState('')
  const [targetID, setTargetID] = useState('')
  const [page, setPage] = useState(1)

  useEffect(() => {
    setPage(1)
  }, [action, actorAccount, targetTable, targetID])

  const query = useQuery({
    queryKey: ['portal', 'audit-log', action, actorAccount, targetTable, targetID, page] as const,
    queryFn: () =>
      api.adminAuditLog({
        action: clean(action),
        actorAccount: clean(actorAccount),
        targetTable: clean(targetTable),
        targetID: clean(targetID),
        page,
        pageSize,
      }),
  })

  const rows = useMemo(() => query.data?.data ?? [], [query.data])
  const total = query.data?.total ?? 0
  const lastPage = Math.max(1, Math.ceil(total / pageSize))

  const resetFilters = () => {
    setAction('')
    setActorAccount('')
    setTargetTable('')
    setTargetID('')
    setPage(1)
  }

  const submitFilters = (event: FormEvent) => {
    event.preventDefault()
    void query.refetch()
  }

  return (
    <PortalShell>
      <div className="mb-8">
        <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Administration · Audit</p>
        <h1 className="text-3xl font-bold tracking-tight mb-2">Audit log</h1>
        <p className="text-foreground-muted">Trace administrative changes, attendance edits, dispute decisions and card-token operations.</p>
      </div>

      <section className="bg-surface border border-border rounded-[10px] p-5 shadow-sm mb-6" aria-label="Audit filters">
        <div className="flex items-center gap-2 mb-4">
          <Funnel size={20} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-lg font-bold tracking-tight">Filters</h2>
        </div>
        <form className="grid gap-4 md:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_1fr_auto]" onSubmit={submitFilters}>
          <FilterField label="Action" value={action} onChange={setAction} placeholder="od.created" />
          <FilterField label="Actor email" value={actorAccount} onChange={setActorAccount} placeholder="admin@amrita.edu" />
          <FilterField label="Target table" value={targetTable} onChange={setTargetTable} placeholder="od_records" />
          <FilterField label="Target id" value={targetID} onChange={setTargetID} placeholder="record id" />
          <div className="flex items-end gap-2">
            <button type="submit" className="primary inline-flex items-center gap-2 justify-center min-h-10 px-4">
              <Funnel size={16} weight="bold" aria-hidden="true" />
              Apply
            </button>
            <button
              type="button"
              onClick={resetFilters}
              className="secondary inline-flex items-center gap-2 justify-center min-h-10 px-4"
            >
              <ArrowsClockwise size={16} weight="bold" aria-hidden="true" />
              Reset
            </button>
          </div>
        </form>
      </section>

      {query.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading audit entries...
        </div>
      )}

      {query.isError && (
        <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-6" role="alert">
          <span className="font-medium text-sm">{failureMessage(query.error)}</span>
          <button
            type="button"
            onClick={() => void query.refetch()}
            className="text-xs font-bold uppercase tracking-wider px-3 py-1.5 rounded-[10px] border border-status-error-fg/30 hover:bg-status-error-fg/10 active:scale-95 transition-all"
          >
            Try again
          </button>
        </div>
      )}

      {query.isSuccess && rows.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3">
          <ClipboardText size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">No audit entries</h2>
          <p className="text-foreground-muted">No audit entries match these filters.</p>
        </div>
      )}

      {rows.length > 0 && (
        <section className="bg-surface border border-border rounded-[10px] shadow-sm overflow-hidden" aria-label="Audit entries">
          <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 p-4 border-b border-border">
            <p className="text-sm font-medium text-foreground-muted">
              <span className="font-bold text-foreground">{total}</span> entr{total === 1 ? 'y' : 'ies'} newest first
            </p>
            <p className="text-xs text-foreground-muted">Page {page} of {lastPage}</p>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse" aria-label="Audit log entries">
              <thead className="bg-canvas border-b border-border">
                <tr>
                  {['Time', 'Action', 'Actor', 'Target', 'Payload'].map((column) => (
                    <th key={column} className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                      {column}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {rows.map((entry, index) => (
                  <AuditRow key={entry.id ?? `${entry.occurred_at}-${index}`} entry={entry} />
                ))}
              </tbody>
            </table>
          </div>
          <nav className="flex items-center justify-between gap-3 p-4 border-t border-border" aria-label="Audit pagination">
            <button
              type="button"
              disabled={page <= 1}
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
            >
              Previous
            </button>
            <span className="text-xs text-foreground-muted">Page {page} of {lastPage}</span>
            <button
              type="button"
              disabled={page >= lastPage}
              onClick={() => setPage((current) => Math.min(lastPage, current + 1))}
              className="text-sm font-medium px-4 py-2 rounded-[10px] hover:bg-canvas active:scale-95 transition-all disabled:opacity-50 disabled:pointer-events-none"
            >
              Next
            </button>
          </nav>
        </section>
      )}
    </PortalShell>
  )
}

function FilterField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder: string
}) {
  const id = `audit-${label.toLowerCase().replace(/\s+/g, '-')}`
  return (
    <label htmlFor={id} className="flex flex-col gap-1.5">
      <span className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">{label}</span>
      <input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className="bg-canvas border border-border focus:border-foreground focus:ring-1 focus:ring-foreground rounded-[10px] px-3 py-2 text-sm outline-none transition-all"
      />
    </label>
  )
}

function AuditRow({ entry }: { entry: AuditLogEntry }) {
  return (
    <tr className="align-top hover:bg-canvas/50 transition-colors">
      <td className="py-3 px-4 text-sm whitespace-nowrap">{formatDateTime(entry.occurred_at)}</td>
      <td className="py-3 px-4">
        <span className="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold bg-canvas border border-border font-mono">
          {entry.action}
        </span>
      </td>
      <td className="py-3 px-4 text-sm">
        <div className="font-medium">{entry.actor_email ?? 'System'}</div>
        <div className="text-xs text-foreground-muted">{entry.actor_role ?? 'No role'}</div>
      </td>
      <td className="py-3 px-4 text-sm">
        <div className="font-medium">{entry.target_table ?? '-'}</div>
        <div className="text-xs text-foreground-muted font-mono break-all">{entry.target_id ?? '-'}</div>
      </td>
      <td className="py-3 px-4 min-w-[18rem] max-w-[32rem]">
        <pre className="text-xs leading-relaxed bg-canvas border border-border rounded-[10px] p-3 max-h-32 overflow-auto whitespace-pre-wrap break-words">
          {JSON.stringify(entry.payload ?? {}, null, 2)}
        </pre>
      </td>
    </tr>
  )
}

function clean(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

function failureMessage(error: unknown): string {
  if (error instanceof ApiFailure && error.status === 403) return 'You are not allowed to view the audit log.'
  if (error instanceof ApiFailure) return error.body.message
  return 'Audit log could not be loaded.'
}

function formatDateTime(value: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(parsed)
}
