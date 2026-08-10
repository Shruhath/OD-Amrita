/**
 * A minimal, dependency-free `.xlsx` writer — one sheet, four columns.
 *
 * WHY NOT A LIBRARY. The only thing this app exports is a flat table of strings
 * and one number, and every spreadsheet package on npm brings a formula engine,
 * a styling model and a chart renderer along with it — hundreds of kilobytes
 * shipped to a coordinator on mobile data so an administrator can download a
 * roll-number list. This is the whole of the format that a flat table needs.
 *
 * WHY NOT CSV, which would be ten lines. A `.csv` is a text file that Excel
 * *guesses* at, and it guesses wrong on exactly the column this export exists
 * to carry: a roll number like `CB.SC.U4CSE24101` survives, but any roster whose
 * ids are digits gets read as a number, loses its leading zeros and — past
 * fifteen digits — its last digits too, silently. A real workbook states the
 * type of every cell, so a roll number stays the string it is.
 *
 * The file is a ZIP of five XML parts. Entries are STORED rather than deflated:
 * the payload is a few kilobytes of highly compressible XML, and storing it
 * removes the only part of the format that needs a compressor at all. Both
 * Excel and LibreOffice read stored entries — the round trip is covered by this
 * module's tests, which unzip what it writes and read the cells back.
 *
 * Strings are written INLINE (`t="inlineStr"`) rather than through the shared
 * string table, which is the other part a writer this size can do without.
 */

export type SheetCell = string | number

export type Sheet = {
  /** The tab's name. Excel refuses `[]:*?/\`, and truncates past 31 characters. */
  name: string
  /** Row 1 — written bold and frozen so a long list keeps its headings. */
  header: readonly string[]
  rows: readonly (readonly SheetCell[])[]
  /** Column widths in Excel's character units, left to right. */
  columnWidths?: readonly number[]
}

/** Builds a single-sheet workbook. */
export function buildWorkbook(sheet: Sheet): Uint8Array {
  const parts: ZipEntry[] = [
    { name: '[Content_Types].xml', data: encode(contentTypes()) },
    { name: '_rels/.rels', data: encode(rootRels()) },
    { name: 'xl/workbook.xml', data: encode(workbook(sheet.name)) },
    { name: 'xl/_rels/workbook.xml.rels', data: encode(workbookRels()) },
    { name: 'xl/styles.xml', data: encode(styles()) },
    { name: 'xl/worksheets/sheet1.xml', data: encode(worksheet(sheet)) },
  ]
  return zip(parts)
}

/**
 * Excel rejects a workbook whose sheet name contains any of `[]:*?/\`, and
 * silently truncates past 31 characters — so the caller's name is made legal
 * here rather than producing a file that will not open.
 */
export function safeSheetName(name: string): string {
  const cleaned = name.replace(/[[\]:*?/\\]/g, ' ').trim()
  const collapsed = cleaned.replace(/\s+/g, ' ')
  return (collapsed === '' ? 'Sheet1' : collapsed).slice(0, 31)
}

// ─────────────────────────────────────────────────────────── the XML parts

const encoder = new TextEncoder()
const encode = (xml: string) => encoder.encode(xml)

/**
 * XML text escaping.
 *
 * Not optional and not cosmetic: a student named `Anand & Co` or a category
 * called `Dance <Solo>` produces a workbook Excel refuses to open at all, and
 * the failure surfaces as "the file is corrupt" with nothing pointing at the
 * name that caused it. Control characters are dropped rather than escaped —
 * XML 1.0 has no representation for most of them.
 */
function escapeXML(value: string): string {
  return value
    // eslint-disable-next-line no-control-regex
    .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F]/g, '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

const XML_DECL = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'

function contentTypes(): string {
  return `${XML_DECL}<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
    '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>' +
    '<Default Extension="xml" ContentType="application/xml"/>' +
    '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>' +
    '<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>' +
    '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>' +
    '</Types>'
}

function rootRels(): string {
  return `${XML_DECL}<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
    '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>' +
    '</Relationships>'
}

function workbook(sheetName: string): string {
  return `${XML_DECL}<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
    'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">' +
    `<sheets><sheet name="${escapeXML(safeSheetName(sheetName))}" sheetId="1" r:id="rId1"/></sheets>` +
    '</workbook>'
}

function workbookRels(): string {
  return `${XML_DECL}<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
    '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>' +
    '<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>' +
    '</Relationships>'
}

/**
 * Two styles: index 0 is the default, index 1 is bold. The minimum a workbook
 * needs is one font, one fill, one border and one cell format; the bold entry
 * is the only addition, and it is what row 1 uses.
 */
function styles(): string {
  return `${XML_DECL}<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
    '<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><name val="Calibri"/></font></fonts>' +
    '<fills count="1"><fill><patternFill patternType="none"/></fill></fills>' +
    '<borders count="1"><border/></borders>' +
    '<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>' +
    '<cellXfs count="2">' +
    '<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>' +
    '<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>' +
    '</cellXfs>' +
    '</styleSheet>'
}

/** A1, B1, … Z1, AA1 — the spreadsheet column name for a zero-based index. */
export function columnName(index: number): string {
  let name = ''
  let remaining = index
  do {
    name = String.fromCharCode(65 + (remaining % 26)) + name
    remaining = Math.floor(remaining / 26) - 1
  } while (remaining >= 0)
  return name
}

function cell(ref: string, value: SheetCell, bold: boolean): string {
  const style = bold ? ' s="1"' : ''
  if (typeof value === 'number' && Number.isFinite(value)) {
    return `<c r="${ref}"${style}><v>${value}</v></c>`
  }
  const text = typeof value === 'number' ? String(value) : value
  if (text === '') return `<c r="${ref}"${style}/>`
  // xml:space="preserve" so a value that is meaningfully padded is not trimmed
  // by the reader.
  return `<c r="${ref}"${style} t="inlineStr"><is><t xml:space="preserve">${escapeXML(text)}</t></is></c>`
}

function row(number: number, values: readonly SheetCell[], bold: boolean): string {
  const cells = values.map((value, index) => cell(`${columnName(index)}${number}`, value, bold)).join('')
  return `<row r="${number}">${cells}</row>`
}

function worksheet(sheet: Sheet): string {
  const widths = (sheet.columnWidths ?? []).map(
    (width, index) => `<col min="${index + 1}" max="${index + 1}" width="${width}" customWidth="1"/>`,
  ).join('')
  const body = [
    row(1, sheet.header, true),
    ...sheet.rows.map((values, index) => row(index + 2, values, false)),
  ].join('')
  return `${XML_DECL}<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
    // The header row stays on screen while a long list is scrolled. Without it
    // the fourth column of row 400 is an unlabelled string.
    '<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>' +
    (widths === '' ? '' : `<cols>${widths}</cols>`) +
    `<sheetData>${body}</sheetData>` +
    '</worksheet>'
}

// ─────────────────────────────────────────────────────────────── the ZIP

type ZipEntry = { name: string; data: Uint8Array }

const CRC_TABLE = (() => {
  const table = new Uint32Array(256)
  for (let index = 0; index < 256; index += 1) {
    let value = index
    for (let bit = 0; bit < 8; bit += 1) {
      value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1
    }
    table[index] = value >>> 0
  }
  return table
})()

export function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff
  for (let index = 0; index < bytes.length; index += 1) {
    crc = CRC_TABLE[(crc ^ bytes[index]) & 0xff] ^ (crc >>> 8)
  }
  return (crc ^ 0xffffffff) >>> 0
}

class ByteWriter {
  private bytes: number[] = []

  get length(): number {
    return this.bytes.length
  }

  u16(value: number) {
    this.bytes.push(value & 0xff, (value >>> 8) & 0xff)
  }

  u32(value: number) {
    this.bytes.push(value & 0xff, (value >>> 8) & 0xff, (value >>> 16) & 0xff, (value >>> 24) & 0xff)
  }

  raw(data: Uint8Array) {
    for (let index = 0; index < data.length; index += 1) this.bytes.push(data[index])
  }

  toUint8Array(): Uint8Array {
    return new Uint8Array(this.bytes)
  }
}

/**
 * A ZIP archive of stored (uncompressed) entries.
 *
 * Timestamps are fixed at 1980-01-01, the epoch of the ZIP date field. That is
 * deliberate: the same table exported twice produces byte-identical files, so a
 * test can assert on the bytes and a person can tell two downloads apart by
 * their content rather than by a clock.
 */
function zip(entries: readonly ZipEntry[]): Uint8Array {
  const out = new ByteWriter()
  const directory: { name: Uint8Array; crc: number; size: number; offset: number }[] = []

  for (const entry of entries) {
    const name = encoder.encode(entry.name)
    const crc = crc32(entry.data)
    directory.push({ name, crc, size: entry.data.length, offset: out.length })

    out.u32(0x04034b50)
    out.u16(20) // version needed
    out.u16(0) // flags
    out.u16(0) // method: stored
    out.u16(0) // modification time
    out.u16(33) // modification date: 1980-01-01
    out.u32(crc)
    out.u32(entry.data.length) // compressed size
    out.u32(entry.data.length) // uncompressed size
    out.u16(name.length)
    out.u16(0) // extra length
    out.raw(name)
    out.raw(entry.data)
  }

  const directoryOffset = out.length
  for (const item of directory) {
    out.u32(0x02014b50)
    out.u16(20) // version made by
    out.u16(20) // version needed
    out.u16(0) // flags
    out.u16(0) // method: stored
    out.u16(0) // modification time
    out.u16(33) // modification date
    out.u32(item.crc)
    out.u32(item.size)
    out.u32(item.size)
    out.u16(item.name.length)
    out.u16(0) // extra
    out.u16(0) // comment
    out.u16(0) // disk number
    out.u16(0) // internal attributes
    out.u32(0) // external attributes
    out.u32(item.offset)
    out.raw(item.name)
  }
  const directorySize = out.length - directoryOffset

  out.u32(0x06054b50)
  out.u16(0) // this disk
  out.u16(0) // disk with the directory
  out.u16(directory.length)
  out.u16(directory.length)
  out.u32(directorySize)
  out.u32(directoryOffset)
  out.u16(0) // comment length

  return out.toUint8Array()
}

/** What a browser must be told the bytes are, for the download to open in Excel. */
export const XLSX_MEDIA_TYPE =
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
