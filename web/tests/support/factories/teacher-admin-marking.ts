import { faker } from '@faker-js/faker'
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

export type AttendanceRecord = {
  record_id: string
  student: ReturnType<typeof markStudent>
  event_id: string
  category: ReturnType<typeof markableCategory>
  slot: number
  capture_method: 'search' | 'nfc' | 'camera'
  marked_by: AttendanceActor
  actor_role: 'teacher' | 'admin' | 'coordinator'
  server_received_at: string
}

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
