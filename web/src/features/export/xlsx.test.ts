import { describe, expect, test } from 'vitest'
import { buildWorkbook, columnName, crc32, safeSheetName } from './xlsx'

/**
 * The workbook writer, read back.
 *
 * These unzip what the module produces and parse the XML out of it, rather than
 * asserting on the string it built. A test that compares generated XML to
 * expected XML passes just as happily for a file no spreadsheet can open — the
 * only question worth asking of a format writer is whether a reader can read it,
 * so every assertion here goes through the archive.
 *
 * A separate check exists outside the suite: `soffice --convert-to csv` on the
 * output of this writer, which is the one thing that proves a real spreadsheet
 * accepts it. It is not in CI because LibreOffice is not on the runners.
 */

/** Reads a STORED-entry ZIP — enough of the format for what this writer emits. */
function unzip(bytes: Uint8Array): Map<string, string> {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const decoder = new TextDecoder()
  const entries = new Map<string, string>()

  // Walk the central directory, which is what a real reader does — following
  // the local headers instead would not notice a directory that disagrees.
  let end = bytes.length - 22
  while (end >= 0 && view.getUint32(end, true) !== 0x06054b50) end -= 1
  if (end < 0) throw new Error('no end-of-central-directory record: this is not a ZIP')

  const count = view.getUint16(end + 10, true)
  let offset = view.getUint32(end + 16, true)

  for (let index = 0; index < count; index += 1) {
    if (view.getUint32(offset, true) !== 0x02014b50) throw new Error('corrupt central directory entry')
    const method = view.getUint16(offset + 10, true)
    const crc = view.getUint32(offset + 16, true)
    const size = view.getUint32(offset + 24, true)
    const nameLength = view.getUint16(offset + 28, true)
    const extraLength = view.getUint16(offset + 30, true)
    const commentLength = view.getUint16(offset + 32, true)
    const localOffset = view.getUint32(offset + 42, true)
    const name = decoder.decode(bytes.subarray(offset + 46, offset + 46 + nameLength))

    if (method !== 0) throw new Error(`entry ${name} is compressed; this reader only handles stored entries`)

    const localNameLength = view.getUint16(localOffset + 26, true)
    const localExtraLength = view.getUint16(localOffset + 28, true)
    const dataStart = localOffset + 30 + localNameLength + localExtraLength
    const data = bytes.subarray(dataStart, dataStart + size)

    expect(crc32(data), `the stored CRC for ${name} does not match its bytes`).toBe(crc)
    entries.set(name, decoder.decode(data))
    offset += 46 + nameLength + extraLength + commentLength
  }
  return entries
}

/** The cells of the first sheet, keyed by reference: `{ A1: 'Roll number' }`. */
function cellsOf(sheetXML: string): Record<string, string> {
  const cells: Record<string, string> = {}
  const pattern = /<c r="([A-Z]+\d+)"[^>]*?(\/>|>([\s\S]*?)<\/c>)/g
  let match = pattern.exec(sheetXML)
  while (match !== null) {
    const [, reference, selfClosing, body] = match
    if (selfClosing === '/>') {
      cells[reference] = ''
    } else {
      const inline = /<is><t[^>]*>([\s\S]*?)<\/t><\/is>/.exec(body ?? '')
      const numeric = /<v>([\s\S]*?)<\/v>/.exec(body ?? '')
      const raw = inline?.[1] ?? numeric?.[1] ?? ''
      cells[reference] = raw
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&quot;/g, '"')
        .replace(/&apos;/g, "'")
        .replace(/&amp;/g, '&')
    }
    match = pattern.exec(sheetXML)
  }
  return cells
}

const simpleSheet = {
  name: '2026-07-28 slot 3',
  header: ['Roll number', 'Name', 'Slot', 'Category'],
  rows: [
    ['CB.SC.U4CSE24101', 'Ananya Rao', 3, 'Procession'],
    ['CB.SC.U4CSE24102', 'Vikram Nair', 3, 'Cultural Programme'],
  ],
}

describe('the workbook writer', () => {
  test('[P0] produces an archive carrying every part a spreadsheet needs to open it', () => {
    const entries = unzip(buildWorkbook(simpleSheet))

    // Miss any one of these and the file opens as "corrupt" with no clue why.
    for (const part of [
      '[Content_Types].xml',
      '_rels/.rels',
      'xl/workbook.xml',
      'xl/_rels/workbook.xml.rels',
      'xl/styles.xml',
      'xl/worksheets/sheet1.xml',
    ]) {
      expect(entries.has(part), `the workbook is missing ${part}`).toBe(true)
    }

    // The relationship graph has to resolve: the package points at the workbook,
    // and the workbook points at the sheet it declares.
    expect(entries.get('_rels/.rels')).toContain('xl/workbook.xml')
    expect(entries.get('xl/_rels/workbook.xml.rels')).toContain('worksheets/sheet1.xml')
    expect(entries.get('xl/workbook.xml')).toContain('r:id="rId1"')
  })

  test('[P0] every cell reads back with the value and the type it was given', () => {
    const entries = unzip(buildWorkbook(simpleSheet))
    const cells = cellsOf(entries.get('xl/worksheets/sheet1.xml') ?? '')

    expect(cells.A1).toBe('Roll number')
    expect(cells.D1).toBe('Category')
    expect(cells.A2).toBe('CB.SC.U4CSE24101')
    expect(cells.B2).toBe('Ananya Rao')
    expect(cells.C2).toBe('3')
    expect(cells.D2).toBe('Procession')
    expect(cells.A3).toBe('CB.SC.U4CSE24102')
    expect(cells.D3).toBe('Cultural Programme')
  })

  test('[P0] a roll number stays TEXT, so leading zeros and long digit strings survive', () => {
    // The reason this writer exists rather than a CSV. `0024101` read as a
    // number becomes 24101, and a nineteen-digit id loses its tail to floating
    // point — both silently, both on the one column that identifies a student.
    const entries = unzip(buildWorkbook({
      name: 'roll numbers',
      header: ['Roll number'],
      rows: [['0024101'], ['9007199254740993']],
    }))
    const sheet = entries.get('xl/worksheets/sheet1.xml') ?? ''

    expect(sheet, 'a roll number was written as a numeric cell').toContain('r="A2" t="inlineStr"')
    const cells = cellsOf(sheet)
    expect(cells.A2).toBe('0024101')
    expect(cells.A3).toBe('9007199254740993')
  })

  test('[P0] a name containing XML metacharacters does not corrupt the file', () => {
    // An unescaped `&` in a student's name produces a workbook that will not
    // open at all, and the error names the file rather than the name.
    const entries = unzip(buildWorkbook({
      name: 'names',
      header: ['Name'],
      rows: [['Anand & Co'], ['Dance <Solo>'], ['O’Brien "AJ"']],
    }))
    const cells = cellsOf(entries.get('xl/worksheets/sheet1.xml') ?? '')

    expect(cells.A2).toBe('Anand & Co')
    expect(cells.A3).toBe('Dance <Solo>')
    expect(cells.A4).toBe('O’Brien "AJ"')
  })

  test('[P1] a sheet name is made legal rather than producing a file that will not open', () => {
    expect(safeSheetName('2026-07-28 slot 3')).toBe('2026-07-28 slot 3')
    expect(safeSheetName('a/b:c*d?e[f]g')).toBe('a b c d e f g')
    expect(safeSheetName('')).toBe('Sheet1')
    expect(safeSheetName('x'.repeat(40))).toHaveLength(31)
  })

  test('[P1] an empty slot still produces a readable file with its headings', () => {
    // Downloading nothing for a slot that had no marks is indistinguishable from
    // the export failing. A file with only headings says "nobody" out loud.
    const entries = unzip(buildWorkbook({ name: 'empty', header: ['Roll number', 'Name'], rows: [] }))
    const cells = cellsOf(entries.get('xl/worksheets/sheet1.xml') ?? '')

    expect(cells.A1).toBe('Roll number')
    expect(cells.A2).toBeUndefined()
  })

  test('[P1] the same table exported twice is byte-identical', () => {
    // Fixed timestamps. Two downloads of the same slot differ only when the data
    // differs, which is what makes "did anything change since I last pulled it"
    // answerable at all.
    expect(buildWorkbook(simpleSheet)).toEqual(buildWorkbook(simpleSheet))
  })

  test('[P1] column names run past Z', () => {
    expect(columnName(0)).toBe('A')
    expect(columnName(3)).toBe('D')
    expect(columnName(25)).toBe('Z')
    expect(columnName(26)).toBe('AA')
    expect(columnName(51)).toBe('AZ')
    expect(columnName(52)).toBe('BA')
  })

  test('[P1] the CRC is the standard one', () => {
    // Checked against a known vector rather than against itself: a self-
    // consistent but wrong CRC passes every round-trip test in this file and
    // fails in Excel.
    expect(crc32(new TextEncoder().encode('123456789'))).toBe(0xcbf43926)
  })
})
