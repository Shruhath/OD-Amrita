import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'

import type { EventApi } from '../../api/client'
import { Application } from '../../Application'
import { AuthContext } from '../../features/auth/context'
import type { SessionPort } from '../../features/auth/session'
import { createPortalMemoryRouter } from '../../router'
import { server } from '../../test/server'
import MarkScreen from './MarkScreen'
import { captureCreditLimitError, fallbackCreditState } from '../../../tests/support/factories/captureCredits'
import {
  collection,
  dataOnly,
  markError,
  markResult,
  markStatusOpen,
  markStudent,
  markableCategory,
  markableEvent,
} from '../../../tests/support/factories/marking'

function session(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'credit-token'),
    refresh: vi.fn(async () => 'credit-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function renderMarkWithCredit(options: {
  credit?: ReturnType<typeof fallbackCreditState>
  markStatus?: number
  markBody?: unknown
  onStatusRead?: () => void
} = {}) {
  const credit = options.credit ?? fallbackCreditState()
  const markBodies: unknown[] = []

  class FakeNDEFReader {
    addEventListener() {}
    removeEventListener() {}
    scan() {
      return new Promise<void>(() => {})
    }
  }

  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
  ;(window as unknown as { NDEFReader: unknown }).NDEFReader = FakeNDEFReader

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: 'coordinator@amrita.edu',
        name: 'Credit Coordinator',
        role: 'coordinator',
      }),
    ),
    http.get('*/api/v1/events', () => HttpResponse.json(collection([markableEvent()]))),
    http.get('*/api/v1/events/:eventID/categories', () => HttpResponse.json(dataOnly([markableCategory()]))),
    http.get('*/api/v1/mark/status', () => {
      options.onStatusRead?.()
      return HttpResponse.json(markStatusOpen({ fallback_credit: credit }))
    }),
    http.get('*/api/v1/students/search', () => HttpResponse.json(dataOnly([markStudent(1)]))),
    http.post('*/api/v1/mark', async ({ request }) => {
      markBodies.push(await request.json())
      if (options.markStatus) {
        return HttpResponse.json(options.markBody as Record<string, unknown>, { status: options.markStatus })
      }
      return HttpResponse.json(markResult(markStudent(1)), { status: 201 })
    }),
  )

  render(<Application session={session()} router={createPortalMemoryRouter(['/mark'])} />)
  return { markBodies }
}

describe('mark fallback credit behavior', () => {
  test('[P0] shows remaining fallback credits and low-credit warning for camera/search', async () => {
    renderMarkWithCredit({ credit: fallbackCreditState({ limit: 5, consumed: 4, remaining: 1 }) })

    expect(await screen.findByText(/1 fallback credit/i)).toBeInTheDocument()
    expect(screen.getByText(/fallback marks are almost exhausted/i)).toBeInTheDocument()
  })

  test('[P0] disables camera and search when fallback credits are exhausted', async () => {
    renderMarkWithCredit({ credit: fallbackCreditState({ limit: 5, consumed: 5, remaining: 0, exhausted: true }) })

    expect(await screen.findByText(/use nfc or request an admin reset/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /camera|scan/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /search/i })).toBeDisabled()
  })

  test('[P0] stops an active camera session when fallback credits become exhausted', async () => {
    const user = userEvent.setup()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    let credit = fallbackCreditState({ limit: 5, consumed: 4, remaining: 1 })
    const cameraSignals: AbortSignal[] = []
    const api = {
      markableEvents: vi.fn(async () => collection([markableEvent()])),
      markableCategories: vi.fn(async () => dataOnly([markableCategory()])),
      markStatus: vi.fn(async () => markStatusOpen({ fallback_credit: credit })),
      searchStudents: vi.fn(async () => dataOnly([markStudent(1)])),
      mark: vi.fn(async () => markResult(markStudent(1))),
    } as unknown as EventApi
    const camera = {
      status: vi.fn(async () => 'available' as const),
      start: vi.fn(async (_decode, _error, signal: AbortSignal) => {
        cameraSignals.push(signal)
      }),
    }
    const nfc = {
      status: vi.fn(() => 'unsupported' as const),
      ensurePermission: vi.fn(async () => undefined),
      write: vi.fn(async () => undefined),
      read: vi.fn(async () => ({ version: 'dev', token: 'token' })),
      scanSession: vi.fn(async () => undefined),
    }

    render(
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider
          value={{
            api,
            identity: {
              subject: 'credit-coordinator',
              email: 'coordinator@amrita.edu',
              name: 'Credit Coordinator',
              role: 'coordinator',
            },
            loading: false,
            error: null,
            signIn: vi.fn(async () => undefined),
            signOut: vi.fn(async () => undefined),
            reload: vi.fn(async () => undefined),
          }}
        >
          <MarkScreen ports={{ camera, nfc }} />
        </AuthContext.Provider>
      </QueryClientProvider>,
    )

    await user.click(await screen.findByRole('button', { name: /camera|scan/i }))
    await waitFor(() => expect(camera.start).toHaveBeenCalledTimes(1))
    expect(cameraSignals[0].aborted).toBe(false)

    credit = fallbackCreditState({ limit: 5, consumed: 5, remaining: 0, exhausted: true })
    await queryClient.invalidateQueries({ queryKey: ['mark-status'] })

    await waitFor(() => expect(cameraSignals[0].aborted).toBe(true))
    expect(screen.getByRole('button', { name: /camera|scan/i })).toBeDisabled()
  })

  test('[P0] keeps NFC enabled and capable of a green mark when fallback credits are exhausted', async () => {
    const user = userEvent.setup()
    renderMarkWithCredit({ credit: fallbackCreditState({ consumed: 5, remaining: 0, exhausted: true }) })

    const nfc = await screen.findByRole('button', { name: /tap|nfc/i })
    expect(nfc).toBeEnabled()
    await user.click(nfc)

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  test('[P0] renders the combined-credit limit error and refetches status after a raced POST rejection', async () => {
    const user = userEvent.setup()
    let statusReads = 0
    renderMarkWithCredit({
      markStatus: 429,
      markBody: captureCreditLimitError(),
      onStatusRead: () => {
        statusReads += 1
      },
      credit: fallbackCreditState(),
    })

    await user.click(await screen.findByRole('button', { name: /search/i }))
    await user.type(screen.getByRole('searchbox'), 'CB.SC.U4CSE99001')
    await user.click(await screen.findByRole('button', { name: /CB\.SC\.U4CSE99001/i }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/fallback credits are exhausted/i)
    expect(screen.getByRole('alert')).not.toHaveTextContent(/qr/i)
    await waitFor(() => expect(statusReads).toBeGreaterThan(1))
  })

  test('[P1] rejected non-credit marks do not change displayed fallback credit state', async () => {
    renderMarkWithCredit({
      credit: fallbackCreditState({ limit: 5, consumed: 2, remaining: 3 }),
      markStatus: 409,
      markBody: markError('SCAN_DUPLICATE', 'Already marked for this slot.'),
    })

    expect(await screen.findByText(/3 fallback credits/i)).toBeInTheDocument()
    expect(screen.queryByText(/2 fallback credits/i)).not.toBeInTheDocument()
  })
})
