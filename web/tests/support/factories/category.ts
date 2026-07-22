import { faker } from '@faker-js/faker'

// Category fixtures for Story 2.2. Types are declared locally rather than
// imported from @od-amrita/contracts because the generated Category schema does
// not exist until the contract task lands — the same reason event.ts declares
// its own shapes. Once the contract is regenerated these stay valid; they
// describe the same wire object.

export type CategoryFixture = {
  id: string
  event_id: string
  name: string
  created_at: string
}

export type CreateCategoryFixture = {
  name: string
}

export const testEventID = '20000000-0000-4000-8000-000000000001'
export const testArchivedEventID = '20000000-0000-4000-8000-000000000002'

export function createCategoryFixture(
  overrides: Partial<CategoryFixture> = {},
): CategoryFixture {
  return {
    id: faker.string.uuid(),
    event_id: testEventID,
    name: `${faker.commerce.department()} ${faker.string.alphanumeric(6)}`,
    created_at: '2026-07-21T03:00:00Z',
    ...overrides,
  }
}

export function createCategoryRequest(
  overrides: Partial<CreateCategoryFixture> = {},
): CreateCategoryFixture {
  return {
    name: `${faker.commerce.department()} ${faker.string.alphanumeric(6)}`,
    ...overrides,
  }
}

// The server orders categories by name, then id, and the client must render
// that order rather than inventing its own. Tests use this to build the
// expectation instead of hand-sorting inline, which is how an assertion quietly
// starts agreeing with a buggy client.
export function serverOrdered(categories: CategoryFixture[]): CategoryFixture[] {
  return [...categories].sort(
    (left, right) => left.name.localeCompare(right.name) || left.id.localeCompare(right.id),
  )
}

// The typed duplicate rejection the API returns for a repeat name. It is a
// VALIDATION_FAILED with a field detail, never a bespoke error code.
export function duplicateNameError() {
  return {
    code: 'VALIDATION_FAILED',
    message: 'Category validation failed.',
    details: [
      { field: 'name', code: 'duplicate', message: 'A category with this name already exists.' },
    ],
  }
}

export function archivedEventError() {
  return {
    code: 'EVENT_ARCHIVED',
    message: 'This event is archived and can no longer be changed.',
  }
}
