import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { CARD_MEDIA_TYPE } from '../../../features/cardPopulator/webNfc'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import {
  cardPayload,
  cardPopulatorBranch,
  cardPopulatorStudent,
  sealResponse,
  studentCollection,
} from '../../../../tests/support/factories/card-populator'

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

type NfcHarness = {
  writes: { records: { recordType: string; mediaType?: string; data?: BufferSource | string }[] }[]
  /** Payload the next simulated tap will present. */
  nextRead: { version: string; token: string } | null
  writeError: Error | null
  settleRead: boolean
}

/**
 * Installs a fake `NDEFReader` on the real window.
 *
 * The component feature-detects against `window`, so stubbing the global — as
 * opposed to injecting an adapter — is what lets these tests exercise the same
 * detection path the phone will take.
 */
function installNfc(overrides: Partial<NfcHarness> = {}): NfcHarness {
  const harness: NfcHarness = { writes: [], nextRead: cardPayload(), writeError: null, settleRead: true, ...overrides }
  type ReadingListener = (event: { message: { records: unknown[] } }) => void

  class FakeNDEFReader {
    private readers: ReadingListener[] = []

    async write(message: NfcHarness['writes'][number]) {
      if (harness.writeError) throw harness.writeError
      harness.writes.push(message)
    }

    async scan() {
      // Deliver the tap on a microtask so the component observes its
      // "Verifying…" state before the read settles.
      queueMicrotask(() => {
        if (!harness.settleRead) return
        const records = harness.nextRead
          ? [
              {
                recordType: 'mime',
                mediaType: CARD_MEDIA_TYPE,
                data: new DataView(new TextEncoder().encode(JSON.stringify(harness.nextRead)).buffer),
              },
            ]
          : []
        for (const listener of [...this.readers]) listener({ message: { records } })
      })
    }

    addEventListener(type: string, listener: never) {
      if (type === 'reading') this.readers.push(listener as unknown as ReadingListener)
    }

    removeEventListener() {}
  }

  vi.stubGlobal('isSecureContext', true)
  Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true, writable: true })
  ;(window as unknown as { NDEFReader?: unknown }).NDEFReader = FakeNDEFReader

  return harness
}

function renderPopulator(role: 'admin' | 'student' = 'admin') {
  const students = [
    cardPopulatorStudent(2, { display_name: 'Madhav Rao', roll_no: 'CB.SC.U4CSE24002' }),
    cardPopulatorStudent(1, { display_name: 'Ananya Rao', roll_no: 'CB.SC.U4CSE24001' }),
  ]
  // Server order is authoritative — the API returns roll-ordered rows, and the
  // component must not re-sort. Ordered here as the server would return them.
  const ordered = [students[1], students[0]]
  const sealCalls: { studentID: unknown }[] = []
  const resolveCalls: unknown[] = []

  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        subject: `card-populator-${role}`,
        email: `${role}@amrita.edu`,
        name: `Card ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [cardPopulatorBranch] })),
    http.get('*/api/v1/admin/students', () => HttpResponse.json(studentCollection(ordered))),
    http.post('*/api/v1/admin/students/:student_id/card-seal', ({ params }) => {
      sealCalls.push({ studentID: params.student_id })
      return HttpResponse.json(sealResponse(students[1], cardPayload()), { status: 201 })
    }),
    http.post('*/api/v1/admin/card-tokens/resolve', async ({ request }) => {
      resolveCalls.push(await request.json())
      return HttpResponse.json(students[1])
    }),
  )

  render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/card-populator'])} />)
  return { students, ordered, sealCalls, resolveCalls }
}

async function loadRoster(user: ReturnType<typeof userEvent.setup>) {
  await screen.findByRole('heading', { name: 'Card populator' })
  // The branch list is fetched, so the option has to arrive before it can be
  // chosen — selecting eagerly would race the query rather than test it.
  await screen.findByRole('option', { name: new RegExp(cardPopulatorBranch.code) })
  await user.selectOptions(screen.getByLabelText('Branch'), cardPopulatorBranch.id)
  await user.type(screen.getByLabelText('Batch'), '2024')
  await user.type(screen.getByLabelText('Section'), 'A')
  await user.click(screen.getByRole('button', { name: 'Load roster' }))
  return screen.findByRole('table', { name: 'Students to write' })
}

afterEach(() => {
  window.sessionStorage.clear()
  vi.unstubAllGlobals()
  delete (window as unknown as { NDEFReader?: unknown }).NDEFReader
})

describe('Card populator portal', () => {
  test('[P0] shows section roster in server order with stable progress counts', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')

    const table = await loadRoster(user)

    expect(within(table).getAllByRole('row').map((row) => row.textContent)).toEqual([
      expect.stringContaining('Roll number'),
      expect.stringContaining('CB.SC.U4CSE24001'),
      expect.stringContaining('CB.SC.U4CSE24002'),
    ])
    expect(screen.getByText(/0 written/i)).toBeInTheDocument()
    expect(screen.getByText(/0 verified/i)).toBeInTheDocument()
  })

  test('[P0] loads every server page for a section before reporting progress', async () => {
    installNfc()
    const user = userEvent.setup()
    const students = Array.from({ length: 101 }, (_item, index) =>
      cardPopulatorStudent(index + 1, {
        display_name: `Student ${String(index + 1).padStart(3, '0')}`,
        roll_no: `CB.SC.U4CSE24${String(index + 1).padStart(3, '0')}`,
      }),
    )
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ subject: 'admin', email: 'admin@amrita.edu', name: 'Admin', role: 'admin' }),
      ),
      http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [cardPopulatorBranch] })),
      http.get('*/api/v1/admin/students', ({ request }) => {
        const page = Number(new URL(request.url).searchParams.get('page') ?? '1')
        const start = (page - 1) * 100
        return HttpResponse.json({
          ...studentCollection(students.slice(start, start + 100)),
          page,
          total: students.length,
        })
      }),
    )
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/card-populator'])} />)

    const table = await loadRoster(user)

    expect(await within(table).findByRole('row', { name: /Student 101/ })).toBeInTheDocument()
    expect(screen.getByText(/101 in section/i)).toBeInTheDocument()
  })

  test('[P0] requires a complete section before loading the roster', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')

    await screen.findByRole('heading', { name: 'Card populator' })
    await screen.findByRole('option', { name: new RegExp(cardPopulatorBranch.code) })
    await user.selectOptions(screen.getByLabelText('Branch'), cardPopulatorBranch.id)
    await user.click(screen.getByRole('button', { name: 'Load roster' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/branch, batch and section/i)
    expect(screen.queryByRole('table', { name: 'Students to write' })).not.toBeInTheDocument()
  })

  test('[P0] normalizes the section filter before requesting the roster', async () => {
    installNfc()
    const user = userEvent.setup()
    const requestedSections: string[] = []
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ subject: 'admin', email: 'admin@amrita.edu', name: 'Admin', role: 'admin' }),
      ),
      http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [cardPopulatorBranch] })),
      http.get('*/api/v1/admin/students', ({ request }) => {
        requestedSections.push(new URL(request.url).searchParams.get('section') ?? '')
        return HttpResponse.json(studentCollection([]))
      }),
    )
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/card-populator'])} />)

    await screen.findByRole('heading', { name: 'Card populator' })
    await screen.findByRole('option', { name: new RegExp(cardPopulatorBranch.code) })
    await user.selectOptions(screen.getByLabelText('Branch'), cardPopulatorBranch.id)
    await user.type(screen.getByLabelText('Batch'), '2024')
    await user.type(screen.getByLabelText('Section'), 'a')
    await user.click(screen.getByRole('button', { name: 'Load roster' }))

    await screen.findByText(/No students match/i)
    expect(requestedSections).toEqual(['A'])
  })

  test('[P0] writes exactly the sealed payload and verifies only the matching student', async () => {
    const nfc = installNfc()
    const user = userEvent.setup()
    const { sealCalls, resolveCalls, students } = renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Write card for Ananya Rao/i }))
    await screen.findByText(/Written, awaiting verify/i)
    await user.click(screen.getByRole('button', { name: /Verify card for Ananya Rao/i }))

    expect(await screen.findByText(/Verified Ananya Rao/i)).toBeInTheDocument()
    expect(sealCalls).toEqual([{ studentID: students[1].student_id }])
    expect(resolveCalls).toEqual([cardPayload()])

    // Exactly one record, carrying only the server payload.
    expect(nfc.writes).toHaveLength(1)
    const written = new TextDecoder().decode(nfc.writes[0].records[0].data as Uint8Array)
    expect(JSON.parse(written)).toEqual(cardPayload())
    expect(written).not.toMatch(/roll_no|display_name|student_id|branch|section|batch|email/i)
  })

  // A resolved write only proves the phone believed it wrote. Auto-marking a
  // card verified would let a card that silently failed the write count as done,
  // and that student arrives at the event with a blank card.
  test('[P0] does not mark a card verified on write alone', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Write card for Ananya Rao/i }))

    expect(await screen.findByText(/Written, awaiting verify/i)).toBeInTheDocument()
    expect(screen.queryByText(/^Verified/i)).not.toBeInTheDocument()
    expect(screen.getByText(/1 written · 0 verified/i)).toBeInTheDocument()
  })

  // Verification compares the SERVER-resolved student to the selected row. A
  // card holding someone else's token must fail loudly rather than pass because
  // a card was present.
  test('[P0] fails verification when the card resolves to a different student', async () => {
    installNfc()
    const user = userEvent.setup()
    const students = [
      cardPopulatorStudent(1, { display_name: 'Ananya Rao', roll_no: 'CB.SC.U4CSE24001' }),
      cardPopulatorStudent(2, { display_name: 'Madhav Rao', roll_no: 'CB.SC.U4CSE24002' }),
    ]
    server.use(
      http.get('*/api/v1/auth/me', () =>
        HttpResponse.json({ subject: 'admin', email: 'admin@amrita.edu', name: 'Admin', role: 'admin' }),
      ),
      http.get('*/api/v1/admin/branches', () => HttpResponse.json({ data: [cardPopulatorBranch] })),
      http.get('*/api/v1/admin/students', () => HttpResponse.json(studentCollection(students))),
      http.post('*/api/v1/admin/students/:student_id/card-seal', () =>
        HttpResponse.json(sealResponse(students[0], cardPayload()), { status: 201 }),
      ),
      // The card resolves to the OTHER student.
      http.post('*/api/v1/admin/card-tokens/resolve', () => HttpResponse.json(students[1])),
    )
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/card-populator'])} />)

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Write card for Ananya Rao/i }))
    await screen.findByText(/Written, awaiting verify/i)
    await user.click(screen.getByRole('button', { name: /Verify card for Ananya Rao/i }))

    expect(await screen.findByText(/This card belongs to Madhav Rao/i)).toBeInTheDocument()
    expect(screen.getByText(/0 verified/i)).toBeInTheDocument()
    // The retry path stays available for the same student.
    expect(screen.getByRole('button', { name: /Rewrite card for Ananya Rao/i })).toBeEnabled()
  })

  test('[P0] surfaces an unrecognized card as a failure with a retry path', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')
    server.use(
      http.post('*/api/v1/admin/card-tokens/resolve', () =>
        HttpResponse.json({ code: 'CARD_NOT_RECOGNIZED', message: 'Card was not recognized.' }, { status: 422 }),
      ),
    )

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Verify card for Ananya Rao/i }))

    expect(await screen.findByText(/Card was not recognized/i)).toBeInTheDocument()
    expect(screen.getByText(/1 failed/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeEnabled()
    expect(screen.getByRole('button', { name: /Write card for Ananya Rao/i })).toBeEnabled()
  })

  test('[P0] does not persist token-like API error text in progress notes', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')
    const leakedToken = cardPayload().token
    server.use(
      http.post('*/api/v1/admin/card-tokens/resolve', () =>
        HttpResponse.json({ code: 'CARD_NOT_RECOGNIZED', message: `token ${leakedToken} was not recognized` }, { status: 422 }),
      ),
    )

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Verify card for Ananya Rao/i }))

    expect(await screen.findByText(/operation failed/i)).toBeInTheDocument()
    expect(window.sessionStorage.getItem('od-amrita.card-populator.progress.v1') ?? '').not.toContain(leakedToken)
  })

  test('[P0] surfaces a blank card as a failure with a retry path', async () => {
    installNfc({ nextRead: null })
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Verify card for Ananya Rao/i }))

    expect(await screen.findByText(/no OD-Amrita card payload/i)).toBeInTheDocument()
    expect(screen.getByText(/1 failed/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeEnabled()
  })

  test('[P0] lets the operator cancel a scan that has not produced a card event', async () => {
    installNfc({ settleRead: false })
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Verify card for Ananya Rao/i }))
    await user.click(await screen.findByRole('button', { name: /Cancel card operation for Ananya Rao/i }))

    expect(await screen.findByText(/Card read cancelled/i)).toBeInTheDocument()
    expect(screen.getByText(/1 failed/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeEnabled()
  })

  test('[P0] surfaces a write failure without advancing the student', async () => {
    installNfc({ writeError: new Error('tag moved') })
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Write card for Ananya Rao/i }))

    expect(await screen.findByText(/could not be written/i)).toBeInTheDocument()
    expect(screen.getByText(/0 written · 0 verified · 1 failed/i)).toBeInTheDocument()
  })

  test('[P0] shows an explicit unsupported state and disables card actions', async () => {
    // No NDEFReader installed — a desktop browser, or Chrome without Web NFC.
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true, writable: true })
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)

    expect(screen.getByRole('alert')).toHaveTextContent(/cannot use NFC/i)
    expect(screen.getByRole('button', { name: /Write card for Ananya Rao/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Verify card for Ananya Rao/i })).toBeDisabled()
  })

  test('[P0] shows an explicit insecure-origin state rather than a generic failure', async () => {
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true, writable: true })
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)

    expect(screen.getByRole('alert')).toHaveTextContent(/HTTPS/i)
  })

  test('[P0] denies non-admins before any card-token request is issued', async () => {
    installNfc()
    const { sealCalls, resolveCalls } = renderPopulator('student')

    expect(await screen.findByRole('heading', { name: 'Access denied' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Write card/i })).not.toBeInTheDocument()
    expect(sealCalls).toHaveLength(0)
    expect(resolveCalls).toHaveLength(0)
  })

  // AC 6: progress must survive a reload, and must do so without ever putting a
  // token, payload or key material in storage.
  test('[P0] persists only token-free progress metadata', async () => {
    installNfc()
    const user = userEvent.setup()
    renderPopulator('admin')

    await loadRoster(user)
    await user.click(await screen.findByRole('button', { name: /Write card for Ananya Rao/i }))
    await screen.findByText(/Written, awaiting verify/i)

    const stored = window.sessionStorage.getItem('od-amrita.card-populator.progress.v1') ?? ''
    expect(stored).toContain('written')
    expect(stored).not.toContain(cardPayload().token)
    expect(stored).not.toMatch(/token|payload|hmac|secret|key/i)
  })
})
