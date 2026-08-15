import { testEventID } from './category'
import { faker } from '@faker-js/faker'

export type FallbackCreditStateFixture = {
  limit: number
  consumed: number
  remaining: number
  exhausted: boolean
  warning_threshold?: number
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
