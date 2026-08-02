import { faker } from '@faker-js/faker'

// Fixtures for the Story 0.6 coordinator marking surface.
//
// Follows tests/support/factories/card-populator.ts: fixed ids so assertions can
// name them, faker only for fields no assertion depends on, and an `overrides`
// parameter on everything so a test states the ONE field it cares about.
//
// Nothing here may carry a card token beyond the opaque `{version, token}` pair
// a tag actually holds. There is no fixture for a "resolved card list" because
// no such list may exist on a device (AR20).

export const markEventID = '11111111-1111-4111-8111-111111111111'
export const markCategoryID = '33333333-3333-4333-8333-333333333333'

// Shapes mirror the CONTRACT, which reuses Event and Category for the scoped
// reads rather than forking new schemas — so the identifier is `id`, not
// `event_id`/`category_id`. A fixture that invented its own field name would let
// a client bug pass every mocked test and fail against the real API.
export type MarkableEvent = {
  id: string
  name: string
  event_date: string
  status: string
  branches: { id: string; code: string; name: string }[]
  created_at: string
  archived_at: string | null
}

export type MarkableCategory = {
  id: string
  event_id: string
  name: string
  created_at: string
}

export type MarkStudent = {
  student_id: string
  roll_no: string
  display_name: string
  branch: string
  section: string
  batch: string
}

export type MarkSlotWindow = {
  slot: number
  started_at: string
  ends_at: string
}

export type MarkStatus = {
  server_time: string
  slot: MarkSlotWindow | null
  counters: { mine: number; category_total: number }
}

/** Today in ISO date form. ActiveSlot compares against events.event_date, so a
 *  fixture event dated anything else can never have an open slot. */
export function todayISODate(): string {
  return new Date().toISOString().slice(0, 10)
}

export function markableEvent(overrides: Partial<MarkableEvent> = {}): MarkableEvent {
  return {
    id: markEventID,
    name: 'Gokulashtami',
    event_date: todayISODate(),
    status: 'active',
    branches: [{ id: '91000000-0000-4000-8000-000000000001', code: 'CSE', name: 'Computer Science and Engineering' }],
    created_at: new Date().toISOString(),
    archived_at: null,
    ...overrides,
  }
}

export function markableCategory(overrides: Partial<MarkableCategory> = {}): MarkableCategory {
  return {
    id: markCategoryID,
    event_id: markEventID,
    name: 'Cultural',
    created_at: new Date().toISOString(),
    ...overrides,
  }
}

export function markStudent(sequence = 1, overrides: Partial<MarkStudent> = {}): MarkStudent {
  return {
    student_id: `95000000-0000-4000-8000-${String(sequence).padStart(12, '0')}`,
    roll_no: `CB.SC.U4CSE24${String(sequence).padStart(3, '0')}`,
    display_name: faker.person.fullName(),
    branch: 'CSE',
    section: 'A',
    batch: '2024',
    ...overrides,
  }
}

/** The raw payload a 0.5-written tag carries. Opaque on purpose: the device
 *  never learns whether it is valid — that is the server's answer (NFR16). */
export function markCardPayload(overrides: Partial<{ version: string; token: string }> = {}) {
  return {
    version: 'v1',
    token: 'QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI',
    ...overrides,
  }
}

/** A status with a live slot. `minutesLeft` controls the countdown under test. */
export function markStatusOpen(overrides: Partial<MarkStatus> = {}, minutesLeft = 12): MarkStatus {
  const now = new Date()
  const ends = new Date(now.getTime() + minutesLeft * 60_000)
  const started = new Date(ends.getTime() - 50 * 60_000)
  return {
    server_time: now.toISOString(),
    slot: { slot: 2, started_at: started.toISOString(), ends_at: ends.toISOString() },
    counters: { mine: 4, category_total: 17 },
    ...overrides,
  }
}

/** The explicit no-slot-open state. `slot` is null — never slot 0. */
export function markStatusClosed(overrides: Partial<MarkStatus> = {}): MarkStatus {
  return {
    server_time: new Date().toISOString(),
    slot: null,
    counters: { mine: 0, category_total: 0 },
    ...overrides,
  }
}

export function markResult(student: MarkStudent = markStudent(), slot = 2) {
  return {
    record_id: '66666666-6666-4666-8666-666666666666',
    student,
    event_id: markEventID,
    category_id: markCategoryID,
    slot,
    capture_method: 'nfc',
    server_received_at: new Date().toISOString(),
  }
}

export function collection<T>(data: T[]) {
  return { data, page: 1, page_size: 50, total: data.length }
}

/** CategoryCollection and StudentSearchResults are unpaginated — `data` only. */
export function dataOnly<T>(data: T[]) {
  return { data }
}

/** The error envelope the API returns for every red state in AC 5. */
export function markError(code: string, message: string) {
  return { code, message }
}
