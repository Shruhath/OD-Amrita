/**
 * Fixtures for the Story 0.10 PUBLIC attendance dashboard.
 *
 * Follows tests/support/factories/marking.ts: fixed ids so assertions can name
 * them, faker only for fields no assertion depends on, and an `overrides`
 * parameter on everything so a test states the ONE field it cares about.
 *
 * These shapes are DISTINCT from the admin AttendanceRecord family on purpose
 * (T3/T4). Nothing here may carry an email, an actor user_id, a card token, a
 * token fingerprint, a fallback-credit value or a dispute field — the response
 * these mirror is served to the open internet with no authentication, and a
 * fixture that invents a field is a fixture that lets a disclosure bug pass
 * every mocked test in the suite.
 *
 * The marker is a NAME and a ROLE. That is the entire actor surface.
 */

export const publicAttendanceRoute = '/attendance'

/** The dashboard's fixture student — the one every assertion below names. */
export const publicStudentID = '95000000-0000-4000-8000-000000000101'
export const publicStudentRoll = 'CB.SC.U4CSE24101'

export type PublicStudent = {
  student_id: string
  roll_no: string
  display_name: string
  branch: string
  section: string
  batch: string
}

export type PublicODRecord = {
  record_id: string
  slot: number
  event_id: string
  event_name: string
  event_date: string
  category_id: string
  category_name: string
  /** Nullable: actor_user_id is ON DELETE SET NULL, so an off-boarded teacher
   *  leaves the record readable with no name rather than deleting it. */
  marked_by_name: string | null
  marked_by_role: 'coordinator' | 'teacher' | 'admin'
  marked_at: string
}

export type PublicODDay = {
  event_date: string
  records: PublicODRecord[]
}

export type PublicODRecordCollection = {
  student: PublicStudent
  days: PublicODDay[]
  generated_at: string
}

export function publicStudent(overrides: Partial<PublicStudent> = {}): PublicStudent {
  return {
    student_id: publicStudentID,
    roll_no: publicStudentRoll,
    display_name: 'Ananya Rao',
    branch: 'CSE',
    section: 'A',
    batch: '2024',
    ...overrides,
  }
}

export function publicRecord(slot: number, overrides: Partial<PublicODRecord> = {}): PublicODRecord {
  return {
    record_id: `66000000-0000-4000-8000-${String(slot).padStart(12, '0')}`,
    slot,
    event_id: '93000000-0000-4000-8000-000000000001',
    event_name: 'Onam Celebrations',
    event_date: '2026-07-28',
    category_id: '94000000-0000-4000-8000-000000000001',
    category_name: 'Procession',
    marked_by_name: 'Portal Coordinator',
    marked_by_role: 'coordinator',
    marked_at: '2026-07-28T04:05:00Z',
    ...overrides,
  }
}

/**
 * The default day: slots 1, 2 and 4 present, slot 3 deliberately absent.
 *
 * That gap is PRD Journey 3 verbatim — a student opens the dashboard at 10pm,
 * sees Slots 1, 2 and 4 for today, and notices Slot 3 is missing. It is the
 * whole reason AC 11 renders the empty slots rather than only the present ones,
 * so every fixture that omits the gap tests a screen that cannot show it.
 */
export function publicDay(overrides: Partial<PublicODDay> = {}): PublicODDay {
  return {
    event_date: '2026-07-28',
    records: [publicRecord(1), publicRecord(2), publicRecord(4)],
    ...overrides,
  }
}

export function publicRecordCollection(overrides: Partial<PublicODRecordCollection> = {}): PublicODRecordCollection {
  return {
    student: publicStudent(),
    days: [publicDay()],
    generated_at: '2026-07-28T16:30:00Z',
    ...overrides,
  }
}

/** A successful read that found the student but no marks — NOT the same answer
 *  as a 404, and the UI must not render them alike (AC 12). */
export function publicEmptyRecordCollection(): PublicODRecordCollection {
  return publicRecordCollection({ days: [] })
}

export function publicSearchResults(students: PublicStudent[] = [publicStudent()]) {
  return { data: students }
}

/** The typed error envelope the public endpoints return for every red state. */
export function publicError(code: string, message: string) {
  return { code, message }
}
