import { faker } from '@faker-js/faker'

// Fixture types are declared LOCALLY rather than imported from
// @od-amrita/contracts, because the generated TeacherProfile/
// CreateTeacherProfileRequest schemas do not exist until Task 2 regenerates
// the contract — exactly what event.ts/category.ts already do for the same
// reason. They describe the same wire object and stay valid after
// regeneration.

export type TeacherBranchFixture = {
  id: string
  code: string
  name: string
}

export type TeacherProfileFixture = {
  user_id: string
  email: string
  name: string | null
  branches: TeacherBranchFixture[]
  categories: TeacherCategoryFixture[]
  created_at: string
}

export type CreateTeacherProfileFixture = {
  email: string
  branch_ids: string[]
  category_ids: string[]
}

export type TeacherCategoryFixture = {
  id: string
  name: string
}

export const testTeacherBranches: readonly [TeacherBranchFixture, TeacherBranchFixture] = [
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

export const testTeacherCategories: readonly [TeacherCategoryFixture, TeacherCategoryFixture] = [
  {
    id: '20000000-0000-4000-8000-000000000011',
    name: 'Campus Decoration',
  },
  {
    id: '20000000-0000-4000-8000-000000000012',
    name: 'Procession',
  },
]

export function createTeacherProfileRequest(
  overrides: Partial<CreateTeacherProfileFixture> = {},
): CreateTeacherProfileFixture {
  return {
    email: faker.internet.email({ provider: 'amrita.edu' }).toLowerCase(),
    branch_ids: testTeacherBranches.map((branch) => branch.id),
    category_ids: testTeacherCategories.map((category) => category.id),
    ...overrides,
  }
}

export function createTeacherProfileFixture(
  overrides: Partial<TeacherProfileFixture> = {},
): TeacherProfileFixture {
  return {
    user_id: faker.string.uuid(),
    email: faker.internet.email({ provider: 'amrita.edu' }).toLowerCase(),
    name: faker.person.fullName(),
    branches: [...testTeacherBranches],
    categories: [...testTeacherCategories],
    created_at: '2026-07-23T03:00:00Z',
    ...overrides,
  }
}

/** The typed USER_NOT_FOUND rejection envelope, defined once so the
 * component, e2e and integration specs cannot drift into asserting
 * different shapes. */
export function userNotFoundError() {
  return { code: 'USER_NOT_FOUND', message: 'No user has signed in with this email yet.' }
}

/** The typed ROLE_ALREADY_ASSIGNED rejection envelope. */
export function roleAlreadyAssignedError() {
  return { code: 'ROLE_ALREADY_ASSIGNED', message: 'This user already has an assigned role.' }
}
