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
  captureCreditSummary,
  captureCreditUsage,
} from '../../../../tests/support/factories/captureCredits'

const eventName = 'Onam Celebrations'
const adminEventsCollectionPath = /\/api\/v1\/admin\/events(?:\?.*)?$/
const adminEventItemPath = /\/api\/v1\/admin\/events\/[^/?]+$/
const adminEventCategoriesPath = /\/api\/v1\/admin\/events\/[^/?]+\/categories$/
const adminEventCoordinatorsPath = /\/api\/v1\/admin\/events\/[^/?]+\/coordinators$/
const adminEventCreditsPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits$/
const adminEventCreditLimitPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits\/coordinators\/[^/?]+\/limit$/
const adminEventCreditResetPath = /\/api\/v1\/admin\/events\/[^/?]+\/capture-credits\/coordinators\/[^/?]+\/reset$/
const adminEventAttendancePath = /\/api\/v1\/admin\/events\/[^/?]+\/attendance(?:\?.*)?$/

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
    data: [
      captureCreditUsage({
        event_id: testEventID,
        user_id: '11111111-1111-4111-8111-111111111111',
        email: 'coordinator-a@amrita.edu',
        name: 'Coordinator A',
        consumed: 4,
        remaining: 1,
      }),
      captureCreditUsage({
        event_id: testEventID,
        user_id: '22222222-2222-4222-8222-222222222222',
        email: 'coordinator-b@amrita.edu',
        name: 'Coordinator B',
        limit: 20,
        consumed: 0,
        remaining: 20,
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
    http.get(adminEventAttendancePath, () =>
      HttpResponse.json({ data: [], page: 1, page_size: 20, total: 0 }),
    ),
    http.get(adminEventCreditsPath, () => HttpResponse.json(summary)),
    http.put(adminEventCreditLimitPath, async ({ request }) => {
      calls.push('limit')
      const input = (await request.json()) as { limit: number }
      const userID = new URL(request.url).pathname.split('/').at(-2)
      const updated = { ...summary.data.find((row) => row.user_id === userID)!, limit: input.limit }
      updated.remaining = Math.max(0, updated.limit - updated.consumed)
      updated.exhausted = updated.consumed >= updated.limit
      summary = { ...summary, data: summary.data.map((row) => (row.user_id === updated.user_id ? updated : row)) }
      return HttpResponse.json(updated)
    }),
    http.post(adminEventCreditResetPath, () => {
      calls.push('reset')
      const reset = { ...summary.data[0], consumed: 0, remaining: summary.data[0].limit, exhausted: false }
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

  test('[P0] persists one coordinator cap change and refreshes only that row', async () => {
    const user = userEvent.setup()
    const { calls } = renderEventDetail()

    const row = (await screen.findByText('Coordinator A')).closest('li')!
    const input = within(row).getByLabelText(/cap/i)
    await user.clear(input)
    await user.type(input, '8')
    await user.click(within(row).getByRole('button', { name: /edit cap/i }))

    expect(calls).toContain('limit')
    expect(await screen.findByText(/limit 8/i)).toBeInTheDocument()
    expect(screen.getByText(/limit 20/i)).toBeInTheDocument()
  })

  test('[P1] rejects fractional cap input before sending the request', async () => {
    const user = userEvent.setup()
    const { calls } = renderEventDetail()

    const row = (await screen.findByText('Coordinator A')).closest('li')!
    const input = within(row).getByLabelText(/cap/i)
    await user.clear(input)
    await user.type(input, '1.5')
    await user.click(within(row).getByRole('button', { name: /edit cap/i }))

    expect(await screen.findByText(/whole number/i)).toBeInTheDocument()
    expect(calls).not.toContain('limit')
  })

  test('[P0] resets one coordinator without a page reload', async () => {
    const user = userEvent.setup()
    const { calls } = renderEventDetail()

    await user.click(await screen.findByRole('button', { name: /reset coordinator-a@amrita.edu/i }))
    expect(calls).not.toContain('reset')
    await user.click(await screen.findByRole('button', { name: /confirm reset/i }))

    expect(calls).toContain('reset')
    const section = await screen.findByRole('region', { name: /fallback credits/i })
    expect(within(section).getByText(/0 used/i)).toBeInTheDocument()
  })

  test('[P1] renders archived events read-only with no cap or reset controls', async () => {
    renderEventDetail('archived')

    const section = await screen.findByRole('region', { name: /fallback credits/i })
    expect(within(section).getByText('coordinator-a@amrita.edu')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /edit cap/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /reset coordinator/i })).not.toBeInTheDocument()
  })
})
