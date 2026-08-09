import { useRef, useState, type ChangeEvent, type FormEvent, type ReactNode } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ApiFailure, type StudentRosterImportResult, type ValidationError } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'
import { messageOf } from '../events/format'
import { motion, AnimatePresence } from 'motion/react'
import { ArrowsClockwise, Check, ClipboardText, File, FolderOpen, GraduationCap, Sparkle, Warning } from '@phosphor-icons/react'

type CountField = 'total_rows' | 'inserted_rows' | 'updated_rows' | 'unchanged_rows' | 'invalid_rows' | 'database_count'

const countCards: Array<[CountField, string, ReactNode]> = [
  ['total_rows', 'Total rows', <ClipboardText key="total_rows" size={24} weight="duotone" aria-hidden="true" />],
  ['inserted_rows', 'Inserted', <Sparkle key="inserted_rows" size={24} weight="duotone" aria-hidden="true" />],
  ['updated_rows', 'Updated', <ArrowsClockwise key="updated_rows" size={24} weight="duotone" aria-hidden="true" />],
  ['unchanged_rows', 'Unchanged', <Check key="unchanged_rows" size={24} weight="bold" aria-hidden="true" />],
  ['invalid_rows', 'Invalid', <Warning key="invalid_rows" size={24} weight="duotone" aria-hidden="true" />],
  ['database_count', 'Students in database', <GraduationCap key="database_count" size={24} weight="duotone" aria-hidden="true" />],
]

export default function StudentsPortal() {
  const { api } = useAuth()
  const fileRef = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<File | null>(null)
  const [fieldError, setFieldError] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const [result, setResult] = useState<StudentRosterImportResult | null>(null)

  const importRoster = useMutation({
    mutationFn: (selected: File) => api.importStudents(selected),
    onSuccess(next) {
      setResult(next)
      setFieldError(null)
      setFormError(null)
      if (fileRef.current) fileRef.current.value = ''
      setFile(null)
    },
    onError(cause) {
      setResult(null)
      setFormError(messageOf(cause, 'The roster could not be imported'))
      if (cause instanceof ApiFailure && cause.body.code === 'VALIDATION_FAILED' && 'details' in cause.body) {
        const details = (cause.body as ValidationError).details
        setFieldError(details.find((detail) => detail.field === 'file')?.message ?? details[0]?.message ?? null)
      }
    },
  })

  const chooseFile = (event: ChangeEvent<HTMLInputElement>) => {
    const selected = event.target.files?.[0] ?? null
    setFile(selected)
    setFieldError(null)
    setFormError(null)
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    setFieldError(null)
    setFormError(null)
    if (!file) {
      setFieldError('Choose a CSV file')
      return
    }
    importRoster.mutate(file)
  }

  return (
    <PortalShell>
      {/* Page Header */}
      <div className="mb-8">
        <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-1">Administration · Students</p>
        <h1 className="text-3xl font-bold tracking-tight mb-2">Student roster</h1>
        <p className="text-foreground-muted">Load the event roster from CSV and reconcile rows by canonical roll number.</p>
      </div>

      {/* Import Form */}
      <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm mb-6">
        <h2 className="text-lg font-bold tracking-tight mb-1">Import CSV</h2>
        <p className="text-sm text-foreground-muted mb-5">Upload a student roster CSV file. Rows are reconciled by roll number.</p>

        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="student-roster-csv" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">
              Student roster CSV
            </label>
            <div className={`relative border-2 border-dashed rounded-[10px] p-6 text-center cursor-pointer transition-all ${
              file
                ? 'border-status-success-fg/40 bg-status-success-bg'
                : fieldError
                  ? 'border-status-error-fg/40 bg-status-error-bg'
                  : 'border-border hover:border-foreground/30 bg-canvas'
            }`}>
              <input
                ref={fileRef}
                id="student-roster-csv"
                type="file"
                accept=".csv,text/csv"
                onChange={chooseFile}
                aria-invalid={Boolean(fieldError)}
                aria-describedby={fieldError ? 'student-roster-csv-error' : undefined}
                className="absolute inset-0 w-full h-full opacity-0 cursor-pointer"
              />
              {file ? (
                <div className="flex flex-col items-center gap-2">
                  <File size={24} weight="duotone" aria-hidden="true" className="text-status-success-fg" />
                  <p className="text-sm font-semibold text-status-success-fg">{file.name}</p>
                  <p className="text-xs text-status-success-fg/70">{(file.size / 1024).toFixed(1)} KB · Click to change</p>
                </div>
              ) : (
                <div className="flex flex-col items-center gap-2">
                  <FolderOpen size={24} weight="duotone" aria-hidden="true" className="text-foreground-muted" />
                  <p className="text-sm font-medium text-foreground-muted">Drop a CSV file here or <span className="text-foreground font-semibold underline underline-offset-2">browse</span></p>
                  <p className="text-xs text-foreground-muted">Accepts .csv files</p>
                </div>
              )}
            </div>
            {fieldError && <p className="text-sm font-medium text-status-error-fg" id="student-roster-csv-error">{fieldError}</p>}
          </div>

          {formError && (
            <div className="p-4 bg-status-error-bg rounded-[10px] border border-status-error-fg/20" role="alert">
              <p className="text-sm font-semibold text-status-error-fg">{formError}</p>
            </div>
          )}

          <button
            className="primary self-start"
            type="submit"
            disabled={importRoster.isPending}
          >
            {importRoster.isPending ? (
              <span className="flex items-center gap-2">
                <span className="animate-spin inline-block w-4 h-4 border-2 border-current border-t-transparent rounded-full" />
                Importing…
              </span>
            ) : 'Import students'}
          </button>
        </form>
      </section>

      {/* Import Summary */}
      <AnimatePresence>
        {result && (
          <motion.div
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ type: 'spring', damping: 25, stiffness: 300 }}
          >
            <ImportSummary result={result} />
          </motion.div>
        )}
      </AnimatePresence>
    </PortalShell>
  )
}

function ImportSummary({ result }: { result: StudentRosterImportResult }) {
  return (
    <section className="bg-surface border border-border rounded-[10px] p-6 shadow-sm" aria-label="Import summary">
      <h2 className="text-lg font-bold tracking-tight mb-5">Import summary</h2>

      <dl className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-4 mb-6">
        {countCards.map(([field, label, icon]) => (
          <div key={field} className="bg-canvas rounded-[10px] border border-border p-4 flex flex-col items-center text-center gap-1">
            <span className="text-2xl mb-1">{icon}</span>
            <dt className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">{label}</dt>
            <dd className="text-3xl font-black tracking-tight">{result[field]}</dd>
          </div>
        ))}
      </dl>

      {result.errors.length > 0 && (
        <div>
          <p className="text-sm font-semibold text-status-error-fg mb-3 flex items-center gap-2">
            <Warning size={18} weight="bold" aria-hidden="true" />
            {result.errors.length} row{result.errors.length !== 1 ? 's' : ''} had errors
          </p>
          <div className="overflow-x-auto rounded-[10px] border border-border">
            <table className="w-full text-left border-collapse" aria-label="Import errors">
              <thead className="bg-canvas border-b border-border">
                <tr>
                  {['Row', 'Field', 'Code', 'Message'].map((col) => (
                    <th key={col} className="py-2.5 px-4 text-xs font-semibold uppercase tracking-wider text-foreground-muted whitespace-nowrap">
                      {col}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {result.errors.map((error) => (
                  <tr key={`${error.row}-${error.field}-${error.code}-${error.message}`} className="hover:bg-canvas/50 transition-colors">
                    <td className="py-3 px-4 text-sm tabular-nums">{error.row}</td>
                    <td className="py-3 px-4 text-sm font-mono">{error.field}</td>
                    <td className="py-3 px-4">
                      <span className="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-bold bg-status-error-bg text-status-error-fg">{error.code}</span>
                    </td>
                    <td className="py-3 px-4 text-sm text-foreground-muted">{error.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </section>
  )
}
