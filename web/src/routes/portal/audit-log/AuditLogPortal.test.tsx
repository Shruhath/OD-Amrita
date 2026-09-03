import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, test, vi } from 'vitest'
import { ApiFailure, type AuditLogPage, type EventApi } from '../../../api/client'
import { AuthContext, type AuthState } from '../../../features/auth/context'
import AuditLogPortal from './AuditLogPortal'

type AuditLogEntry = AuditLogPage['data'][number]

describe('AuditLogPortal', () => {
  test('renders audit entries with actor, target and payload details', async () => {
    renderPortal(vi.fn<NonNullable<EventApi['adminAuditLog']>>().mockResolvedValue(auditPage()))

    expect(await screen.findByRole('heading', { name: 'Audit log' })).toBeInTheDocument()
    const table = await screen.findByRole('table', { name: 'Audit log entries' })
    expect(table).toHaveTextContent('app_setting.updated')
    expect(table).toHaveTextContent('admin@amrita.edu')
    expect(table).toHaveTextContent('app_settings')
    expect(table).toHaveTextContent('extended_slots_enabled')
    expect(table).toHaveTextContent('"previous_value": false')
    expect(table).toHaveTextContent('"new_value": true')
  })

  test('passes explicit filters and resets pagination when a filter changes', async () => {
    const user = userEvent.setup()
    const adminAuditLog = vi.fn<NonNullable<EventApi['adminAuditLog']>>().mockResolvedValue(auditPage({ total: 75 }))
    renderPortal(adminAuditLog)

    expect(await screen.findByRole('table', { name: 'Audit log entries' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Next' }))
    await waitFor(() => expect(adminAuditLog).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2 })))

    await user.type(screen.getByLabelText('Action'), 'od.created')

    await waitFor(() =>
      expect(adminAuditLog).toHaveBeenLastCalledWith(expect.objectContaining({
        action: 'od.created',
        page: 1,
        pageSize: 50,
      })),
    )
  })

  test('shows the audit-specific forbidden message', async () => {
    const adminAuditLog = vi
      .fn<NonNullable<EventApi['adminAuditLog']>>()
      .mockRejectedValue(new ApiFailure(403, { code: 'RBAC_FORBIDDEN', message: 'Forbidden.' }))
    renderPortal(adminAuditLog)

    expect(await screen.findByRole('alert')).toHaveTextContent('You are not allowed to view the audit log.')
  })
})

function renderPortal(adminAuditLog: EventApi['adminAuditLog']) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const api = { adminAuditLog } as EventApi
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
          <AuditLogPortal />
        </MemoryRouter>
      </AuthContext.Provider>
    </QueryClientProvider>,
  )
}

function auditPage(overrides: Partial<AuditLogPage> = {}): AuditLogPage {
  return {
    data: [auditEntry()],
    page: 1,
    page_size: 50,
    total: 1,
    ...overrides,
  }
}

function auditEntry(overrides: Partial<AuditLogEntry> = {}): AuditLogEntry {
  return {
    id: '97000000-0000-4000-8000-000000000001',
    action: 'app_setting.updated',
    actor_email: 'admin@amrita.edu',
    actor_role: 'admin',
    target_table: 'app_settings',
    target_id: 'extended_slots_enabled',
    occurred_at: '2026-09-03T12:30:00Z',
    payload: { previous_value: false, new_value: true },
    ...overrides,
  }
}
