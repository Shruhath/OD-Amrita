import { faker } from '@faker-js/faker'

import { testEventID } from './category'

export type CoordinatorAssignmentFixture = {
  id: string
  user_id: string
  email: string
  name: string | null
  event_id: string
  created_at: string
}

export type CreateCoordinatorAssignmentRequest = {
  email: string
}

export function createCoordinatorAssignmentFixture(
  overrides: Partial<CoordinatorAssignmentFixture> = {},
): CoordinatorAssignmentFixture {
  const first = faker.person.firstName()
  const last = faker.person.lastName()
  return {
    id: faker.string.uuid(),
    user_id: faker.string.uuid(),
    email: `${first}.${last}.${faker.string.alphanumeric(6)}@amrita.edu`.toLowerCase(),
    name: `${first} ${last}`,
    event_id: testEventID,
    created_at: '2026-08-03T03:00:00Z',
    ...overrides,
  }
}

export function createCoordinatorAssignmentRequest(
  overrides: Partial<CreateCoordinatorAssignmentRequest> = {},
): CreateCoordinatorAssignmentRequest {
  return {
    email: `candidate.${faker.string.alphanumeric(8)}@amrita.edu`.toLowerCase(),
    ...overrides,
  }
}

export function serverOrderedCoordinatorAssignments(
  assignments: CoordinatorAssignmentFixture[],
): CoordinatorAssignmentFixture[] {
  return [...assignments].sort((left, right) => {
    if (left.created_at !== right.created_at) return left.created_at < right.created_at ? -1 : 1
    if (left.email !== right.email) return left.email < right.email ? -1 : 1
    if (left.id === right.id) return 0
    return left.id < right.id ? -1 : 1
  })
}

export function duplicateAssignmentError() {
  return {
    code: 'COORDINATOR_ASSIGNMENT_EXISTS',
    message: 'This coordinator is already assigned to this event.',
  }
}

export function userNotFoundError() {
  return {
    code: 'USER_NOT_FOUND',
    message: 'No provisioned Amrita user exists for that email.',
  }
}

export function roleAlreadyAssignedError() {
  return {
    code: 'ROLE_ALREADY_ASSIGNED',
    message: 'Admins and teachers cannot be assigned as event coordinators.',
  }
}

export function coordinatorArchivedEventError() {
  return {
    code: 'EVENT_ARCHIVED',
    message: 'This event is archived and can no longer be changed.',
  }
}
