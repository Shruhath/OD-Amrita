import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Branch, CreateTeacherProfileInput, TeacherProfile, ValidationError } from '../../../api/client'
import { BranchChip } from '../../../components/BranchChip'
import { PortalShell } from '../../../components/PortalShell'
import { ApiFailure } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { messageOf } from '../events/format'

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
      <div className="page-heading">
        <div>
          <p className="eyebrow">Administration · Teachers</p>
          <h1>Teachers</h1>
          <p>Elevate an already signed-in user to teacher and scope them to their branches.</p>
        </div>
        <button className="primary" type="button" disabled={!canCreate} onClick={() => setCreating(true)}>
          New teacher
        </button>
      </div>

      {branches.isPending && <p role="status" aria-label="Loading branches">Loading branches…</p>}
      {teachers.isPending && <p role="status" aria-label="Loading teachers">Loading teachers…</p>}

      {branches.isError && (
        <ErrorState message={messageOf(branches.error, 'Branches could not be loaded')} retry={() => void branches.refetch()} />
      )}
      {teachers.isError && (
        <ErrorState message={messageOf(teachers.error, 'Teachers could not be loaded')} retry={() => void teachers.refetch()} />
      )}

      {branches.isSuccess && branchData.length === 0 && (
        <section className="empty-state">
          <h2>No branches available</h2>
          <p>Branches must be configured before a teacher profile can be created.</p>
        </section>
      )}

      {branches.isSuccess && teachers.isSuccess && branchData.length > 0 && teacherData.length === 0 && (
        <section className="empty-state">
          <h2>No teachers yet</h2>
          <p>Create the first teacher profile to begin the roster.</p>
        </section>
      )}

      {teachers.isSuccess && teacherData.length > 0 && (
        <>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">Every teacher profile, ordered by email</caption>
              <thead><tr><th>Email</th><th>Name</th><th>Branches</th></tr></thead>
              <tbody>
                {teacherData.map((teacher) => (
                  <tr key={teacher.user_id}>
                    <th scope="row">{teacher.email}</th>
                    <td>{teacher.name ?? '—'}</td>
                    <td><div className="branch-list">{teacher.branches.map((branch) => <BranchChip branch={branch} key={branch.id} />)}</div></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <nav className="pagination" aria-label="Teachers pages">
            <button type="button" disabled={page === 1 || teachers.isFetching} onClick={() => setPage((current) => Math.max(1, current - 1))}>Previous</button>
            <span aria-live="polite">Page {page} of {totalPages}</span>
            <button type="button" disabled={page >= totalPages || teachers.isFetching} onClick={() => setPage((current) => Math.min(totalPages, current + 1))}>Next</button>
          </nav>
        </>
      )}

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
    </PortalShell>
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

  return (
    <Dialog title="New teacher" onClose={() => !create.isPending && onClose()} initialFocus="first">
      <form onSubmit={submit} noValidate>
        <label htmlFor="teacher-email">Email</label>
        <input
          id="teacher-email"
          type="email"
          value={email}
          onChange={(event) => { setEmail(event.target.value); clearErrors() }}
          aria-invalid={Boolean(fieldErrors.email)}
          aria-describedby={fieldErrors.email ? 'teacher-email-error' : undefined}
        />
        {fieldErrors.email && <p className="field-error" id="teacher-email-error">{fieldErrors.email}</p>}

        <fieldset aria-describedby={fieldErrors.branch_ids ? 'teacher-branch-error' : undefined}>
          <legend>Branches</legend>
          <div className="branch-options">
            {branches.map((branch) => (
              <label key={branch.id} title={branch.name}>
                <input
                  type="checkbox"
                  checked={selected.includes(branch.id)}
                  onChange={() => { toggle(branch.id); clearErrors() }}
                />
                {/* The code is what is shown; the name stays in the accessible
                    name (and in the tooltip on the label) so a checkbox is
                    never just three unexplained letters. */}
                <span><strong>{branch.code}</strong><span className="sr-only">{branch.name}</span></span>
              </label>
            ))}
          </div>
        </fieldset>
        {fieldErrors.branch_ids && <p className="field-error" id="teacher-branch-error">{fieldErrors.branch_ids}</p>}
        {formError && <p className="notice error" role="alert">{formError}</p>}

        <div className="dialog-actions">
          <button type="button" data-dialog-cancel disabled={create.isPending} onClick={onClose}>Cancel</button>
          <button className="primary" type="submit" disabled={!canSubmit}>{create.isPending ? 'Creating…' : 'Create teacher'}</button>
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
