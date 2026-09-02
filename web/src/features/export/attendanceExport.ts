import type { AttendanceRecord } from '../../api/client'
import { XLSX_MEDIA_TYPE, buildWorkbook } from './xlsx'

/**
 * The per-slot on-duty export.
 *
 * ONE FILE PER SLOT, because that is the unit the office actually works in: a
 * slot is one period of one day, and the person receiving the list is checking
 * who missed that period. A single workbook covering the day would have to be
 * filtered before it could be used, by hand, once per slot.
 *
 * ALL CATEGORIES IN EACH FILE. Cultural, sports, technical and the rest go in
 * together with the category named in its own column, so one file answers "who
 * was on duty in slot 3" completely. Splitting by category as well would
 * multiply the downloads by the number of verticals and leave the reader
 * joining them back together.
 *
 * PRESENT RECORDS ONLY. The list this reads returns removed rows too, so that
 * the management screen can show a correction history — a removed row is a mark
 * that was taken back. Exporting one would put a student on an official list of
 * people who were excused when the record says they were not.
 */

/** The four columns, in order. */
export const EXPORT_COLUMNS = ['Roll number', 'Name', 'Slot', 'Category'] as const

export type ExportRow = {
  rollNo: string
  name: string
  slot: number
  category: string
}

/**
 * `2026-07-28-slot-3.xlsx` — the day first, then the slot.
 *
 * The date leads so that a folder of these sorts chronologically by name, and
 * it is the ISO form rather than a local one because `28-07-2026` and
 * `07-28-2026` are the same eight characters read two different ways.
 */
export function exportFileName(odDate: string, slot: number): string {
  return `${odDate}-slot-${slot}.xlsx`
}

/** The tab inside the workbook. Excel caps this at 31 characters. */
export function exportSheetName(odDate: string, slot: number): string {
  return `${odDate} slot ${slot}`
}

/**
 * Turns the day's records into the rows for ONE slot.
 *
 * Ordered by roll number, which is the order the receiving office reads in and
 * is stable across exports — the list arrives ordered by when each mark was
 * received, which would put the same students in a different order every time
 * the same slot is exported.
 */
export function rowsForSlot(records: readonly AttendanceRecord[], slot: number): ExportRow[] {
  return records
    .filter((record) => record.slot === slot && record.status === 'present')
    .map((record) => ({
      rollNo: record.student.roll_no,
      name: record.student.display_name,
      slot: record.slot,
      category: record.category.name,
    }))
    .sort((left, right) => left.rollNo.localeCompare(right.rollNo, 'en'))
}

/** The slots that actually have present marks on this day, ascending. */
export function slotsWithRecords(records: readonly AttendanceRecord[]): number[] {
  const slots = new Set<number>()
  for (const record of records) {
    if (record.status === 'present') slots.add(record.slot)
  }
  return [...slots].sort((left, right) => left - right)
}

/** How many students each slot would export, for the picker to show. */
export function slotCounts(records: readonly AttendanceRecord[]): Map<number, number> {
  const counts = new Map<number, number>()
  for (const record of records) {
    if (record.status !== 'present') continue
    counts.set(record.slot, (counts.get(record.slot) ?? 0) + 1)
  }
  return counts
}

export type ExportFile = {
  fileName: string
  mediaType: string
  bytes: Uint8Array
  rowCount: number
}

/** Builds the workbook for one slot. */
export function buildSlotWorkbook(records: readonly AttendanceRecord[], odDate: string, slot: number): ExportFile {
  const rows = rowsForSlot(records, slot)
  const bytes = buildWorkbook({
    name: exportSheetName(odDate, slot),
    header: [...EXPORT_COLUMNS],
    rows: rows.map((row) => [row.rollNo, row.name, row.slot, row.category]),
    columnWidths: [22, 30, 8, 26],
  })
  return { fileName: exportFileName(odDate, slot), mediaType: XLSX_MEDIA_TYPE, bytes, rowCount: rows.length }
}

/** Builds one workbook per selected slot, in ascending slot order. */
export function buildSlotWorkbooks(
  records: readonly AttendanceRecord[],
  odDate: string,
  slots: readonly number[],
): ExportFile[] {
  return [...slots]
    .sort((left, right) => left - right)
    .map((slot) => buildSlotWorkbook(records, odDate, slot))
}

// ───────────────────────────────────────────────────────── reading the day

/** The most rows one export will read before it stops and says so. */
export const MAX_EXPORT_RECORDS = 20_000

/**
 * The page size the reader asks for. The management record endpoint caps this
 * at 100, even though the older admin attendance endpoint allows a larger bulk
 * page. The export reads through the management route so teachers get the same
 * branch-scoped rows they see on screen; the page size must follow that route's
 * contract.
 */
export const EXPORT_PAGE_SIZE = 100

export class ExportTooLarge extends Error {
  readonly total: number

  constructor(total: number) {
    super(`This day holds ${total} records, which is more than one export can read. Narrow it by category and try again.`)
    this.name = 'ExportTooLarge'
    this.total = total
  }
}

type RecordPage = { data: AttendanceRecord[]; total: number }

/**
 * Reads EVERY record for one day, following the pages.
 *
 * The whole day is read once and then split by slot in memory, rather than
 * re-reading per slot: exporting five slots would otherwise re-fetch the same
 * pages five times, and the counts shown in the picker would come from a
 * different read than the files do — so a slot could be offered as "42
 * students" and download 41.
 */
export async function readDay(
  fetchPage: (page: number, pageSize: number) => Promise<RecordPage>,
  options: { categoryID?: string } = {},
): Promise<AttendanceRecord[]> {
  void options
  const collected: AttendanceRecord[] = []
  // The FIRST page's total is the one that decides how far to read.
  //
  // Every page carries a `total`, and each one costs the server a full re-count
  // of the filtered set — the same count, recomputed, once per page. Reading it
  // from page one and not looking again removes half the export's queries.
  //
  // The risk this trades away is a day that grows mid-export. It is bounded on
  // both sides: the short-page check below is what actually terminates the loop,
  // so extra rows appearing cannot cut the read short, and rows landing after
  // page one simply arrive in a later page. A day that SHRINKS mid-export
  // (a correction removing a record) ends the loop early on a short page, which
  // is the same answer re-reading the count would have given.
  let total = Number.POSITIVE_INFINITY
  let page = 1
  for (;;) {
    const result = await fetchPage(page, EXPORT_PAGE_SIZE)
    if (page === 1) {
      if (result.total > MAX_EXPORT_RECORDS) throw new ExportTooLarge(result.total)
      total = result.total
    }
    collected.push(...result.data)
    // Stop on a short page as well as on the count: a page that comes back
    // smaller than asked for is the last one, and trusting only `total` turns a
    // server that disagrees with itself into an endless loop.
    if (result.data.length < EXPORT_PAGE_SIZE || collected.length >= total) break
    page += 1
  }
  return collected
}

/**
 * Hands the bytes to the browser as a download.
 *
 * Injected as a parameter everywhere it is used, so the flow can be tested
 * without a DOM that actually saves files.
 */
export function downloadFile(file: ExportFile): void {
  const blob = new Blob([file.bytes as unknown as BlobPart], { type: file.mediaType })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = file.fileName
  anchor.rel = 'noopener'
  document.body.append(anchor)
  anchor.click()
  anchor.remove()
  // Revoked on the next turn rather than immediately: Safari has not started
  // reading the blob when click() returns, and revoking synchronously cancels
  // the download it just began.
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
