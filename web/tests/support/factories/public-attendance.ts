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
  /** The EVENT's first day. Stays on the record because eventsFromRecords()
   *  builds the dispute form's event options from it — a record reporting its
   *  own day here would offer an event dated to one arbitrary record. */
  event_date: string
  /** The day this OD BELONGS to, which is not the day the row was written.
   *  A teacher's next-morning correction has both, and they differ. */
  od_date: string
  category_id: string
  category_name: string
  /** Nullable: actor_user_id is ON DELETE SET NULL, so an off-boarded teacher
   *  leaves the record readable with no name rather than deleting it. */
  marked_by_name: string | null
  marked_by_role: 'coordinator' | 'teacher' | 'admin'
  marked_at: string
}

/**
 * The grouping key is RENAMED from `event_date` to `od_date` under Story 0.12a,
 * not supplemented. The key must not lie about which fact it holds: keyed on the
 * event's start date, a thirty-day festival collapses onto one heading and reads
 * to the student as twenty-nine days of missing attendance.
 *
 * `event_date` is GONE from this type, deleted in the same change that switched
 * PublicAttendanceScreen.tsx to `day.od_date`, exactly as the deprecation note
 * here instructed. Keeping it would have left every fixture emitting both keys
 * with the same value, so the 0.10 and 0.11 suites would pass whether the
 * screen read `od_date` or `event_date` — a test that cannot fail, and the
 * defect shape this codebase has been bitten by repeatedly.
 *
 * The server cannot produce the field either: `PublicODDay` in the contract is
 * exactly `{od_date, records}`, enforced by the exact-match allowlist in
 * publicSchemaAllowlists.
 */
export type PublicODDay = {
  od_date: string
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
    od_date: overrides.od_date ?? overrides.event_date ?? '2026-07-28',
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
  const odDate = overrides.od_date ?? '2026-07-28'
  return {
    od_date: odDate,
    records: [publicRecord(1, { od_date: odDate }), publicRecord(2, { od_date: odDate }), publicRecord(4, { od_date: odDate })],
    ...overrides,
  }
}

/**
 * A student's history across three days of ONE thirty-day festival.
 *
 * ⚠️ The day groups carry `od_date` and NOTHING ELSE — no `event_date` at all.
 * That is deliberate and it is what makes the assertions in
 * PublicAttendanceDays.test.tsx able to FAIL. Emitting both keys made those tests
 * pass whether the screen read `od_date` or `event_date`, because both held the
 * same value — a test that cannot fail, which is the defect shape this codebase
 * has been bitten by three times (0.10's review found three of them at once).
 *
 * This is also exactly the shape the server produces after T18: `PublicODDay`'s
 * `event_date` is RENAMED to `od_date`, not supplemented.
 *
 * Every RECORD keeps `event_date` at the festival's FIRST day (2026-08-10) while
 * carrying its own `od_date`, because on the record the two are genuinely
 * different facts and both are required.
 *
 * `publicMultiDayEventID` is distinct from `publicRecord`'s default event so the
 * two fixtures cannot be confused in an assertion.
 */
export const publicMultiDayEventID = '93000000-0000-4000-8000-000000000002'

export function publicMultiDayCollection(): PublicODRecordCollection {
  const festivalRecord = (slot: number, odDate: string): PublicODRecord =>
    publicRecord(slot, {
      record_id: `67000000-0000-4000-8000-${odDate.replace(/-/g, '')}${String(slot).padStart(2, '0')}`,
      event_id: publicMultiDayEventID,
      event_name: 'Amritotsavam',
      event_date: '2026-08-10',
      od_date: odDate,
      marked_at: `${odDate}T04:05:00Z`,
    })

  return publicRecordCollection({
    days: [
      { od_date: '2026-08-19', records: [festivalRecord(1, '2026-08-19'), festivalRecord(2, '2026-08-19')] },
      { od_date: '2026-08-12', records: [festivalRecord(1, '2026-08-12')] },
      { od_date: '2026-08-10', records: [festivalRecord(4, '2026-08-10')] },
    ],
    generated_at: '2026-08-19T16:30:00Z',
  })
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
