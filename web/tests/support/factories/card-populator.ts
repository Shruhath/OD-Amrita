import { faker } from '@faker-js/faker'

export type CardPopulatorBranch = {
  id: string
  code: string
  name: string
}

export type CardPopulatorStudent = {
  student_id: string
  roll_no: string
  display_name: string
  branch: string
  section: string
  batch: string
}

export type CardPayload = {
  version: string
  token: string
}

export const cardPopulatorBranch: CardPopulatorBranch = {
  id: '91000000-0000-4000-8000-000000000001',
  code: 'CSE',
  name: 'Computer Science and Engineering',
}

export function cardPopulatorStudent(sequence: number, overrides: Partial<CardPopulatorStudent> = {}): CardPopulatorStudent {
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

export function cardPayload(overrides: Partial<CardPayload> = {}): CardPayload {
  return {
    version: 'v1',
    token: 'QlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlJSQlI',
    ...overrides,
  }
}

export function studentCollection(data: CardPopulatorStudent[]) {
  return {
    data,
    page: 1,
    page_size: 100,
    total: data.length,
  }
}

export function sealResponse(student: CardPopulatorStudent, payload: CardPayload = cardPayload()) {
  return {
    student,
    version: payload.version,
    token: payload.token,
    payload,
  }
}
