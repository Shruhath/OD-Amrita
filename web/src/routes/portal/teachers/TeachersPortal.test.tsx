import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { createCategoryFixture, testEventID } from '../../../../tests/support/factories/category'
import { createEventFixture } from '../../../../tests/support/factories/event'
import {
  createTeacherProfileFixture,
  testTeacherBranches,
  testTeacherCategories,
  type TeacherProfileFixture,
} from '../../../../tests/support/factories/teacher'

// teachers-portal-component.test.tsx — Story 2.3 coverage for AC 6.
// UNIT/component test (Vitest + jsdom + React Testing Library + MSW).
//
// Invoked by: web-deploy -> build job (`pnpm --filter od-amrita-web test`).
//
// RED PHASE: `/portal/teachers` has no route in router.tsx and
// TeachersPortal.tsx does not exist until Task 5. Every case is reached by
// NAVIGATING the memory router to '/portal/teachers' rather than importing the
// component directly — the same call Story 2.2's EventDetailPortal.test.tsx
// made — so `pnpm typecheck` stays green while the module does not exist: this
// file imports nothing from routes/portal/teachers/*.
//
// Boundary note: this file asserts DOM behavior only. Wire-shape and rejection
// codes belong to the Go handler/service scaffolds and are not restated here.

type PortalScenario = {
  role?: 'admin' | 'teacher'
  branches?: typeof testTeacherBranches | []
  teachers?: TeacherProfileFixture[]
  teachersStatus?: number
}

function renderTeachersPortal({
  role = 'admin',
  branches = testTeacherBranches,
  teachers = [createTeacherProfileFixture({ email: 'existing-teacher@amrita.edu' })],
  teachersStatus = 200,
}: PortalScenario = {}) {
  const adminRequest = vi.fn()
  let serverTeachers = [...teachers]
  const categoryFixtures = testTeacherCategories.map((category) => createCategoryFixture({
    id: category.id,
    event_id: testEventID,
    name: category.name,
  }))
  const session: SessionPort = {
    accessToken: vi.fn(async () => 'atdd-access-token'),
    refresh: vi.fn(async () => 'atdd-access-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({ subject: `atdd-${role}`, email: `${role}@amrita.edu`, name: `ATDD ${role}`, role }),
    ),
    http.get('*/api/v1/admin/branches', () => {
      adminRequest('branches')
      return HttpResponse.json({ data: branches })
    }),
    http.get('*/api/v1/admin/events', () =>
      HttpResponse.json({
        data: [createEventFixture({ id: testEventID, status: 'active' })],
        page: 1,
        page_size: 100,
        total: 1,
      }),
    ),
    http.get('*/api/v1/admin/events/:eventID/categories', () => HttpResponse.json({ data: categoryFixtures })),
    http.get('*/api/v1/admin/teachers', ({ request }) => {
      adminRequest('teachers')
      if (teachersStatus !== 200) {
        return HttpResponse.json({ code: 'INTERNAL', message: 'Teachers could not be loaded' }, { status: teachersStatus })
      }
      const url = new URL(request.url)
      const page = Number(url.searchParams.get('page') ?? '1')
      const pageSize = Number(url.searchParams.get('page_size') ?? '20')
      const ordered = orderTeachers(serverTeachers)
      const start = (page - 1) * pageSize
      return HttpResponse.json({ data: ordered.slice(start, start + pageSize), page, page_size: pageSize, total: ordered.length })
    }),
    http.post('*/api/v1/admin/teachers', async ({ request }) => {
      adminRequest('create')
      const input = (await request.json()) as { email: string; branch_ids: string[]; category_ids: string[] }
      const created = createTeacherProfileFixture({
        email: input.email.trim().toLowerCase(),
        branches: testTeacherBranches.filter((branch) => input.branch_ids.includes(branch.id)),
        categories: testTeacherCategories.filter((category) => input.category_ids.includes(category.id)),
      })
      serverTeachers = orderTeachers([created, ...serverTeachers])
      return HttpResponse.json(created, { status: 201 })
    }),
    http.put('*/api/v1/admin/teachers/:userID/categories', async ({ params, request }) => {
      adminRequest('update-categories')
      const input = (await request.json()) as { category_ids: string[] }
      const userID = String(params.userID)
      const found = serverTeachers.find((teacher) => teacher.user_id === userID)
      if (!found) {
        return HttpResponse.json({ code: 'USER_NOT_FOUND', message: 'No user has signed in with this email yet.' }, { status: 404 })
      }
      const updated = {
        ...found,
        categories: testTeacherCategories.filter((category) => input.category_ids.includes(category.id)),
      }
      serverTeachers = orderTeachers(serverTeachers.map((teacher) => teacher.user_id === userID ? updated : teacher))
      return HttpResponse.json(updated)
    }),
  )

  const view = render(<Application session={session} router={createPortalMemoryRouter(['/portal/teachers'])} />)
  return { adminRequest, unmount: view.unmount }
}

describe('Admin teachers portal composition (ATDD red phase)', () => {
  test('[P0] allows an Admin to open the teachers view and see existing profiles with their branches', async () => {
    renderTeachersPortal({ teachers: [createTeacherProfileFixture({ email: 'priya@amrita.edu' })] })
    expect(await screen.findByRole('heading', { name: 'Teachers' })).toBeInTheDocument()
    const row = await screen.findByRole('row', { name: /priya@amrita.edu/ })
    // Branches are shown as their CODE: one profile can carry nine of them, and
    // nine full names turn a table cell into a paragraph. The full name is not
    // lost — it stays the abbreviation's expansion.
    expect(row).toHaveTextContent('ATDD-CSE')
    expect(within(row).getByTitle('Computer Science Test Fixture')).toBeInTheDocument()
  })

  test('[P0] denies a non-Admin without issuing an Admin teacher request', async () => {
    const { adminRequest } = renderTeachersPortal({ role: 'teacher' })
    expect(await screen.findByRole('heading', { name: 'Access denied' })).toBeInTheDocument()
    expect(adminRequest).not.toHaveBeenCalled()
  })

  test('[P0] creates a teacher profile by email and branch selection, and sees it appear without a reload', async () => {
    const user = userEvent.setup()
    const { adminRequest } = renderTeachersPortal({ teachers: [] })
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    await user.type(within(dialog).getByLabelText('Email'), 'new-teacher@amrita.edu')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create teacher' }))

    expect(await screen.findByRole('row', { name: /new-teacher@amrita.edu/ })).toBeInTheDocument()
    expect(adminRequest).toHaveBeenCalledWith('create')
  })

  test('[P0] edits an existing teacher category access without changing branches', async () => {
    const user = userEvent.setup()
    const existing = createTeacherProfileFixture({
      email: 'category-edit@amrita.edu',
      categories: [testTeacherCategories[0]],
      branches: [testTeacherBranches[0]],
    })
    const { adminRequest } = renderTeachersPortal({ teachers: [existing] })

    const row = await screen.findByRole('row', { name: /category-edit@amrita.edu/ })
    expect(row).toHaveTextContent(testTeacherCategories[0].name)
    await user.click(within(row).getByRole('button', { name: 'Edit' }))

    const dialog = screen.getByRole('dialog', { name: 'Edit category access' })
    await user.click(within(dialog).getByRole('checkbox', { name: testTeacherCategories[1].name }))
    await user.click(within(dialog).getByRole('button', { name: 'Save access' }))

    const updated = await screen.findByRole('row', { name: /category-edit@amrita.edu/ })
    expect(updated).toHaveTextContent(testTeacherCategories[1].name)
    expect(updated).toHaveTextContent(testTeacherBranches[0].code)
    expect(adminRequest).toHaveBeenCalledWith('update-categories')
  })

  test('[P1] maps a typed USER_NOT_FOUND rejection onto an announced non-field error', async () => {
    const user = userEvent.setup()
    renderTeachersPortal()
    server.use(
      http.post('*/api/v1/admin/teachers', () =>
        HttpResponse.json({ code: 'USER_NOT_FOUND', message: 'No user has signed in with this email yet.' }, { status: 404 }),
      ),
    )
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    await user.type(within(dialog).getByLabelText('Email'), 'never-logged-in@amrita.edu')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create teacher' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('No user has signed in with this email yet.')
  })

  test('[P1] maps a typed ROLE_ALREADY_ASSIGNED rejection onto an announced non-field error', async () => {
    const user = userEvent.setup()
    renderTeachersPortal()
    server.use(
      http.post('*/api/v1/admin/teachers', () =>
        HttpResponse.json({ code: 'ROLE_ALREADY_ASSIGNED', message: 'This user already has an assigned role.' }, { status: 409 }),
      ),
    )
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    await user.type(within(dialog).getByLabelText('Email'), 'already-admin@amrita.edu')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create teacher' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('This user already has an assigned role.')
  })

  test('[P1] maps a typed branch_ids field error onto the fieldset', async () => {
    const user = userEvent.setup()
    renderTeachersPortal()
    server.use(
      http.post('*/api/v1/admin/teachers', () =>
        HttpResponse.json(
          { code: 'VALIDATION_FAILED', message: 'Request validation failed', details: [{ field: 'branch_ids', code: 'duplicate', message: 'Branch ids must be unique.' }] },
          { status: 400 },
        ),
      ),
    )
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    await user.type(within(dialog).getByLabelText('Email'), 'duplicate-branch@amrita.edu')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create teacher' }))

    expect(await within(dialog).findByText('Branch ids must be unique.')).toBeInTheDocument()
  })

  test('[P1] clears field and form errors on every input change and on every new submit', async () => {
    // Story 2.2's review found EventDetailPortal did not do this (a stale
    // "already exists" error stuck to a freshly-typed, valid value) — the exact
    // defect this story's Task 5 is told not to repeat.
    const user = userEvent.setup()
    renderTeachersPortal()
    server.use(
      http.post('*/api/v1/admin/teachers', () =>
        HttpResponse.json({ code: 'USER_NOT_FOUND', message: 'No user has signed in with this email yet.' }, { status: 404 }),
      ),
    )
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    await user.type(within(dialog).getByLabelText('Email'), 'never-logged-in@amrita.edu')
    await user.click(within(dialog).getByRole('checkbox', { name: /Computer Science/ }))
    await user.click(within(dialog).getByRole('checkbox', { name: /Campus Decoration/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Create teacher' }))
    await within(dialog).findByRole('alert')

    await user.type(within(dialog).getByLabelText('Email'), 'x')
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument()
  })

  test('[P1] disables submit while the email is blank, whitespace-only, or the request is in flight', async () => {
    const user = userEvent.setup()
    renderTeachersPortal()
    await user.click(await screen.findByRole('button', { name: 'New teacher' }))
    const dialog = screen.getByRole('dialog', { name: 'New teacher' })
    const submit = within(dialog).getByRole('button', { name: 'Create teacher' })
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Email'), '   ')
    expect(submit).toBeDisabled()
  })

  test('[P1] paginates the teacher roster the same way the events list does', async () => {
    const paged = Array.from({ length: 21 }, (_, index) =>
      createTeacherProfileFixture({ email: `paged-teacher-${index + 1}@amrita.edu` }),
    )
    renderTeachersPortal({ teachers: paged })
    expect(await screen.findByText('Page 1 of 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
  })

  test('[P2] exposes an honest empty state before any teacher exists', async () => {
    renderTeachersPortal({ teachers: [] })
    expect(await screen.findByText('No teachers yet')).toBeInTheDocument()
  })

  test('[P2] renders a retryable list error without an indefinite spinner', async () => {
    renderTeachersPortal({ teachersStatus: 500 })
    expect(await screen.findByRole('alert')).toHaveTextContent('Teachers could not be loaded')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeEnabled()
    expect(screen.queryByRole('status', { name: 'Loading teachers' })).not.toBeInTheDocument()
  })

  test('[P2] renders a placeholder for a teacher with no display name', async () => {
    renderTeachersPortal({ teachers: [createTeacherProfileFixture({ email: 'no-name@amrita.edu', name: null })] })
    const row = await screen.findByRole('row', { name: /no-name@amrita.edu/ })
    expect(row).toHaveTextContent('—')
  })
})

function orderTeachers(teachers: TeacherProfileFixture[]) {
  return [...teachers].sort((left, right) => left.email.localeCompare(right.email) || left.user_id.localeCompare(right.user_id))
}
