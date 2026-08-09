import { faker } from '@faker-js/faker'
import type { AttendanceRecord as ContractAttendanceRecord } from '../../../src/api/client'
import {
  collection,
  dataOnly,
  markCategoryID,
  markEventID,
  markStudent,
  markableCategory,
  markableEvent,
} from './marking'

export const teacherMarkRoute = '/portal/mark'

export type AttendanceActor = {
  user_id: string | null
  email: string | null
  name: string | null
}

/**
 * Typed from the CONTRACT rather than by hand.
 *
 * The hand-written version had already drifted by the time `od_date` was added
 * under Story 0.12a: it declared the shape it happened to need, so a field the
 * API required could go missing here and every mocked test still passed while
 * the real screen crashed on it. Story 0.11 retyped `PortalDispute` for the same
 * reason after the same failure.
 */
export type AttendanceRecord = ContractAttendanceRecord

export function teacherMarkEvent() {
  return markableEvent({ name: 'Teacher Mark Fixture' })
}

export function teacherMarkCategory() {
  return markableCategory({ name: 'Exception Desk' })
}

export function teacherMarkStudent(sequence = 1) {
  return markStudent(sequence, { display_name: faker.person.fullName() })
}

export function teacherSearchMarkResult(slot = 4, role: 'teacher' | 'admin' = 'teacher') {
  const student = teacherMarkStudent()
  return {
    record_id: '66000000-0000-4000-8000-000000000009',
    student,
    event_id: markEventID,
    category_id: markCategoryID,
    slot,
    capture_method: 'search',
    server_received_at: new Date().toISOString(),
    actor_role: role,
  }
}

export function attendanceRecord(overrides: Partial<AttendanceRecord> = {}): AttendanceRecord {
  return {
    record_id: '66000000-0000-4000-8000-000000000009',
    student: teacherMarkStudent(),
    event_id: markEventID,
    category: teacherMarkCategory(),
    slot: 4,
    // The day the OD belongs to. Distinct from server_received_at below, which
    // is when the row was written — on a correction they are different days.
    od_date: new Date().toISOString().slice(0, 10),
    status: 'present',
    reason_note: null,
    capture_method: 'search',
    marked_by: {
      user_id: '77777777-7777-4777-8777-777777777777',
      email: 'teacher@amrita.edu',
      name: 'E2E teacher',
    },
    actor_role: 'teacher',
    server_received_at: new Date().toISOString(),
    ...overrides,
  }
}

export function attendanceCollection(rows: AttendanceRecord[] = [attendanceRecord()]) {
  return collection(rows)
}

export function teacherMarkReads() {
  return {
    events: collection([teacherMarkEvent()]),
    categories: dataOnly([teacherMarkCategory()]),
    students: dataOnly([teacherMarkStudent()]),
  }
}
