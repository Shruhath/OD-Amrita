import { faker } from '@faker-js/faker'

import { testEventID } from './category'

export type FallbackCreditStateFixture = {
  limit: number
  consumed: number
  remaining: number
  exhausted: boolean
  warning_threshold?: number
}

export type CaptureCreditConfigFixture = {
  event_id: string
  limit: number
  updated_by_user_id: string
  updated_at: string
}

export type CaptureCreditUsageFixture = {
  event_id: string
  user_id: string
  email: string
  name: string | null
  limit: number
  consumed: number
  remaining: number
  exhausted: boolean
}

export type CaptureCreditSummaryFixture = {
  config: CaptureCreditConfigFixture
  data: CaptureCreditUsageFixture[]
}

export function fallbackCreditState(
  overrides: Partial<FallbackCreditStateFixture> = {},
): FallbackCreditStateFixture {
  const limit = overrides.limit ?? 5
  const consumed = overrides.consumed ?? 0
  const remaining = Math.max(0, limit - consumed)
  return {
    limit,
    consumed,
    remaining: overrides.remaining ?? remaining,
    exhausted: overrides.exhausted ?? remaining <= 0,
    ...overrides,
  }
}

export function captureCreditConfig(
  overrides: Partial<CaptureCreditConfigFixture> = {},
): CaptureCreditConfigFixture {
  return {
    event_id: testEventID,
    limit: 5,
    updated_by_user_id: faker.string.uuid(),
    updated_at: '2026-08-03T05:00:00Z',
    ...overrides,
  }
}

export function captureCreditUsage(
  overrides: Partial<CaptureCreditUsageFixture> = {},
): CaptureCreditUsageFixture {
  const limit = overrides.limit ?? 5
  const consumed = overrides.consumed ?? 0
  return {
    event_id: testEventID,
    user_id: faker.string.uuid(),
    email: `credit.${faker.string.alphanumeric(8)}@amrita.edu`.toLowerCase(),
    name: faker.person.fullName(),
    limit,
    consumed,
    remaining: overrides.remaining ?? Math.max(0, limit - consumed),
    exhausted: overrides.exhausted ?? consumed >= limit,
    ...overrides,
  }
}

export function captureCreditSummary(
  overrides: Partial<CaptureCreditSummaryFixture> = {},
): CaptureCreditSummaryFixture {
  return {
    config: captureCreditConfig(),
    data: [captureCreditUsage()],
    ...overrides,
  }
}

export function captureCreditLimitError() {
  return {
    code: 'CAPTURE_CREDIT_LIMIT_REACHED',
    message: 'Fallback credits are exhausted. Use NFC or ask an admin to reset or raise the limit.',
  }
}
