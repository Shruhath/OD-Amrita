import { faker } from '@faker-js/faker'

export type BranchFixture = {
  id: string
  code: string
  name: string
}

export type EventFixture = {
  id: string
  name: string
  event_date: string
  status: 'active' | 'archived'
  branches: BranchFixture[]
  created_at: string
  archived_at: string | null
}

export type CreateEventFixture = {
  name: string
  event_date: string
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

export function createEventFixture(
  overrides: Partial<EventFixture> = {},
): EventFixture {
  const status = overrides.status ?? 'active'
  return {
    id: faker.string.uuid(),
    name: `${faker.company.catchPhrase()} ${faker.string.alphanumeric(8)}`,
    event_date: '2026-08-15',
    status,
    branches: [...testBranches],
    created_at: '2026-07-20T03:00:00Z',
    archived_at: status === 'archived' ? '2026-07-21T03:00:00Z' : null,
    ...overrides,
  }
}
