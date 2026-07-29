import { expect, test } from '@playwright/test'
import { Buffer } from 'node:buffer'
import { studentRosterCsv, studentRosterRow, type StudentImportResult } from '../support/factories/student-roster'

const portalAPI = 'http://127.0.0.1:8081'
const first = studentRosterRow(201)
const second = studentRosterRow(202)
const rosterCsv = studentRosterCsv([first, second], ['Name', 'Roll No', 'Section', 'Branch', 'Batch'])

test('imports students through the real API and Postgres, then exact rerun is unchanged', async ({ page }) => {
  await page.goto('/integration.html?path=%2Fportal%2Fstudents')
  await expect(page.getByRole('heading', { name: 'Student roster', exact: true })).toBeVisible()

  await page.getByLabel('Student roster CSV').setInputFiles({
    name: 'students.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(rosterCsv, 'utf8'),
  })
  await page.getByRole('button', { name: 'Import students' }).click()

  const summary = page.getByRole('region', { name: 'Import summary' })
  await expect(summary.getByText(/Total rows\s*2/i)).toBeVisible()
  await expect(summary.getByText(/Inserted\s*2/i)).toBeVisible()
  await expect(summary.getByText(/Invalid\s*0/i)).toBeVisible()

  await page.reload()
  await expect(page.getByRole('heading', { name: 'Student roster', exact: true })).toBeVisible()

  const response = await page.request.post(`${portalAPI}/api/v1/admin/students/import`, {
    headers: { Authorization: 'Bearer portal-admin-token' },
    multipart: {
      file: {
        name: 'students.csv',
        mimeType: 'text/csv',
        buffer: Buffer.from(rosterCsv, 'utf8'),
      },
    },
  })
  expect(response.status()).toBe(201)
  const body = (await response.json()) as StudentImportResult
  expect(body).toMatchObject({
    total_rows: 2,
    inserted_rows: 0,
    updated_rows: 0,
    unchanged_rows: 2,
    invalid_rows: 0,
    errors: [],
  })
})

test('commits valid rows and shows invalid branch errors through the real stack', async ({ page }) => {
  const valid = studentRosterRow(211)
  const invalid = studentRosterRow(212, { branch: 'EEE' })
  await page.goto('/integration.html?path=%2Fportal%2Fstudents')
  await page.getByLabel('Student roster CSV').setInputFiles({
    name: 'mixed-students.csv',
    mimeType: 'text/csv',
    buffer: Buffer.from(studentRosterCsv([valid, invalid]), 'utf8'),
  })
  await page.getByRole('button', { name: 'Import students' }).click()

  const summary = page.getByRole('region', { name: 'Import summary' })
  await expect(summary.getByText(/Inserted\s*1/i)).toBeVisible()
  await expect(summary.getByText(/Invalid\s*1/i)).toBeVisible()
  await expect(page.getByRole('table', { name: 'Import errors' }).getByRole('row', { name: /branch.*unknown/i })).toBeVisible()
})

test('denies a real Student principal on the import route through the production middleware chain', async ({ request }) => {
  const response = await request.post(`${portalAPI}/api/v1/admin/students/import`, {
    headers: { Authorization: 'Bearer portal-student-token' },
    multipart: {
      file: {
        name: 'students.csv',
        mimeType: 'text/csv',
        buffer: Buffer.from(rosterCsv, 'utf8'),
      },
    },
  })
  expect(response.status()).toBe(403)
  await expect(response.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })
})
