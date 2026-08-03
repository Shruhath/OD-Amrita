import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'

import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { createEventFixture, testBranches } from '../../../../tests/support/factories/event'
import { createCategoryFixture, testEventID } from '../../../../tests/support/factories/category'
import {
  captureCreditConfig,
  captureCreditSummary,
  captureCreditUsage,
} from '../../../../tests/support/factories/captureCredits'

const eventName = 'Onam Celebrations'
const adminEventsCollectionPath = /\/api\/v1\/admin\/events(?:\?.*)?$/
const adminEventItemPath = /\/api\/v1\/admin\/events\/[^/?]+$/
const adminEventCategoriesPath = /\/api\/v1\/admin\/events\/[^/?]+\/categories$/
const adminEventCoordinatorsPath = /\/api\/v1\/admin\/events\/[^/?]+\/coordinators$/
const adminEventCreditsPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits$/
const adminEventCreditConfigPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits\/config$/
const adminEventCreditResetPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits\/coordinators\/[^/?]+\/reset$/

function session(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'credit-admin-token'),
    refresh: vi.fn(async () => 'credit-admin-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function renderEventDetail(status: 'active' | 'archived' = 'active') {
  let summary = captureCreditSummary({
    config: captureCreditConfig({ event_id: testEventID, limit: 5 }),
    data: [
      captureCreditUsage({
        event_id: testEventID,
        email: 'coordinator-a@amrita.edu',
        name: 'Coordinator A',
        consumed: 4,
        remaining: 1,
      }),
    ],
  })
  const calls: string[] = []
  const event = createEventFixture({ id: testEventID, name: eventName, status })

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        subject: 'credit-admin',
        email: 'admin@amrita.edu',
        name: 'Credit Admin',
        role: 'admin',
      }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: testBranches })),
    http.get(adminEventsCollectionPath, () =>
      HttpResponse.json({ data: [event], page: 1, page_size: 20, total: 1 }),
    ),
    http.get(adminEventItemPath, () => HttpResponse.json(event)),
    http.get(adminEventCategoriesPath, () =>
      HttpResponse.json({ data: [createCategoryFixture({ event_id: testEventID })] }),
    ),
    http.get(adminEventCoordinatorsPath, () => HttpResponse.json({ data: [] })),
    http.get(adminEventCreditsPath, () => HttpResponse.json(summary)),
    http.put(adminEventCreditConfigPath, async ({ request }) => {
      calls.push('config')
      const input = (await request.json()) as { limit: number }
      summary = { ...summary, config: captureCreditConfig({ event_id: testEventID, limit: input.limit }) }
      return HttpResponse.json(summary.config)
    }),
    http.post(adminEventCreditResetPath, () => {
      calls.push('reset')
      const reset = { ...summary.data[0], consumed: 0, remaining: summary.config.limit, exhausted: false }
      summary = { ...summary, data: [reset] }
      return HttpResponse.json(reset)
    }),
  )

  render(
    <Application
      session={session()}
      router={createPortalMemoryRouter([`/portal/events/${testEventID}`])}
    />,
  )
  return { calls }
}

describe('event detail fallback credits', () => {
  test('[P0] lists each assigned coordinator credit state in server order', async () => {
    renderEventDetail()

    const section = await screen.findByRole('region', { name: /fallback credits/i })
    expect(within(section).getByText('coordinator-a@amrita.edu')).toBeInTheDocument()
    expect(within(section).getByText(/4 used/i)).toBeInTheDocument()
    expect(within(section).getByText(/1 remaining/i)).toBeInTheDocument()
  })

  test('[P0] persists a cap change and refreshes the displayed value', async () => {
    const user = userEvent.setup()
    const { calls } = renderEventDetail()

    const input = await screen.findByLabelText(/fallback credit cap/i)
    await user.clear(input)
    await user.type(input, '8')
    await user.click(screen.getByRole('button', { name: /save cap/i }))

    expect(calls).toContain('config')
    expect(await screen.findByText(/cap: 8/i)).toBeInTheDocument()
  })

  test('[P0] resets one coordinator without a page reload', async () => {
    const user = userEvent.setup()
    const { calls } = renderEventDetail()

    await user.click(await screen.findByRole('button', { name: /reset coordinator-a@amrita.edu/i }))

    expect(calls).toContain('reset')
    const section = await screen.findByRole('region', { name: /fallback credits/i })
    expect(within(section).getByText(/0 used/i)).toBeInTheDocument()
  })

  test('[P1] renders archived events read-only with no cap or reset controls', async () => {
    renderEventDetail('archived')

    const section = await screen.findByRole('region', { name: /fallback credits/i })
    expect(within(section).getByText('coordinator-a@amrita.edu')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /save cap/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /reset coordinator/i })).not.toBeInTheDocument()
  })
})
