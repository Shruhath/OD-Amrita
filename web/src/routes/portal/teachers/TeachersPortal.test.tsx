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

// TeachersPortal.test.tsx — component coverage for the teachers portal.
// UNIT/component test (Vitest + jsdom + React Testing Library + MSW).
//
// Invoked by: web-deploy -> build job (`pnpm --filter od-amrita-web test`).
//
// Every case is reached by NAVIGATING the memory router to '/portal/teachers'
// rather than importing the component directly — the same call
// EventDetailPortal.test.tsx makes — so the route registration in router.tsx is
// exercised too: this file imports nothing from routes/portal/teachers/*.
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
    http.post('*/api/v1/admin/users/:userID/demote', ({ params }) => {
      const userID = String(params.userID)
      adminRequest(`demote:${userID}`)
      const found = serverTeachers.find((teacher) => teacher.user_id === userID)
      if (!found) return HttpResponse.json({ code: 'USER_NOT_FOUND', message: 'User not found.' }, { status: 404 })
      serverTeachers = serverTeachers.filter((teacher) => teacher.user_id !== userID)
      return HttpResponse.json({
        user_id: userID,
        email: found.email,
        previous_role: 'teacher',
        role: 'student',
        changed: true,
        removed_coordinator_assignments: 0,
        removed_category_assignments: found.categories.length,
        removed_teacher_branches: found.branches.length,
        sessions_revoked: true,
      })
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
    // EventDetailPortal once did not do this (a stale "already exists" error
    // stuck to a freshly-typed, valid value) — the exact defect this screen must
    // not repeat.
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

describe('Admin removes staff access from a teacher', () => {
  test('[P0] demotes only after the confirmation, and the teacher leaves the roster', async () => {
    const user = userEvent.setup()
    const leaving = createTeacherProfileFixture({ email: 'leaving-teacher@amrita.edu' })
    const { adminRequest } = renderTeachersPortal({
      teachers: [leaving, createTeacherProfileFixture({ email: 'staying-teacher@amrita.edu' })],
    })

    const row = await screen.findByRole('row', { name: /leaving-teacher@amrita.edu/ })
    await user.click(within(row).getByRole('button', { name: 'Remove staff access for leaving-teacher@amrita.edu' }))

    const dialog = screen.getByRole('alertdialog', { name: 'Remove staff access' })
    expect(dialog).toHaveTextContent('becomes a student')
    expect(dialog).toHaveTextContent('signs them out everywhere')
    expect(adminRequest).not.toHaveBeenCalledWith(`demote:${leaving.user_id}`)

    await user.click(within(dialog).getByRole('button', { name: 'Remove staff access' }))

    expect(adminRequest).toHaveBeenCalledWith(`demote:${leaving.user_id}`)
    await vi.waitFor(() => expect(screen.queryByRole('row', { name: /leaving-teacher@amrita.edu/ })).not.toBeInTheDocument())
    expect(screen.getByRole('row', { name: /staying-teacher@amrita.edu/ })).toBeInTheDocument()
  })

  test('[P0] cancel leaves the teacher untouched, and focus starts on Cancel', async () => {
    const user = userEvent.setup()
    const kept = createTeacherProfileFixture({ email: 'kept-teacher@amrita.edu' })
    const { adminRequest } = renderTeachersPortal({ teachers: [kept] })

    const row = await screen.findByRole('row', { name: /kept-teacher@amrita.edu/ })
    await user.click(within(row).getByRole('button', { name: 'Remove staff access for kept-teacher@amrita.edu' }))
    const dialog = screen.getByRole('alertdialog', { name: 'Remove staff access' })
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    await vi.waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(adminRequest).not.toHaveBeenCalledWith(`demote:${kept.user_id}`)
    expect(screen.getByRole('row', { name: /kept-teacher@amrita.edu/ })).toBeInTheDocument()
  })

  test('[P1] shows the server refusal inside the dialog', async () => {
    const user = userEvent.setup()
    const teacher = createTeacherProfileFixture({ email: 'refused-teacher@amrita.edu' })
    renderTeachersPortal({ teachers: [teacher] })
    server.use(
      http.post('*/api/v1/admin/users/:userID/demote', () =>
        HttpResponse.json({ code: 'LAST_ADMIN_DEMOTION_REFUSED', message: 'This is the last administrator; promote another admin first.' }, { status: 409 }),
      ),
    )

    const row = await screen.findByRole('row', { name: /refused-teacher@amrita.edu/ })
    await user.click(within(row).getByRole('button', { name: 'Remove staff access for refused-teacher@amrita.edu' }))
    const dialog = screen.getByRole('alertdialog', { name: 'Remove staff access' })
    await user.click(within(dialog).getByRole('button', { name: 'Remove staff access' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('This is the last administrator')
    expect(screen.getByRole('row', { name: /refused-teacher@amrita.edu/ })).toBeInTheDocument()
  })
})

function orderTeachers(teachers: TeacherProfileFixture[]) {
  return [...teachers].sort((left, right) => left.email.localeCompare(right.email) || left.user_id.localeCompare(right.user_id))
}
