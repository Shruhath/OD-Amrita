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

/**
 * The section these fixtures import into.
 *
 * Deliberately NOT 'A'. The harness seeds its card-populator students into
 * CSE/2024/A, and the populator journey asserts that the roll-ordered first two
 * rows of that section are exactly those two seeded students. These fixture
 * rolls are `CB.SC.U4CSE<random 5 digits><n>`, so whenever the run token starts
 * below "24" they sort AHEAD of the seeded rolls — and with `fullyParallel` an
 * import that lands first pushed the seeded rows down the list and failed a
 * journey that has nothing to do with importing. Roughly a quarter of tokens
 * qualify, which is why it read as flake rather than a bug.
 *
 * Nothing here depends on the section itself; the import path treats it as
 * opaque text. Owning a section of our own removes the interference at its
 * source instead of loosening the populator's ordering assertion.
 */
const rosterSection = 'C'

export function studentRosterRow(
  sequence: number,
  overrides: Partial<StudentRosterRow> = {},
): StudentRosterRow {
  return {
    rollNo: `CB.SC.U4CSE${rosterRunToken}${String(sequence).padStart(3, '0')}`,
    name: faker.person.fullName(),
    branch: 'CSE',
    section: rosterSection,
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
