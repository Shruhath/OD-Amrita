import { Buffer } from 'node:buffer'
import { expect, test, type Page, type Request } from '@playwright/test'
import {
  studentImportResult,
  studentRosterCsv,
  studentRosterRow,
  type StudentImportResult,
} from '../support/factories/student-roster'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

type MockOptions = {
  role?: 'admin' | 'student'
  result?: StudentImportResult
}

const successfulImport = studentImportResult()

const partialImport = studentImportResult({
  import_id: '88888888-8888-4888-8888-888888888888',
  total_rows: 3,
  inserted_rows: 1,
  updated_rows: 1,
  invalid_rows: 1,
  database_count: 902,
  errors: [
    {
      row: 3,
      field: 'branch',
      code: 'UNKNOWN_BRANCH',
      message: 'Branch code ECE does not exist.',
    },
  ],
})

async function mockRosterApi(page: Page, options: MockOptions = {}) {
  const role = options.role ?? 'admin'
  const requests: Request[] = []

  await installPortalSession(page, role)
  await page.route('**/api/v1/auth/me', (route) =>
    json(route, {
      subject: `roster-${role}`,
      email: `${role}@amrita.edu`,
      name: `Roster ${role}`,
      role,
    }),
  )
  await page.route('**/api/v1/admin/students/import', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    if (route.request().method() !== 'POST') return route.continue()
    requests.push(route.request())
    return json(route, options.result ?? successfulImport)
  })

  return requests
}

const validCsv = studentRosterCsv([
  studentRosterRow(1, { name: 'Mādhav Rao' }),
  studentRosterRow(2, { name: 'Ananya Das' }),
], ['Name', 'Roll No', 'Section', 'Branch', 'Batch'])

async function chooseRosterCsv(page: Page, csv: string) {
  await page.getByLabel('Student roster CSV').setInputFiles({
    name: 'students.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(csv, 'utf8'),
  })
}

async function importRoster(page: Page) {
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/v1/admin/students/import') &&
      candidate.request().method() === 'POST',
  )
  await page.getByRole('button', { name: 'Import students' }).click()
  await response
}

test.describe('Student roster import', () => {
  test('[P0] uploads a UTF-8 CSV as the file multipart field and displays the import summary', async ({ page }) => {
    // Given an authenticated Admin and a successful import response.
    const requests = await mockRosterApi(page)
    await page.goto('/portal/students')

    await expect(page.getByRole('heading', { name: 'Student roster', exact: true })).toBeVisible()
    // When the Admin selects and uploads a UTF-8 CSV.
    await chooseRosterCsv(page, validCsv)
    await importRoster(page)

    // Then one multipart request is sent and the complete summary is visible.
    expect(requests).toHaveLength(1)
    const request = requests[0]
    expect(request.headers()['content-type']).toMatch(/^multipart\/form-data;\s*boundary=/)
    const multipartBody = request.postData() ?? ''
    expect(multipartBody).toContain('name="file"')
    expect(multipartBody).toContain('filename="students.csv"')
    expect(multipartBody).toContain('Mādhav Rao')

    const summary = page.getByRole('region', { name: 'Import summary' })
    await expect(summary.getByText(/Total rows\s*2/i)).toBeVisible()
    await expect(summary.getByText(/Inserted\s*2/i)).toBeVisible()
    await expect(summary.getByText(/Updated\s*0/i)).toBeVisible()
    await expect(summary.getByText(/Unchanged\s*0/i)).toBeVisible()
    await expect(summary.getByText(/Invalid\s*0/i)).toBeVisible()
    await expect(summary.getByText(/Students in database\s*901/i)).toBeVisible()
  })

  test('[P0] reports partial success counts and identifies each invalid CSV row', async ({ page }) => {
    // Given an Admin import that commits valid rows and returns one row error.
    await mockRosterApi(page, { result: partialImport })
    await page.goto('/portal/students')
    await chooseRosterCsv(page, studentRosterCsv([
      studentRosterRow(3, { name: 'Kiran Nair' }),
      studentRosterRow(4, { name: 'Diya Menon' }),
      studentRosterRow(5, { name: 'Rahul Sen', branch: 'ECE' }),
    ]))
    // When the mixed-validity CSV is uploaded.
    await importRoster(page)

    // Then reconciliation counts and the actionable row error are visible.
    const summary = page.getByRole('region', { name: 'Import summary' })
    await expect(summary.getByText(/Total rows\s*3/i)).toBeVisible()
    await expect(summary.getByText(/Inserted\s*1/i)).toBeVisible()
    await expect(summary.getByText(/Updated\s*1/i)).toBeVisible()
    await expect(summary.getByText(/Unchanged\s*0/i)).toBeVisible()
    await expect(summary.getByText(/Invalid\s*1/i)).toBeVisible()
    await expect(summary.getByText(/Students in database\s*902/i)).toBeVisible()

    const errors = page.getByRole('table', { name: 'Import errors' })
    await expect(errors.getByRole('row', { name: /3.*UNKNOWN_BRANCH.*Branch code ECE does not exist\./i })).toBeVisible()
  })

  test('[P1] denies a student without issuing any roster import request', async ({ page }) => {
    // Given an authenticated Student principal.
    const requests = await mockRosterApi(page, { role: 'student' })
    const observedRosterRequests: string[] = []
    page.on('request', (request) => {
      if (request.url().includes('/api/v1/admin/students')) observedRosterRequests.push(request.url())
    })

    // When the Student navigates to the roster URL.
    await page.goto('/portal/students')

    // Then access is denied before any roster request or upload control exists.
    await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
    await expect(page.getByLabel('Student roster CSV')).toHaveCount(0)
    expect(requests).toHaveLength(0)
    expect(observedRosterRequests).toHaveLength(0)
  })
})
