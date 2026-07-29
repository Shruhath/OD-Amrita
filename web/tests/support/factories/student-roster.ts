import { faker } from '@faker-js/faker'

export type StudentRosterRow = {
  rollNo: string
  name: string
  branch: string
  section: string
  batch: string
}

export type StudentImportError = {
  row: number
  field: string
  code: string
  message: string
}

export type StudentImportResult = {
  import_id: string
  total_rows: number
  inserted_rows: number
  updated_rows: number
  unchanged_rows: number
  invalid_rows: number
  database_count: number
  errors: StudentImportError[]
}

const rosterRunToken = faker.string.numeric(5)

export function studentRosterRow(
  sequence: number,
  overrides: Partial<StudentRosterRow> = {},
): StudentRosterRow {
  return {
    rollNo: `CB.SC.U4CSE${rosterRunToken}${String(sequence).padStart(3, '0')}`,
    name: faker.person.fullName(),
    branch: 'CSE',
    section: 'A',
    batch: '2024',
    ...overrides,
  }
}

function headerKey(header: string): keyof StudentRosterRow {
  const normalized = header.trim().toLowerCase().replace(/[_-]+/g, ' ').replace(/\s+/g, ' ')
  if (normalized === 'roll no' || normalized === 'roll number') return 'rollNo'
  if (normalized === 'name') return 'name'
  if (normalized === 'branch') return 'branch'
  if (normalized === 'section') return 'section'
  if (normalized === 'batch') return 'batch'
  throw new Error(`Unsupported fixture header: ${header}`)
}

export function studentRosterCsv(
  rows: StudentRosterRow[],
  headers = ['Roll No', 'Name', 'Branch', 'Section', 'Batch'],
): string {
  const lines = [
    headers.join(','),
    ...rows.map((row) => headers.map((header) => row[headerKey(header)]).join(',')),
  ]
  return `${lines.join('\n')}\n`
}

export function studentImportResult(
  overrides: Partial<StudentImportResult> = {},
): StudentImportResult {
  return {
    import_id: '77777777-7777-4777-8777-777777777777',
    total_rows: 2,
    inserted_rows: 2,
    updated_rows: 0,
    unchanged_rows: 0,
    invalid_rows: 0,
    database_count: 901,
    errors: [],
    ...overrides,
  }
}
