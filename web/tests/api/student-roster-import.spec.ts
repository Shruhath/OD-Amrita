import { Buffer } from 'node:buffer'
import { expect, test, type APIRequestContext } from '@playwright/test'
import {
  studentRosterCsv,
  studentRosterRow,
  type StudentImportResult,
} from '../support/factories/student-roster'

const importURL = '/api/v1/admin/students/import'
const adminToken = 'portal-admin-token'
const studentToken = 'portal-student-token'

type ValidationFailure = {
  code: 'VALIDATION_FAILED'
  message: string
  details: Array<{ field: string; code: string; message: string }>
}

async function upload(
  request: APIRequestContext,
  csv: string,
  token = adminToken,
  filename = 'student-roster.csv',
) {
  return request.post(importURL, {
    headers: { Authorization: `Bearer ${token}` },
    multipart: {
      file: {
        name: filename,
        mimeType: 'text/csv',
        buffer: Buffer.from(csv, 'utf8'),
      },
    },
  })
}

async function expectValidationFailure(response: Awaited<ReturnType<typeof upload>>) {
  expect(response.status()).toBe(400)
  const body = (await response.json()) as ValidationFailure
  expect(body).toMatchObject({
    code: 'VALIDATION_FAILED',
    message: expect.any(String),
    details: expect.any(Array),
  })
  for (const detail of body.details) {
    expect(detail).toEqual({
      field: expect.any(String),
      code: expect.any(String),
      message: expect.any(String),
    })
  }
}

test.describe('Student roster import API', () => {
  test('[P0] imports UTF-8 CSV by normalized header name and reports reconciliation counts', async ({ request }) => {
    // Given a UTF-8 roster with reordered, aliased, and padded headers and values.
    const first = studentRosterRow(1)
    const csv = studentRosterCsv(
      [
        { ...first, rollNo: ` ${first.rollNo.toLowerCase()} `, branch: ' cse ' },
        studentRosterRow(2, { name: 'Ananya José' }),
      ],
      [' batch ', 'BRANCH', ' name ', 'roll_no', 'Section'],
    )

    // When an Admin uploads it through the multipart endpoint.
    const response = await upload(request, csv)
    // Then both rows are inserted and every reconciliation field is returned.
    expect(response.status()).toBe(201)
    const body = (await response.json()) as StudentImportResult
    expect(body).toEqual({
      import_id: expect.stringMatching(/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i),
      total_rows: 2,
      inserted_rows: 2,
      updated_rows: 0,
      unchanged_rows: 0,
      invalid_rows: 0,
      database_count: expect.any(Number),
      errors: [],
    })
    expect(body.database_count).toBeGreaterThanOrEqual(2)
  })

  test('[P0] commits valid rows while returning typed errors for every invalid row', async ({ request }) => {
    // Given one valid row and every required row-level rejection family.
    const valid = studentRosterRow(10)
    const rows = [
      valid,
      studentRosterRow(11, { name: '' }),
      studentRosterRow(12, { rollNo: valid.rollNo }),
      studentRosterRow(13, { branch: 'UNKNOWN' }),
      studentRosterRow(14, { name: 'N'.repeat(300) }),
      studentRosterRow(15, { rollNo: 'bad roll!' }),
    ]
    // Trailing raw row: valid roll, but no Batch column value at all. Its
    // section matches the fixture section for the same isolation reason.
    const csv = `${studentRosterCsv(rows)}${studentRosterRow(16).rollNo},Missing Batch,CSE,C\n`

    // When the mixed-validity roster is uploaded.
    const response = await upload(request, csv)
    // Then the valid row commits and each invalid row has an actionable error.
    expect(response.status()).toBe(201)
    const body = (await response.json()) as StudentImportResult
    expect(body).toMatchObject({
      total_rows: 7,
      inserted_rows: 1,
      updated_rows: 0,
      unchanged_rows: 0,
      invalid_rows: 6,
      database_count: expect.any(Number),
    })
    expect(body.errors).toHaveLength(6)
    expect(body.errors).toEqual(expect.arrayContaining([
      expect.objectContaining({ row: 3, field: 'name', code: expect.any(String) }),
      expect.objectContaining({ row: 4, field: 'roll_no', code: expect.any(String) }),
      expect.objectContaining({ row: 5, field: 'branch', code: expect.any(String) }),
      expect.objectContaining({ row: 6, field: 'name', code: expect.any(String) }),
      expect.objectContaining({ row: 7, field: 'roll_no', code: expect.any(String) }),
      expect.objectContaining({ row: 8, field: expect.any(String), code: expect.any(String) }),
    ]))
  })

  test('[P0] treats an exact rerun as unchanged without creating duplicate students', async ({ request }) => {
    // Given a roster that has already been imported successfully.
    const csv = studentRosterCsv([studentRosterRow(20), studentRosterRow(21)])
    const first = await upload(request, csv)
    expect(first.status()).toBe(201)
    const firstBody = (await first.json()) as StudentImportResult
    expect(firstBody).toMatchObject({
      inserted_rows: 2,
      updated_rows: 0,
      unchanged_rows: 0,
      invalid_rows: 0,
    })

    // When the exact same bytes are imported again.
    const second = await upload(request, csv)
    // Then no new rows are inserted or updated and both rows are unchanged.
    expect(second.status()).toBe(201)
    const secondBody = (await second.json()) as StudentImportResult
    expect(secondBody).toMatchObject({
      total_rows: 2,
      inserted_rows: 0,
      updated_rows: 0,
      unchanged_rows: 2,
      invalid_rows: 0,
      database_count: firstBody.database_count,
      errors: [],
    })
    expect(secondBody.import_id).not.toBe(firstBody.import_id)
  })

  test('[P1] reports changed rows as updates rather than inserts', async ({ request }) => {
    // Given an existing canonical roll whose mutable data changes.
    const original = studentRosterRow(30)
    const first = await upload(request, studentRosterCsv([original]))
    expect(first.status()).toBe(201)

    // When the changed row is uploaded again.
    const second = await upload(
      request,
      studentRosterCsv([{ ...original, name: 'Updated Student Name', section: 'B' }]),
    )
    // Then it is classified as one update, not a new student.
    expect(second.status()).toBe(201)
    await expect(second.json()).resolves.toMatchObject({
      total_rows: 1,
      inserted_rows: 0,
      updated_rows: 1,
      unchanged_rows: 0,
      invalid_rows: 0,
      errors: [],
    })
  })

  test('[P1] rejects an import with zero valid rows using the typed validation envelope', async ({ request }) => {
    // Given a roster whose every data row is invalid.
    const csv = studentRosterCsv([
      studentRosterRow(40, { name: '' }),
      studentRosterRow(41, { branch: 'UNKNOWN' }),
    ])
    // When it is uploaded, then the whole import is rejected before summary/audit creation.
    await expectValidationFailure(await upload(request, csv))
  })

  test('[P1] rejects malformed CSV using the typed validation envelope', async ({ request }) => {
    // Given malformed quoting, when uploaded, then the file-level failure uses the typed envelope.
    const csv = 'Roll No,Name,Branch,Section,Batch\nCB.SC.U4CSE24001,"unterminated,CSE,A,2024\n'
    await expectValidationFailure(await upload(request, csv))
  })

  test('[P1] rejects missing or duplicate required headers without relying on column position', async ({ request }) => {
    // Given missing and normalization-colliding required headers.
    const record = studentRosterRow(50)
    for (const csv of [
      'Roll No,Name,Branch,Section\nCB.SC.U4CSE24001,Missing Batch,CSE,A\n',
      studentRosterCsv([record], ['Roll No', 'roll_number', 'Name', 'Branch', 'Section', 'Batch']),
    ]) {
      // When each file is uploaded, then ambiguous/missing selection is rejected.
      await expectValidationFailure(await upload(request, csv))
    }
  })

  test('[P0] denies a Student principal with the canonical RBAC error', async ({ request }) => {
    // Given a Student token, when the protected endpoint is called, then RBAC denies it.
    const response = await upload(request, studentRosterCsv([studentRosterRow(60)]), studentToken)
    expect(response.status()).toBe(403)
    await expect(response.json()).resolves.toMatchObject({
      code: 'RBAC_FORBIDDEN',
      message: expect.any(String),
    })
  })

  test('[P1] rejects a missing bearer token with the canonical authentication error', async ({ request }) => {
    // Given no bearer token, when a roster is uploaded, then authentication rejects it.
    const response = await request.post(importURL, {
      multipart: {
        file: {
          name: 'student-roster.csv',
          mimeType: 'text/csv',
          buffer: Buffer.from(studentRosterCsv([studentRosterRow(70)]), 'utf8'),
        },
      },
    })
    expect(response.status()).toBe(401)
    await expect(response.json()).resolves.toMatchObject({
      code: 'AUTH_TOKEN_INVALID',
      message: expect.any(String),
    })
  })
})
