import { describe, expect, test, vi } from 'vitest'
import type { AttendanceRecord } from '../../api/client'
import { attendanceRecord } from '../../../tests/support/factories/teacher-admin-marking'
import {
  EXPORT_COLUMNS,
  EXPORT_PAGE_SIZE,
  ExportTooLarge,
  MAX_EXPORT_RECORDS,
  buildSlotWorkbook,
  buildSlotWorkbooks,
  exportFileName,
  readDay,
  rowsForSlot,
  slotCounts,
  slotsWithRecords,
} from './attendanceExport'

/**
 * What ends up in the file, and what must never.
 *
 * The export is handed to a department office and read as an official record of
 * who was excused. Two of these tests are about a person's attendance being
 * misreported rather than about a format — a removed record appearing, and a
 * slot's rows landing in another slot's file — and those are the ones worth
 * breaking the build over.
 */

const day = '2026-07-28'

type RecordOverrides = Partial<AttendanceRecord> & { rollNo?: string; name?: string; categoryName?: string }

function record(overrides: RecordOverrides = {}): AttendanceRecord {
  const { rollNo, name, categoryName, ...rest } = overrides
  const base = attendanceRecord(rest)
  return {
    ...base,
    od_date: rest.od_date ?? day,
    student: { ...base.student, roll_no: rollNo ?? base.student.roll_no, display_name: name ?? base.student.display_name },
    category: { ...base.category, name: categoryName ?? base.category.name },
  }
}

describe('the per-slot export', () => {
  test('[P0] a REMOVED record is never exported', () => {
    // The list this reads returns removed rows so the management screen can show
    // a correction history. A removed row is a mark that was TAKEN BACK, and
    // exporting one puts a student on an official list of people who were
    // excused when the record says they were not.
    const records = [
      record({ slot: 3, status: 'present', rollNo: 'CB.SC.U4CSE24101' }),
      record({ slot: 3, status: 'removed', rollNo: 'CB.SC.U4CSE24999' }),
    ]

    const rows = rowsForSlot(records, 3)

    expect(rows.map((row) => row.rollNo)).toEqual(['CB.SC.U4CSE24101'])
  })

  test('[P0] a slot file holds only that slot', () => {
    const records = [
      record({ slot: 3, rollNo: 'A-3' }),
      record({ slot: 4, rollNo: 'A-4' }),
      record({ slot: 3, rollNo: 'B-3' }),
    ]

    expect(rowsForSlot(records, 3).map((row) => row.rollNo)).toEqual(['A-3', 'B-3'])
    expect(rowsForSlot(records, 4).map((row) => row.rollNo)).toEqual(['A-4'])
  })

  test('[P0] EVERY category goes into the same file, named in its own column', () => {
    // The point of the layout. One file answers "who was on duty in slot 3"
    // completely; splitting by vertical would multiply the downloads and leave
    // the reader joining them back together.
    const records = [
      record({ slot: 3, rollNo: 'A', categoryName: 'Cultural Programme' }),
      record({ slot: 3, rollNo: 'B', categoryName: 'Sports Meet' }),
      record({ slot: 3, rollNo: 'C', categoryName: 'Procession' }),
    ]

    expect(rowsForSlot(records, 3).map((row) => row.category)).toEqual([
      'Cultural Programme',
      'Sports Meet',
      'Procession',
    ])
  })

  test('[P0] the four columns are roll number, name, slot and category', () => {
    expect(EXPORT_COLUMNS).toEqual(['Roll number', 'Name', 'Slot', 'Category'])

    const rows = rowsForSlot([record({ slot: 6, rollNo: 'CB.SC.U4CSE24101', name: 'Ananya Rao', categoryName: 'Procession' })], 6)

    expect(rows[0]).toEqual({ rollNo: 'CB.SC.U4CSE24101', name: 'Ananya Rao', slot: 6, category: 'Procession' })
  })

  test('[P0] one file per selected slot, named by day then slot', () => {
    // Three slots selected, three files — the whole request in one assertion.
    const records = [record({ slot: 1 }), record({ slot: 2 }), record({ slot: 3 })]

    const files = buildSlotWorkbooks(records, day, [3, 1, 2])

    expect(files.map((file) => file.fileName)).toEqual([
      '2026-07-28-slot-1.xlsx',
      '2026-07-28-slot-2.xlsx',
      '2026-07-28-slot-3.xlsx',
    ])
  })

  test('[P1] the name puts the date first, in ISO form', () => {
    // So a folder of these sorts chronologically, and so that 28-07 and 07-28
    // are not the same eight characters read two different ways.
    expect(exportFileName('2026-07-28', 3)).toBe('2026-07-28-slot-3.xlsx')
    expect(exportFileName('2026-08-01', 12)).toBe('2026-08-01-slot-12.xlsx')
  })

  test('[P1] rows are ordered by roll number, not by when the mark arrived', () => {
    // The list arrives newest-first. Exporting in that order gives the same slot
    // a different order every time it is pulled, which makes two exports of the
    // same data impossible to compare.
    const records = [
      record({ slot: 5, rollNo: 'CB.SC.U4CSE24103' }),
      record({ slot: 5, rollNo: 'CB.SC.U4CSE24101' }),
      record({ slot: 5, rollNo: 'CB.SC.U4CSE24102' }),
    ]

    expect(rowsForSlot(records, 5).map((row) => row.rollNo)).toEqual([
      'CB.SC.U4CSE24101',
      'CB.SC.U4CSE24102',
      'CB.SC.U4CSE24103',
    ])
  })

  test('[P1] only slots with present marks are offered, with their counts', () => {
    const records = [
      record({ slot: 2 }),
      record({ slot: 2 }),
      record({ slot: 6 }),
      record({ slot: 9, status: 'removed' }),
    ]

    expect(slotsWithRecords(records)).toEqual([2, 6])
    expect(slotCounts(records).get(2)).toBe(2)
    expect(slotCounts(records).get(6)).toBe(1)
    expect(
      slotCounts(records).get(9),
      'a slot whose only mark was removed is offered as though it had one',
    ).toBeUndefined()
  })

  test('[P1] a slot with no rows still builds a file', () => {
    const file = buildSlotWorkbook([], day, 7)

    expect(file.rowCount).toBe(0)
    expect(file.fileName).toBe('2026-07-28-slot-7.xlsx')
    expect(file.bytes.byteLength).toBeGreaterThan(0)
  })
})

describe('reading the whole day', () => {
  test('[P0] follows every page, so a big day is not silently truncated', async () => {
    // Reading one page and exporting it would produce a plausible-looking file
    // missing everybody after the first page, with nothing on screen saying so.
    //
    // Sized FROM EXPORT_PAGE_SIZE rather than from a literal, so raising the page
    // size keeps this test asserting what it is named for. Written against a
    // hardcoded 250 it silently became a one-page test the moment the page size
    // passed 250, which is exactly the truncation it exists to catch.
    const total = EXPORT_PAGE_SIZE * 2 + 50
    const all = Array.from({ length: total }, (_, index) => record({ slot: 3, rollNo: `R-${String(index).padStart(5, '0')}` }))
    const fetchPage = vi.fn(async (page: number, pageSize: number) => ({
      data: all.slice((page - 1) * pageSize, page * pageSize),
      total,
    }))

    const records = await readDay(fetchPage)

    expect(records).toHaveLength(total)
    expect(fetchPage).toHaveBeenCalledTimes(3)
    expect(fetchPage).toHaveBeenNthCalledWith(1, 1, EXPORT_PAGE_SIZE)
    expect(fetchPage).toHaveBeenNthCalledWith(3, 3, EXPORT_PAGE_SIZE)
  })

  test('[P0] reads the total once, not on every page', async () => {
    // Every page carries a `total`, and producing it costs the server a full
    // re-count of the filtered set. Trusting page one's removes half the
    // export's queries; this pins that the later pages' counts are ignored
    // rather than accidentally load-bearing.
    const total = EXPORT_PAGE_SIZE + 10
    const all = Array.from({ length: total }, (_, index) => record({ slot: 3, rollNo: `R-${String(index).padStart(5, '0')}` }))
    const fetchPage = vi.fn(async (page: number, pageSize: number) => ({
      data: all.slice((page - 1) * pageSize, page * pageSize),
      // A wildly different count on page two must change nothing: the loop is
      // terminated by the short page, and the first total already bounded it.
      total: page === 1 ? total : 999_999,
    }))

    const records = await readDay(fetchPage)

    expect(records).toHaveLength(total)
    expect(fetchPage).toHaveBeenCalledTimes(2)
  })

  test('[P0] a short page ends the read even if the total disagrees', async () => {
    // A server whose count and rows disagree would otherwise spin forever,
    // asking for page 4, 5, 6 … and hanging the dialog with no error.
    const fetchPage = vi.fn(async () => ({ data: [record({ slot: 1 })], total: 9_999 }))

    const records = await readDay(fetchPage)

    expect(records).toHaveLength(1)
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  test('[P1] refuses a day too large to read rather than issuing hundreds of requests', async () => {
    const fetchPage = vi.fn(async () => ({ data: [], total: MAX_EXPORT_RECORDS + 1 }))

    await expect(readDay(fetchPage)).rejects.toBeInstanceOf(ExportTooLarge)
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  test('[P1] an empty day reads as empty, not as a failure', async () => {
    const records = await readDay(async () => ({ data: [], total: 0 }))

    expect(records).toEqual([])
    expect(slotsWithRecords(records)).toEqual([])
  })
})
