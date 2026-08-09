import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Branch, CreateTeacherProfileInput, TeacherProfile, ValidationError } from '../../../api/client'
import { BranchChip } from '../../../components/BranchChip'
import { PortalShell } from '../../../components/PortalShell'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { messageOf } from '../events/format'
import { AnimatePresence, motion } from 'motion/react'
import { Buildings, ChalkboardTeacher, CaretLeft, CaretRight, Check, EnvelopeSimple, Plus, UsersThree, X } from '@phosphor-icons/react'
import { Button } from '../../../components/ui/button'

const branchKey = ['admin', 'branches'] as const
const teacherKeyRoot = ['admin', 'teachers'] as const
const pageSize = 20
const teacherKey = (page: number) => [...teacherKeyRoot, page, pageSize] as const

export default function TeachersPortal() {
  const { api } = useAuth()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState(false)
  const [page, setPage] = useState(1)

  const branches = useQuery({ queryKey: branchKey, queryFn: api.branches })
  const currentTeacherKey = teacherKey(page)
  const teachers = useQuery({ queryKey: currentTeacherKey, queryFn: () => api.teachers(page, pageSize) })

  const branchData = branches.data?.data ?? []
  const teacherData = teachers.data?.data ?? []
  const canCreate = branches.isSuccess && branchData.length > 0
  const totalPages = Math.max(1, Math.ceil((teachers.data?.total ?? 0) / (teachers.data?.page_size ?? pageSize)))

  return (
    <PortalShell>
      {/* Page Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-8">
        <div>
          <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Administration · Teachers</p>
          <h1 className="text-3xl font-bold tracking-tight mb-2">Teachers</h1>
          <p className="text-foreground-muted">Elevate an already signed-in user to teacher and scope them to their branches.</p>
        </div>
        <Button disabled={!canCreate} onClick={() => setCreating(true)} className="w-full shrink-0 sm:w-auto">
          <Plus size={18} weight="bold" aria-hidden="true" />
          New teacher
        </Button>
      </div>

      <section className="mb-8 grid grid-cols-1 gap-3 sm:grid-cols-3" aria-label="Teacher directory summary">
        <SummaryCard icon={<UsersThree size={21} weight="duotone" aria-hidden="true" />} label="Teachers shown" value={teachers.data?.data.length ?? '—'} />
        <SummaryCard icon={<Buildings size={21} weight="duotone" aria-hidden="true" />} label="Available branches" value={branchData.length || '—'} />
        <SummaryCard icon={<Check size={21} weight="bold" aria-hidden="true" />} label="Page" value={`${page} / ${totalPages}`} />
      </section>

      {/* Loading states */}
      {branches.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading branches…
        </div>
      )}
      {teachers.isPending && (
        <div className="flex items-center gap-3 text-sm text-status-info-fg bg-status-info-bg p-4 rounded-[10px] mb-6" role="status">
          <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
          Loading teachers…
        </div>
      )}

      {/* Error states */}
      {branches.isError && (
        <ErrorState message={messageOf(branches.error, 'Branches could not be loaded')} retry={() => void branches.refetch()} />
      )}
      {teachers.isError && (
        <ErrorState message={messageOf(teachers.error, 'Teachers could not be loaded')} retry={() => void teachers.refetch()} />
      )}

      {/* Empty states */}
      {branches.isSuccess && branchData.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3 mb-8">
          <Buildings size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">No branches available</h2>
          <p className="text-foreground-muted">Branches must be configured before a teacher profile can be created.</p>
        </div>
      )}

      {branches.isSuccess && teachers.isSuccess && branchData.length > 0 && teacherData.length === 0 && (
        <div className="bg-surface border border-border rounded-[10px] p-12 flex flex-col items-center text-center gap-3 mb-8">
          <ChalkboardTeacher size={40} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
          <h2 className="text-xl font-bold tracking-tight">No teachers yet</h2>
          <p className="text-foreground-muted">Create the first teacher profile to begin the roster.</p>
        </div>
      )}

      {/* Teacher table */}
      {teachers.isSuccess && teacherData.length > 0 && (
        <div className="flex flex-col gap-4">
          <div className="hidden overflow-hidden rounded-2xl border border-border bg-surface shadow-sm md:block">
            <div>
              <table className="w-full table-fixed text-left border-collapse">
                <caption className="sr-only">Every teacher profile, ordered by email</caption>
                <thead className="bg-canvas border-b border-border">
                  <tr>
                    {['Email', 'Name', 'Branches'].map((col) => (
                      <th key={col} className="py-3 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                        {col}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {teacherData.map((teacher) => (
                    <tr key={teacher.user_id} className="hover:bg-canvas/50 transition-colors">
                      <th scope="row" className="py-4 px-4 font-medium text-sm">{teacher.email}</th>
                      <td className="py-4 px-4 text-sm text-foreground-muted">{teacher.name ?? '—'}</td>
                      <td className="py-4 px-4">
                        <div className="flex flex-wrap gap-2">
                          {teacher.branches.map((branch) => <BranchChip branch={branch} key={branch.id} />)}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          <div className="grid gap-3 md:hidden">
            {teacherData.map((teacher) => (
              <article key={teacher.user_id} className="rounded-2xl border border-border bg-surface p-4 shadow-sm">
                <div className="flex items-start gap-3">
                  <span className="grid h-10 w-10 shrink-0 place-items-center rounded-xl bg-accent text-primary"><EnvelopeSimple size={19} weight="duotone" aria-hidden="true" /></span>
                  <div className="min-w-0 flex-1">
                    <h2 className="break-words text-sm font-bold">{teacher.email}</h2>
                    <p className="mt-1 break-words text-sm text-foreground-muted">{teacher.name ?? '—'}</p>
                  </div>
                </div>
                <div className="mt-4 border-t border-border pt-3">
                  <p className="mb-2 text-[11px] font-bold uppercase tracking-wider text-foreground-muted">Branch access</p>
                  <div className="flex flex-wrap gap-2">{teacher.branches.map((branch) => <BranchChip branch={branch} key={branch.id} />)}</div>
                </div>
              </article>
            ))}
          </div>

          <nav className="flex items-center justify-between gap-3 rounded-2xl border border-border bg-surface p-3 shadow-sm" aria-label="Teachers pages">
            <Button variant="outline" size="sm" disabled={page === 1 || teachers.isFetching} onClick={() => setPage((current) => Math.max(1, current - 1))}>
              <CaretLeft size={16} aria-hidden="true" /> <span className="hidden sm:inline">Previous</span>
            </Button>
            <span aria-live="polite" className="text-sm font-medium text-foreground-muted">Page {page} of {totalPages}</span>
            <Button variant="outline" size="sm" disabled={page >= totalPages || teachers.isFetching} onClick={() => setPage((current) => Math.min(totalPages, current + 1))}>
              <span className="hidden sm:inline">Next</span> <CaretRight size={16} aria-hidden="true" />
            </Button>
          </nav>
        </div>
      )}

      {/* Create teacher modal */}
      <AnimatePresence>
        {creating && (
          <CreateDialog
            branches={branchData}
            onClose={() => setCreating(false)}
            onCreated={() => {
              void queryClient.invalidateQueries({ queryKey: teacherKeyRoot })
              setPage(1)
              setCreating(false)
            }}
          />
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

function CreateDialog({ branches, onClose, onCreated }: { branches: Branch[]; onClose(): void; onCreated(teacher: TeacherProfile): void }) {
  const { api } = useAuth()
  const [email, setEmail] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string | null>(null)
  const create = useMutation({ mutationFn: (input: CreateTeacherProfileInput) => api.createTeacher(input), onSuccess: onCreated })
  const canSubmit = Boolean(email.trim() && selected.length > 0) && !create.isPending

  const toggle = (id: string) => setSelected((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])
  const clearErrors = () => { setFieldErrors({}); setFormError(null) }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const errors: Record<string, string> = {}
    if (!email.trim()) errors.email = 'Enter an email address'
    if (selected.length === 0) errors.branch_ids = 'Select at least one branch'
    setFieldErrors(errors)
    setFormError(null)
    if (Object.keys(errors).length > 0) return

    create.mutate({ email: email.trim(), branch_ids: selected }, {
      onError(cause) {
        setFormError(messageOf(cause, 'The teacher profile could not be created'))
        if (cause instanceof ApiFailure && cause.body.code === 'VALIDATION_FAILED' && 'details' in cause.body && Array.isArray(cause.body.details)) {
          const mapped: Record<string, string> = {}
          for (const detail of (cause.body as ValidationError).details) mapped[detail.field.replace(/\[\d+\]$/, '')] = detail.message
          setFieldErrors(mapped)
        }
      },
    })
  }

  // Focus trap
  const dialogRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const node = dialogRef.current
    if (!node) return
    const focusable = () => Array.from(node.querySelectorAll<HTMLElement>('button:not([disabled]), input:not([disabled])'))
    focusable()[0]?.focus()
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); onClose(); return }
      if (event.key !== 'Tab') return
      const items = focusable()
      if (items.length === 0) return
      const current = items.indexOf(document.activeElement as HTMLElement)
      const next = event.shiftKey ? (current <= 0 ? items.length - 1 : current - 1) : (current >= items.length - 1 ? 0 : current + 1)
      event.preventDefault()
      items[next]?.focus()
    }
    document.addEventListener('keydown', keydown)
    return () => document.removeEventListener('keydown', keydown)
  }, [onClose])

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      exit={{ opacity: 0 }}
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-foreground/20 backdrop-blur-sm"
    >
      <motion.div
        ref={dialogRef}
        initial={{ opacity: 0, y: 20, scale: 0.95 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        exit={{ opacity: 0, y: 20, scale: 0.95 }}
        transition={{ type: 'spring', damping: 25, stiffness: 300 }}
        className="max-h-[calc(100dvh-2rem)] w-full max-w-lg overflow-y-auto rounded-2xl border border-border bg-surface shadow-[0_24px_80px_rgb(0_0_0/0.24)]"
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-teacher-dialog-title"
      >
        <div className="flex items-start justify-between gap-4 border-b border-border bg-canvas/50 px-5 py-5 sm:px-6">
          <div>
          <h2 id="new-teacher-dialog-title" className="text-xl font-bold tracking-tight">New teacher</h2>
          <p className="mt-1 text-sm text-foreground-muted">Grant branch-scoped teaching access to an existing account.</p>
          </div>
          <button type="button" onClick={onClose} disabled={create.isPending} aria-label="Close dialog" className="grid h-9 w-9 shrink-0 place-items-center rounded-lg border border-border text-foreground-muted hover:bg-surface-muted hover:text-foreground">
            <X size={17} aria-hidden="true" />
          </button>
        </div>
        <div className="p-5 sm:p-6">
          <form onSubmit={submit} noValidate className="flex flex-col gap-5">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="teacher-email" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Email</label>
              <input
                id="teacher-email"
                type="email"
                value={email}
                onChange={(event) => { setEmail(event.target.value); clearErrors() }}
                aria-invalid={Boolean(fieldErrors.email)}
                aria-describedby={fieldErrors.email ? 'teacher-email-error' : undefined}
                placeholder="name@amrita.edu"
                className={`w-full bg-canvas border focus:ring-1 rounded-[10px] px-3 py-2 text-sm outline-none transition-all ${
                  fieldErrors.email
                    ? 'border-status-error-fg focus:border-status-error-fg focus:ring-status-error-fg'
                    : 'border-border focus:border-foreground focus:ring-foreground'
                }`}
              />
              {fieldErrors.email && <p className="text-sm font-medium text-status-error-fg" id="teacher-email-error">{fieldErrors.email}</p>}
            </div>

            <fieldset
              aria-describedby={fieldErrors.branch_ids ? 'teacher-branch-error' : undefined}
              className="flex flex-col gap-2"
            >
              <legend className="text-xs font-semibold uppercase tracking-wider text-foreground-muted mb-1">Branches</legend>
              <div className="flex flex-wrap gap-2">
                {branches.map((branch) => (
                  <label
                    key={branch.id}
                    title={branch.name}
                    className={`relative flex items-center justify-center min-w-16 px-3 py-2 rounded-[10px] cursor-pointer border select-none transition-all active:scale-95 ${
                      selected.includes(branch.id)
                        ? 'bg-foreground text-canvas border-foreground font-bold'
                        : 'bg-canvas text-foreground border-border hover:bg-surface font-medium'
                    }`}
                  >
                    <input
                      type="checkbox"
                      className="sr-only"
                      checked={selected.includes(branch.id)}
                      onChange={() => { toggle(branch.id); clearErrors() }}
                    />
                    <span>
                      <strong>{branch.code}</strong>
                      <span className="sr-only">{branch.name}</span>
                    </span>
                  </label>
                ))}
              </div>
              {fieldErrors.branch_ids && <p className="text-sm font-medium text-status-error-fg" id="teacher-branch-error">{fieldErrors.branch_ids}</p>}
            </fieldset>

            {formError && (
              <div className="p-4 bg-status-error-bg rounded-[10px] border border-status-error-fg/20" role="alert">
                <p className="text-sm font-semibold text-status-error-fg">{formError}</p>
              </div>
            )}

            <div className="flex flex-col-reverse sm:flex-row gap-3 pt-4 border-t border-border">
              <button
                type="button"
                data-dialog-cancel
                disabled={create.isPending}
                onClick={onClose}
                className="flex-1 border border-border bg-canvas hover:bg-surface text-foreground px-4 py-2.5 rounded-[10px] font-medium active:scale-95 transition-all disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                className="flex-1 bg-foreground text-canvas px-4 py-2.5 rounded-[10px] font-semibold active:scale-95 transition-all disabled:opacity-50"
                type="submit"
                disabled={!canSubmit}
              >
                {create.isPending ? 'Creating…' : 'Create teacher'}
              </button>
            </div>
          </form>
        </div>
      </motion.div>
    </motion.div>
  )
}

function ErrorState({ message, retry }: { message: string; retry(): void }) {
  return (
    <div className="flex items-center justify-between bg-status-error-bg text-status-error-fg p-4 rounded-[10px] border border-status-error-fg/20 mb-6" role="alert">
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
