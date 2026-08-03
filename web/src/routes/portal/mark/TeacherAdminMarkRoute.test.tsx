import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import { createPortalMemoryRouter } from '../../../router'
import type { SessionPort } from '../../../features/auth/session'
import { server } from '../../../test/server'
import {
  attendanceCollection,
  teacherMarkReads,
  teacherMarkRoute,
  teacherSearchMarkResult,
} from '../../../../tests/support/factories/teacher-admin-marking'

const redTest = process.env.ATDD_ACTIVE_09 === '1' ? test : test.skip

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function installTeacherBackend(role: 'teacher' | 'admin' = 'teacher') {
  const reads = teacherMarkReads()
  const posts: unknown[] = []
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '77777777-7777-4777-8777-777777777777',
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/portal/mark/events', () => HttpResponse.json(reads.events)),
    http.get('*/api/v1/portal/mark/events/:eventID/categories', () => HttpResponse.json(reads.categories)),
    http.get('*/api/v1/students/search', () => HttpResponse.json(reads.students)),
    http.post('*/api/v1/mark', async ({ request }) => {
      posts.push(await request.json())
      return HttpResponse.json(teacherSearchMarkResult(4, role), { status: 201 })
    }),
    http.get('*/api/v1/admin/events/:eventID/attendance', () => HttpResponse.json(attendanceCollection())),
  )
  return posts
}

function renderRoute(path = teacherMarkRoute) {
  return render(
    <Application session={sessionPort()} router={createPortalMemoryRouter([path])} />,
  )
}

describe('teacher/admin mark route red-phase scaffold', () => {
  redTest('[P0] teacher search-mark posts student_id, capture_method search and explicit slot', async () => {
    const user = userEvent.setup()
    const posts = installTeacherBackend('teacher')

    renderRoute()

    await screen.findByRole('heading', { name: /mark attendance/i })
    await user.selectOptions(screen.getByRole('combobox', { name: /event/i }), /teacher mark fixture/i)
    await user.selectOptions(screen.getByRole('combobox', { name: /category/i }), /exception desk/i)
    await user.selectOptions(screen.getByRole('combobox', { name: /slot/i }), '4')
    await user.type(screen.getByRole('searchbox', { name: /student/i }), 'CB.SC.U4CSE24')
    await user.click(await screen.findByRole('option', { name: /CB\.SC\.U4CSE/i }))
    await user.click(screen.getByRole('button', { name: /mark present/i }))

    await waitFor(() => expect(posts).toHaveLength(1))
    expect(posts[0]).toMatchObject({
      event_id: expect.any(String),
      category_id: expect.any(String),
      student_id: expect.any(String),
      capture_method: 'search',
      slot: 4,
    })
    expect(screen.getByRole('status')).toHaveTextContent(/marked|present/i)
  })

  redTest('[P0] teacher/admin route has no NFC, camera, or fallback-credit controls', async () => {
    installTeacherBackend('admin')
    renderRoute()

    await screen.findByRole('heading', { name: /mark attendance/i })
    expect(screen.queryByRole('button', { name: /tap|nfc/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /camera|scan/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/fallback credit/i)).not.toBeInTheDocument()
  })

  redTest('[P1] admin attendance view renders filters and server rows', async () => {
    installTeacherBackend('admin')
    renderRoute('/portal/events/11111111-1111-4111-8111-111111111111')

    await screen.findByRole('heading', { name: /attendance/i })
    expect(screen.getByRole('combobox', { name: /category/i })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /slot/i })).toBeInTheDocument()
    expect(screen.getByText(/CB\.SC\.U4CSE/i)).toBeInTheDocument()
    expect(screen.getByText(/teacher@amrita\.edu/i)).toBeInTheDocument()
    expect(screen.getByText(/search/i)).toBeInTheDocument()
  })
})
