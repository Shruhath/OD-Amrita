import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { Application } from '../../../Application'
import type { SessionPort } from '../../../features/auth/session'
import { createPortalMemoryRouter } from '../../../router'
import { server } from '../../../test/server'
import { attendanceRecord } from '../../../../tests/support/factories/teacher-admin-marking'
import type { AttendanceRecord, CategoryRecord } from '../../../api/client'
import type { ExportFile } from '../../../features/export/attendanceExport'

/**
 * The export, driven the way an administrator drives it.
 *
 * Rendered through the REAL screen rather than the dialog on its own, because
 * the two things most likely to break sit outside the dialog: whether the
 * control is reachable for the role looking at it, and whether the event the
 * screen has selected is the event the export reads. Mounting the dialog with
 * props supplied by hand proves neither.
 *
 * Only the SAVING step is replaced. No test runner will let a page write files,
 * so what is asserted is the set of files the flow handed to the browser — their
 * names, their number, and the rows inside each, read back out of the workbook
 * bytes rather than out of the state that produced them.
 */

const saved: ExportFile[] = []

vi.mock('../../../features/export/attendanceExport', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../features/export/attendanceExport')>()
  return { ...actual, downloadFile: (file: ExportFile) => { saved.push(file) } }
})

const eventID = '11111111-1111-4111-8111-111111111111'
const day = '2026-07-28'
const culturalID = '33333333-3333-4333-8333-333333333331'
const sportsID = '33333333-3333-4333-8333-333333333332'
const technicalID = '33333333-3333-4333-8333-333333333333'

function sessionPort(): SessionPort {
  return {
    accessToken: vi.fn(async () => 'fresh-token'),
    refresh: vi.fn(async () => 'fresh-token'),
    signIn: vi.fn(async () => undefined),
    signOut: vi.fn(async () => undefined),
    subscribe: vi.fn(() => () => undefined),
  }
}

function row(
  slot: number,
  rollNo: string,
  name: string,
  category: string,
  status: 'present' | 'removed' = 'present',
  categoryID = '33333333-3333-4333-8333-333333333333',
): AttendanceRecord {
  const base = attendanceRecord({ slot, status, od_date: day })
  return {
    ...base,
    record_id: `${slot}-${rollNo}`,
    event_id: eventID,
    student: { ...base.student, roll_no: rollNo, display_name: name },
    category: { ...base.category, id: categoryID, name: category },
  }
}

const dayRecords: AttendanceRecord[] = [
  row(1, 'CB.SC.U4ECE24007', 'Vikram Nair', 'Sports Meet', 'present', sportsID),
  row(1, 'CB.SC.U4CSE24101', 'Ananya Rao', 'Cultural Programme', 'present', culturalID),
  row(2, 'CB.SC.U4CSE24102', 'Meera Suresh', 'Procession', 'present', culturalID),
  row(3, 'CB.SC.U4CSE24103', 'Rahul Menon', 'Technical Expo', 'present', technicalID),
  // Taken back. It must reach no file.
  row(3, 'CB.SC.U4CSE24999', 'Withdrawn Student', 'Technical Expo', 'removed', technicalID),
  // Present so the picker has a slot to leave out.
  row(7, 'CB.SC.U4CSE24104', 'Nikhil Das', 'Cultural Programme', 'present', culturalID),
]

const categories: CategoryRecord[] = [
  {
    id: culturalID,
    event_id: eventID,
    name: 'Cultural Programme',
    visible_on_public_dashboard: true,
    created_at: new Date().toISOString(),
  },
  {
    id: sportsID,
    event_id: eventID,
    name: 'Sports Meet',
    visible_on_public_dashboard: true,
    created_at: new Date().toISOString(),
  },
  {
    id: technicalID,
    event_id: eventID,
    name: 'Technical Expo',
    visible_on_public_dashboard: true,
    created_at: new Date().toISOString(),
  },
]

function install(role: 'admin' | 'teacher', records: AttendanceRecord[] = dayRecords, categoryRows: CategoryRecord[] = []) {
  const recordQueries: URL[] = []
  const eventQueries: URL[] = []
  server.use(
    http.get('*/api/v1/auth/me', () =>
      HttpResponse.json({
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `Portal ${role}`,
        role,
      }),
    ),
    http.get('*/api/v1/portal/mark/events', ({ request }) => {
      eventQueries.push(new URL(request.url))
      return HttpResponse.json({
        data: [{
          id: eventID,
          name: 'Amritotsavam 2026',
          event_date: day,
          end_date: day,
          status: 'active',
          branches: [],
          created_at: new Date().toISOString(),
        }],
      })
    }),
    http.get('*/api/v1/portal/mark/events/:eventID/categories', () => HttpResponse.json({ data: categoryRows })),
    http.get('*/api/v1/portal/events/:eventID/records', ({ request }) => {
      const url = new URL(request.url)
      recordQueries.push(url)
      const odDate = url.searchParams.get('od_date')
      const categoryID = url.searchParams.get('category_id')
      const page = Number(url.searchParams.get('page') ?? '1')
      const pageSize = Number(url.searchParams.get('page_size') ?? '50')
      const matching = records.filter((record) => (
        (odDate === null || record.od_date === odDate) &&
        (categoryID === null || record.category.id === categoryID)
      ))
      return HttpResponse.json({
        data: matching.slice((page - 1) * pageSize, page * pageSize),
        page,
        page_size: pageSize,
        total: matching.length,
      })
    }),
  )
  return { eventQueries, recordQueries }
}

/** Reads a STORED-entry ZIP back into `{ path: text }`. */
function unzip(bytes: Uint8Array): Map<string, string> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const decoder = new TextDecoder()
  const entries = new Map<string, string>()
  let end = bytes.length - 22
  while (end >= 0 && view.getUint32(end, true) !== 0x06054b50) end -= 1
  const count = view.getUint16(end + 10, true)
  let offset = view.getUint32(end + 16, true)
  for (let index = 0; index < count; index += 1) {
    const size = view.getUint32(offset + 24, true)
    const nameLength = view.getUint16(offset + 28, true)
    const extraLength = view.getUint16(offset + 30, true)
    const commentLength = view.getUint16(offset + 32, true)
    const localOffset = view.getUint32(offset + 42, true)
    const name = decoder.decode(bytes.subarray(offset + 46, offset + 46 + nameLength))
    const dataStart = localOffset + 30 + view.getUint16(localOffset + 26, true) + view.getUint16(localOffset + 28, true)
    entries.set(name, decoder.decode(bytes.subarray(dataStart, dataStart + size)))
    offset += 46 + nameLength + extraLength + commentLength
  }
  return entries
}

/** The sheet's rows as arrays of strings — what a spreadsheet would display. */
function sheetRows(file: ExportFile): string[][] {
  const sheet = unzip(file.bytes).get('xl/worksheets/sheet1.xml') ?? ''
  return [...sheet.matchAll(/<row r="\d+">([\s\S]*?)<\/row>/g)].map(([, body]) =>
    [...body.matchAll(/<c [^>]*?(?:\/>|>([\s\S]*?)<\/c>)/g)].map(([, cellBody]) => {
      const inline = /<is><t[^>]*>([\s\S]*?)<\/t><\/is>/.exec(cellBody ?? '')
      const numeric = /<v>([\s\S]*?)<\/v>/.exec(cellBody ?? '')
      return (inline?.[1] ?? numeric?.[1] ?? '')
        .replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"')
        .replace(/&apos;/g, "'").replace(/&amp;/g, '&')
    }),
  )
}

/** Opens the screen, picks the event, opens the dialog and chooses the day. */
async function openExportFor(role: 'admin' | 'teacher', records?: AttendanceRecord[]) {
  const user = userEvent.setup()
  const installed = install(role, records)
  render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/od-records'])} />)

  // Wait for the option, not merely for the select: the control renders with
  // only its placeholder while the event list is still in flight, and choosing
  // an id that has not arrived yet fails as "value not found".
  await screen.findByRole('option', { name: 'Amritotsavam 2026' })
  await user.selectOptions(screen.getByLabelText('Event'), eventID)
  await user.click(await screen.findByRole('button', { name: /export on-duty lists/i }))
  const dialog = await screen.findByRole('dialog', { name: /export on-duty lists/i })
  // A date input takes its value as a whole; typing into one character by
  // character is not what a picker does.
  fireEvent.change(within(dialog).getByLabelText('Day'), { target: { value: day } })
  return { user, dialog: within(dialog), ...installed }
}

beforeEach(() => {
  saved.length = 0
})

describe('the on-duty export', () => {
  test('[P0] export opens before the records page has an event selected', async () => {
    const user = userEvent.setup()
    const { eventQueries } = install('admin')
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/od-records'])} />)

    await user.click(await screen.findByRole('button', { name: /export on-duty lists/i }))
    const dialog = within(await screen.findByRole('dialog', { name: /export on-duty lists/i }))

    await waitFor(() => expect(eventQueries.some((url) => url.searchParams.get('page_size') === '100')).toBe(true))
    expect(dialog.getByLabelText('Day')).toBeInTheDocument()
    expect(dialog.getByLabelText('Event')).toBeInTheDocument()
    expect(dialog.getByText(/choose an event to see its categories/i)).toBeInTheDocument()
  })

  test('[P0] selected categories are read separately and merged into the chosen slot files', async () => {
    const user = userEvent.setup()
    const { recordQueries } = install('admin', dayRecords, categories)
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/od-records'])} />)

    await screen.findByRole('option', { name: 'Amritotsavam 2026' })
    await user.selectOptions(screen.getByLabelText('Event'), eventID)
    await user.click(await screen.findByRole('button', { name: /export on-duty lists/i }))
    const dialog = within(await screen.findByRole('dialog', { name: /export on-duty lists/i }))

    await waitFor(() => expect(dialog.getByLabelText('Category Technical Expo')).toBeChecked())
    await user.click(dialog.getByLabelText('Category Technical Expo'))
    fireEvent.change(dialog.getByLabelText('Day'), { target: { value: day } })

    await waitFor(() => expect(dialog.getByLabelText(/slot 1, 2 students/i)).toBeInTheDocument())
    expect(dialog.queryByLabelText(/slot 3,/i)).toBeNull()
    await user.click(dialog.getByLabelText(/slot 1,/i))
    await user.click(dialog.getByRole('button', { name: /^download$/i }))

    await waitFor(() => expect(saved).toHaveLength(1))
    expect(sheetRows(saved[0])).toEqual([
      ['Roll number', 'Name', 'Slot', 'Category'],
      ['CB.SC.U4CSE24101', 'Ananya Rao', '1', 'Cultural Programme'],
      ['CB.SC.U4ECE24007', 'Vikram Nair', '1', 'Sports Meet'],
    ])
    const exportQueries = recordQueries.filter((url) => url.searchParams.get('od_date') === day)
    expect(exportQueries.map((url) => url.searchParams.get('category_id')).sort()).toEqual([culturalID, sportsID].sort())
  })

  test('[P0] day download produces one sheet sorted by slot with serial numbers', async () => {
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    await user.click(dialog.getByRole('button', { name: /download day/i }))

    await waitFor(() => expect(saved).toHaveLength(1))
    expect(saved[0].fileName).toBe('2026-07-28-day.xlsx')
    expect(sheetRows(saved[0])).toEqual([
      ['Serial number', 'Roll number', 'Name', 'Slot', 'Category'],
      ['1', 'CB.SC.U4CSE24101', 'Ananya Rao', '1', 'Cultural Programme'],
      ['2', 'CB.SC.U4ECE24007', 'Vikram Nair', '1', 'Sports Meet'],
      ['3', 'CB.SC.U4CSE24102', 'Meera Suresh', '2', 'Procession'],
      ['4', 'CB.SC.U4CSE24103', 'Rahul Menon', '3', 'Technical Expo'],
      ['5', 'CB.SC.U4CSE24104', 'Nikhil Das', '7', 'Cultural Programme'],
    ])
  })

  test('[P0] day download keeps the selected category customization', async () => {
    const user = userEvent.setup()
    install('admin', dayRecords, categories)
    render(<Application session={sessionPort()} router={createPortalMemoryRouter(['/portal/od-records'])} />)

    await screen.findByRole('option', { name: 'Amritotsavam 2026' })
    await user.selectOptions(screen.getByLabelText('Event'), eventID)
    await user.click(await screen.findByRole('button', { name: /export on-duty lists/i }))
    const dialog = within(await screen.findByRole('dialog', { name: /export on-duty lists/i }))

    await waitFor(() => expect(dialog.getByLabelText('Category Technical Expo')).toBeChecked())
    await user.click(dialog.getByLabelText('Category Technical Expo'))
    fireEvent.change(dialog.getByLabelText('Day'), { target: { value: day } })
    await waitFor(() => expect(dialog.getByLabelText(/slot 1, 2 students/i)).toBeInTheDocument())
    await user.click(dialog.getByRole('button', { name: /download day/i }))

    await waitFor(() => expect(saved).toHaveLength(1))
    expect(sheetRows(saved[0])).toEqual([
      ['Serial number', 'Roll number', 'Name', 'Slot', 'Category'],
      ['1', 'CB.SC.U4CSE24101', 'Ananya Rao', '1', 'Cultural Programme'],
      ['2', 'CB.SC.U4ECE24007', 'Vikram Nair', '1', 'Sports Meet'],
      ['3', 'CB.SC.U4CSE24102', 'Meera Suresh', '2', 'Procession'],
      ['4', 'CB.SC.U4CSE24104', 'Nikhil Das', '7', 'Cultural Programme'],
    ])
  })

  test('[P0] three slots ticked downloads three files, named by day then slot', async () => {
    // The request, verbatim: choose the day, tick slots 1, 2 and 3, press
    // download, and receive three files called <date>-slot-<n>.xlsx.
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    await user.click(dialog.getByLabelText(/slot 1,/i))
    await user.click(dialog.getByLabelText(/slot 2,/i))
    await user.click(dialog.getByLabelText(/slot 3,/i))

    await user.click(dialog.getByRole('button', { name: /download 3 files/i }))

    await waitFor(() => expect(saved).toHaveLength(3))
    expect(saved.map((file) => file.fileName)).toEqual([
      '2026-07-28-slot-1.xlsx',
      '2026-07-28-slot-2.xlsx',
      '2026-07-28-slot-3.xlsx',
    ])
    expect(
      saved.map((file) => file.fileName).join(' '),
      'a slot that was never ticked was downloaded anyway',
    ).not.toContain('slot-7')
  })

  test('[P0] each file holds its own slot only, with every category in it', async () => {
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    await user.click(dialog.getByLabelText(/slot 1,/i))
    await user.click(dialog.getByLabelText(/slot 2,/i))
    await user.click(dialog.getByRole('button', { name: /download 2 files/i }))
    await waitFor(() => expect(saved).toHaveLength(2))

    // Slot 1: two students, two DIFFERENT categories, ordered by roll number.
    expect(sheetRows(saved[0])).toEqual([
      ['Roll number', 'Name', 'Slot', 'Category'],
      ['CB.SC.U4CSE24101', 'Ananya Rao', '1', 'Cultural Programme'],
      ['CB.SC.U4ECE24007', 'Vikram Nair', '1', 'Sports Meet'],
    ])
    // Slot 2: only its own student. Nobody from slot 1 leaked across.
    expect(sheetRows(saved[1])).toEqual([
      ['Roll number', 'Name', 'Slot', 'Category'],
      ['CB.SC.U4CSE24102', 'Meera Suresh', '2', 'Procession'],
    ])
  })

  test('[P0] a REMOVED record is never exported', async () => {
    // A removed row is a mark that was taken back. Exporting it puts a student
    // on an official list of people who were excused when the record says the
    // opposite — and the file is read by an office that cannot check.
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 3,/i)).toBeInTheDocument())
    await user.click(dialog.getByLabelText(/slot 3,/i))
    await user.click(dialog.getByRole('button', { name: /^download$/i }))
    await waitFor(() => expect(saved).toHaveLength(1))

    expect(sheetRows(saved[0])).toEqual([
      ['Roll number', 'Name', 'Slot', 'Category'],
      ['CB.SC.U4CSE24103', 'Rahul Menon', '3', 'Technical Expo'],
    ])
  })

  test('[P0] a teacher gets the export too, reading through their own scoped list', async () => {
    // The whole reason it lives on this screen. The record list is already
    // branch-scoped for a teacher, so the export needs no access rule of its own
    // — and a teacher who could not open it would have to ask an admin for a
    // list they are entitled to pull themselves.
    const { user, dialog, recordQueries } = await openExportFor('teacher')

    await waitFor(() => expect(dialog.getByLabelText(/slot 2,/i)).toBeInTheDocument())
    await user.click(dialog.getByLabelText(/slot 2,/i))
    await user.click(dialog.getByRole('button', { name: /^download$/i }))

    await waitFor(() => expect(saved).toHaveLength(1))
    expect(saved[0].fileName).toBe('2026-07-28-slot-2.xlsx')
    // Read through the ordinary scoped route, not through some admin-only one.
    expect(recordQueries.every((url) => url.pathname.includes('/portal/events/'))).toBe(true)
  })

  test('[P1] only the slots that were marked on that day are offered, with counts', async () => {
    const { dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1, 2 students/i)).toBeInTheDocument())
    expect(dialog.getByLabelText(/slot 2, 1 students/i)).toBeInTheDocument()
    expect(dialog.getByLabelText(/slot 3, 1 students/i)).toBeInTheDocument()
    expect(dialog.getByLabelText(/slot 7, 1 students/i)).toBeInTheDocument()
    expect(
      dialog.queryByLabelText(/slot 4,/i),
      'a slot with no marks is offered, so it can be selected and downloaded empty',
    ).toBeNull()
  })

  test('[P1] the export reads the DAY that was chosen, not the whole event', async () => {
    const { recordQueries } = await openExportFor('admin')

    await waitFor(() => {
      expect(recordQueries.some((url) => url.searchParams.get('od_date') === day)).toBe(true)
    })
  })

  test('[P1] a day with nothing marked says so instead of offering an empty download', async () => {
    const { dialog } = await openExportFor('admin', [])

    expect(await dialog.findByText(/no student was marked present on this day/i)).toBeInTheDocument()
    expect(dialog.getByRole('button', { name: /download day/i })).toBeDisabled()
    expect(dialog.getByRole('button', { name: /^download$/i })).toBeDisabled()
  })

  test('[P1] download is refused until at least one slot is chosen', async () => {
    const { dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    expect(dialog.getByRole('button', { name: /^download$/i })).toBeDisabled()
    expect(dialog.getByRole('button', { name: /download day/i })).toBeEnabled()
    expect(saved).toHaveLength(0)
  })

  test('[P1] select all ticks every offered slot and downloads one file each', async () => {
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    await user.click(dialog.getByRole('button', { name: /select all/i }))
    await user.click(dialog.getByRole('button', { name: /download 4 files/i }))

    await waitFor(() => expect(saved).toHaveLength(4))
    expect(saved.map((file) => file.fileName)).toEqual([
      '2026-07-28-slot-1.xlsx',
      '2026-07-28-slot-2.xlsx',
      '2026-07-28-slot-3.xlsx',
      '2026-07-28-slot-7.xlsx',
    ])
  })

  test('[P1] the files are named individually after the download', async () => {
    // A browser that silently blocked the second and third of three downloads
    // looks exactly like one that saved them. The names are what an
    // administrator then goes looking for in their downloads folder.
    const { user, dialog } = await openExportFor('admin')

    await waitFor(() => expect(dialog.getByLabelText(/slot 1,/i)).toBeInTheDocument())
    await user.click(dialog.getByLabelText(/slot 1,/i))
    await user.click(dialog.getByRole('button', { name: /^download$/i }))

    const announcement = await dialog.findByRole('status')
    expect(announcement).toHaveTextContent('2026-07-28-slot-1.xlsx')
  })
})
