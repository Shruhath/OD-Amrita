import { faker } from '@faker-js/faker'

// Category fixtures. Types are declared locally rather than imported from
// @od-amrita/contracts — the same shape event.ts uses — so a fixture cannot
// silently drift into asserting the generated type against itself. They describe
// the same wire object the contract does.

export type CategoryFixture = {
  id: string
  event_id: string
  name: string
  created_at: string
}

export const testEventID = '20000000-0000-4000-8000-000000000001'

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

// The server orders categories by name, then id, and the client must render
// that order rather than inventing its own. Tests use this to build the
// expectation instead of hand-sorting inline, which is how an assertion quietly
// starts agreeing with a buggy client.
// Compared with `<`/`>` rather than localeCompare, mirroring the component: the
// API's ORDER BY uses the DATABASE collation, and localeCompare applies locale
// rules that disagree with it (it sorts "apple" before "Zebra"; a C-collation
// cluster does the opposite). A fixture that sorted the localeCompare way would
// assert the client's convention against itself and could never catch the split.
export function serverOrdered(categories: CategoryFixture[]): CategoryFixture[] {
  return [...categories].sort((left, right) => {
    if (left.name !== right.name) return left.name < right.name ? -1 : 1
    if (left.id === right.id) return 0
    return left.id < right.id ? -1 : 1
  })
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
