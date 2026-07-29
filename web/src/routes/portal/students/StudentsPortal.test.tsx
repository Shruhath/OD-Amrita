import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, test, vi } from 'vitest'
import { ApiFailure, type EventApi, type StudentRosterImportResult, type ValidationError } from '../../../api/client'
import { AuthContext, type AuthState } from '../../../features/auth/context'
import StudentsPortal from './StudentsPortal'

describe('StudentsPortal', () => {
  test('uploads a CSV and renders reconciliation counts', async () => {
    const user = userEvent.setup()
    const importStudents = vi.fn<NonNullable<EventApi['importStudents']>>().mockResolvedValue(result())
    renderPortal(importStudents)

    await user.upload(screen.getByLabelText('Student roster CSV'), new File(['Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24001,Name,CSE,A,2024\n'], 'students.csv', { type: 'text/csv' }))
    await user.click(screen.getByRole('button', { name: 'Import students' }))

    expect(importStudents).toHaveBeenCalledWith(expect.objectContaining({ name: 'students.csv' }))
    const summary = await screen.findByRole('region', { name: 'Import summary' })
    expect(summary).toHaveTextContent(/Total rows\s*2/i)
    expect(summary).toHaveTextContent(/Inserted\s*2/i)
    expect(summary).toHaveTextContent(/Students in database\s*901/i)
  })

  test('renders typed validation details from the API', async () => {
    const user = userEvent.setup()
    const body: ValidationError = {
      code: 'VALIDATION_FAILED',
      message: 'Student roster validation failed.',
      details: [{ field: 'file', code: 'maximum', message: 'CSV upload must not exceed 2 MiB.' }],
    }
    const importStudents = vi.fn<NonNullable<EventApi['importStudents']>>().mockRejectedValue(new ApiFailure(400, body))
    renderPortal(importStudents)

    await user.upload(screen.getByLabelText('Student roster CSV'), new File(['x'], 'large.csv', { type: 'text/csv' }))
    await user.click(screen.getByRole('button', { name: 'Import students' }))

    expect(await screen.findByText('CSV upload must not exceed 2 MiB.')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('Student roster validation failed.')
  })
})

function renderPortal(importStudents: EventApi['importStudents']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const api = { importStudents } as EventApi
  const auth: AuthState = {
    api,
    identity: { email: 'admin@amrita.edu', subject: 'admin-subject', role: 'admin', name: 'Admin' },
    loading: false,
    error: null,
    signIn: vi.fn(),
    signOut: vi.fn(),
    reload: vi.fn(),
  }
  render(
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={auth}>
        <MemoryRouter>
          <StudentsPortal />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>,
  )
}

function result(overrides: Partial<StudentRosterImportResult> = {}): StudentRosterImportResult {
  return {
    import_id: '77777777-7777-4777-8777-777777777777',
    total_rows: 2,
    inserted_rows: 2,
    updated_rows: 0,
    unchanged_rows: 0,
    invalid_rows: 0,
    database_count: 901,
    errors: [],
    ...overrides,
  }
}
