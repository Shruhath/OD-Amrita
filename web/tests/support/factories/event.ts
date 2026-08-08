import { faker } from '@faker-js/faker'

export type BranchFixture = {
  id: string
  code: string
  name: string
}

export type EventFixture = {
  id: string
  name: string
  /** The FIRST day of the range. Deliberately NOT renamed to start_date (T2). */
  event_date: string
  /** The LAST day of the range. Always present on a server-produced Event —
   *  every pre-existing row is backfilled to end_date = event_date. */
  end_date: string
  status: 'active' | 'archived'
  branches: BranchFixture[]
  created_at: string
  archived_at: string | null
}

export type CreateEventFixture = {
  name: string
  event_date: string
  /** OPTIONAL on the request, which is what makes a single-day create behave
   *  exactly as before: omitting it defaults to event_date server-side. */
  end_date?: string
  branch_ids: string[]
}

export const testBranches: readonly [BranchFixture, BranchFixture] = [
  {
    id: '10000000-0000-4000-8000-000000000001',
    code: 'ATDD-CSE',
    name: 'Computer Science Test Fixture',
  },
  {
    id: '10000000-0000-4000-8000-000000000002',
    code: 'ATDD-ECE',
    name: 'Electronics Test Fixture',
  },
]

export function createEventRequest(
  overrides: Partial<CreateEventFixture> = {},
): CreateEventFixture {
  return {
    name: `${faker.company.catchPhrase()} ${faker.string.alphanumeric(8)}`,
    event_date: '2026-08-15',
    branch_ids: testBranches.map((branch) => branch.id),
    ...overrides,
  }
}

/**
 * The default fixture stays a SINGLE-day event (end_date === event_date), so
 * every assertion written before this story keeps testing what it was written to
 * test. A test that is about the range says so by passing both dates.
 *
 * `end_date` defaults to whatever `event_date` resolves to — including an
 * override — rather than to a hardcoded '2026-08-15'. Hardcoding it would make
 * `createEventFixture({ event_date: '2026-09-01' })` silently produce a backwards
 * range that the real database would refuse with events_date_range_ck, and the
 * fixture would be testing a state the system cannot reach.
 */
export function createEventFixture(
  overrides: Partial<EventFixture> = {},
): EventFixture {
  const status = overrides.status ?? 'active'
  const eventDate = overrides.event_date ?? '2026-08-15'
  return {
    id: faker.string.uuid(),
    name: `${faker.company.catchPhrase()} ${faker.string.alphanumeric(8)}`,
    event_date: eventDate,
    end_date: eventDate,
    status,
    branches: [...testBranches],
    created_at: '2026-07-20T03:00:00Z',
    archived_at: status === 'archived' ? '2026-07-21T03:00:00Z' : null,
    ...overrides,
  }
}

/** A real multi-day range — the 30-day festival the story is written for. */
export function createMultiDayEventFixture(
  overrides: Partial<EventFixture> = {},
): EventFixture {
  return createEventFixture({
    name: 'Amritotsavam',
    event_date: '2026-08-10',
    end_date: '2026-09-08',
    ...overrides,
  })
}
