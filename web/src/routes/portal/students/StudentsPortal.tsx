import { useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ApiFailure, type StudentRosterImportResult, type ValidationError } from '../../../api/client'
import { PortalShell } from '../../../components/PortalShell'
import { useAuth } from '../../../features/auth/context'
import { messageOf } from '../events/format'

type CountField = 'total_rows' | 'inserted_rows' | 'updated_rows' | 'unchanged_rows' | 'invalid_rows' | 'database_count'

const countCards: Array<[CountField, string]> = [
  ['total_rows', 'Total rows'],
  ['inserted_rows', 'Inserted'],
  ['updated_rows', 'Updated'],
  ['unchanged_rows', 'Unchanged'],
  ['invalid_rows', 'Invalid'],
  ['database_count', 'Students in database'],
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
      <div className="page-heading">
        <div>
          <p className="eyebrow">Administration · Students</p>
          <h1>Student roster</h1>
          <p>Load the event roster from CSV and reconcile rows by canonical roll number.</p>
        </div>
      </div>

      <section className="detail-section roster-import">
        <h2>Import CSV</h2>
        <form className="roster-form" onSubmit={submit} noValidate>
          <label htmlFor="student-roster-csv">Student roster CSV</label>
          <input
            ref={fileRef}
            id="student-roster-csv"
            type="file"
            accept=".csv,text/csv"
            onChange={chooseFile}
            aria-invalid={Boolean(fieldError)}
            aria-describedby={fieldError ? 'student-roster-csv-error' : undefined}
          />
          {fieldError && <p className="field-error" id="student-roster-csv-error">{fieldError}</p>}
          <button className="primary" type="submit" disabled={importRoster.isPending}>
            {importRoster.isPending ? 'Importing…' : 'Import students'}
          </button>
        </form>
        {formError && <p className="notice error" role="alert">{formError}</p>}
      </section>

      {result && <ImportSummary result={result} />}
    </PortalShell>
  )
}

function ImportSummary({ result }: { result: StudentRosterImportResult }) {
  return (
    <section className="detail-section import-summary" aria-label="Import summary">
      <h2>Import summary</h2>
      <dl className="summary-grid">
        {countCards.map(([field, label]) => (
          <div key={field}>
            <dt>{label}</dt>
            <dd>{result[field]}</dd>
          </div>
        ))}
      </dl>
      {result.errors.length > 0 && (
        <div className="table-wrap import-errors">
          <table aria-label="Import errors">
            <thead><tr><th>Row</th><th>Field</th><th>Code</th><th>Message</th></tr></thead>
            <tbody>
              {result.errors.map((error) => (
                <tr key={`${error.row}-${error.field}-${error.code}-${error.message}`}>
                  <td>{error.row}</td>
                  <td>{error.field}</td>
                  <td>{error.code}</td>
                  <td>{error.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
