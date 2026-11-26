import { readFileSync } from 'node:fs'
import { expect, test, type Download, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json } from './support/portal-session'

/**
 * The on-duty export, through a real browser that really saves files.
 *
 * THE ONE THING ONLY THIS LAYER CAN PROVE. The component tests replace the
 * saving step, so they show which files the flow *asked* for. Whether a browser
 * actually writes three separate files when three are requested in one gesture
 * is a browser question — pop-up blocking, download throttling and same-gesture
 * limits all live there — and it is the part an administrator notices when it
 * goes wrong, because two of their three slots simply never arrive.
 *
 * The saved bytes are then unzipped and read, so what is asserted is what a
 * spreadsheet would show, not what the page believed it produced.
 */

const eventID = '11111111-1111-4111-8111-111111111111'
const day = '2026-07-28'

type Row = { slot: number; roll: string; name: string; category: string; status?: 'present' | 'removed' }

const dayRows: Row[] = [
  { slot: 1, roll: 'CB.SC.U4ECE99007', name: 'Vikram Nair', category: 'Sports Meet' },
  { slot: 1, roll: 'CB.SC.U4CSE99101', name: 'Ananya Rao', category: 'Cultural Programme' },
  { slot: 2, roll: 'CB.SC.U4CSE99102', name: 'Meera Suresh', category: 'Procession' },
  { slot: 3, roll: 'CB.SC.U4CSE99103', name: 'Rahul Menon', category: 'Technical Expo' },
  { slot: 3, roll: 'CB.SC.U4CSE99999', name: 'Withdrawn Student', category: 'Technical Expo', status: 'removed' },
]

function apiRecord(row: Row, index: number) {
  return {
    record_id: `66000000-0000-4000-8000-0000000000${String(index).padStart(2, '0')}`,
    student: {
      student_id: `55000000-0000-4000-8000-0000000000${String(index).padStart(2, '0')}`,
      roll_no: row.roll,
      display_name: row.name,
      branch: 'CSE',
      section: 'A',
      batch: '2024',
    },
    event_id: eventID,
    category: { id: `94000000-0000-4000-8000-00000000000${row.slot}`, name: row.category, created_at: '2026-07-01T00:00:00Z' },
    slot: row.slot,
    od_date: day,
    status: row.status ?? 'present',
    reason_note: null,
    capture_method: 'search',
    marked_by: { user_id: '77777777-7777-4777-8777-777777777777', email: 'teacher@amrita.edu', name: 'E2E teacher' },
    actor_role: 'teacher',
    server_received_at: '2026-07-28T04:05:00Z',
  }
}

async function installRoutes(page: Page, role: 'admin' | 'teacher', rows: Row[] = dayRows) {
  await installPortalSession(page, role)
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)
    const url = new URL(request.url())

    if (url.pathname.endsWith('/api/v1/auth/me')) {
      return json(route, {
        user_id: '99999999-9999-4999-8999-999999999999',
        email: `${role}@amrita.edu`,
        name: `E2E ${role}`,
        role,
      })
    }
    if (url.pathname.endsWith('/portal/mark/events')) {
      return json(route, {
        data: [{
          id: eventID,
          name: 'Amritotsavam 2026',
          event_date: day,
          end_date: day,
          status: 'active',
          branches: [],
          created_at: '2026-07-01T00:00:00Z',
        }],
        total: 1,
        page: 1,
        page_size: 20,
      })
    }
    if (url.pathname.includes('/portal/mark/events/') && url.pathname.endsWith('/categories')) {
      return json(route, { data: [] })
    }
    if (url.pathname.includes('/portal/events/') && url.pathname.endsWith('/records')) {
      const odDate = url.searchParams.get('od_date')
      const pageNumber = Number(url.searchParams.get('page') ?? '1')
      const pageSize = Number(url.searchParams.get('page_size') ?? '50')
      const matching = odDate === null ? rows : rows.filter(() => odDate === day)
      const data = matching.map(apiRecord).slice((pageNumber - 1) * pageSize, pageNumber * pageSize)
      return json(route, { data, page: pageNumber, page_size: pageSize, total: matching.length })
    }
    // Anything else is a defect: the export must not reach for a route of its own.
    return json(route, { code: 'INTERNAL', message: `unexpected call to ${url.pathname}` }, 500)
  })
}

/** Reads a STORED-entry ZIP — enough of the format for what this app writes. */
function unzip(bytes: Buffer): Map<string, string> {
  const entries = new Map<string, string>()
  let end = bytes.length - 22
  while (end >= 0 && bytes.readUInt32LE(end) !== 0x06054b50) end -= 1
  const count = bytes.readUInt16LE(end + 10)
  let offset = bytes.readUInt32LE(end + 16)
  for (let index = 0; index < count; index += 1) {
    const size = bytes.readUInt32LE(offset + 24)
    const nameLength = bytes.readUInt16LE(offset + 28)
    const extraLength = bytes.readUInt16LE(offset + 30)
    const commentLength = bytes.readUInt16LE(offset + 32)
    const localOffset = bytes.readUInt32LE(offset + 42)
    const name = bytes.subarray(offset + 46, offset + 46 + nameLength).toString('utf8')
    const dataStart = localOffset + 30 + bytes.readUInt16LE(localOffset + 26) + bytes.readUInt16LE(localOffset + 28)
    entries.set(name, bytes.subarray(dataStart, dataStart + size).toString('utf8'))
    offset += 46 + nameLength + extraLength + commentLength
  }
  return entries
}

function sheetRows(file: string): string[][] {
  const bytes = readFileSync(file)
  const sheet = unzip(bytes).get('xl/worksheets/sheet1.xml') ?? ''
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

async function openDialog(page: Page) {
  await page.goto('/portal/od-records')
  await page.getByLabel('Event').selectOption(eventID)
  await page.getByRole('button', { name: /export on-duty lists/i }).click()
  const dialog = page.getByRole('dialog', { name: /export on-duty lists/i })
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('Day').fill(day)
  return dialog
}

test.describe('the on-duty export', () => {
  test('[P0] ticking three slots saves three files, one per slot, named by day and slot', async ({ page }) => {
    await installRoutes(page, 'admin')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 1,/i)).toBeVisible()
    await dialog.getByLabel(/slot 1,/i).check()
    await dialog.getByLabel(/slot 2,/i).check()
    await dialog.getByLabel(/slot 3,/i).check()

    // Every download from the single press, collected before it is triggered —
    // a browser that saves only the first of three is the failure this catches.
    const downloads: Download[] = []
    page.on('download', (download) => downloads.push(download))

    await dialog.getByRole('button', { name: /download 3 files/i }).click()

    await expect
      .poll(() => downloads.length, { message: 'the browser did not save one file per selected slot' })
      .toBe(3)

    expect(downloads.map((download) => download.suggestedFilename()).sort()).toEqual([
      '2026-07-28-slot-1.xlsx',
      '2026-07-28-slot-2.xlsx',
      '2026-07-28-slot-3.xlsx',
    ])
  })

  test('[P0] the saved workbook opens, and holds that slot with every category in it', async ({ page }) => {
    await installRoutes(page, 'admin')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 1,/i)).toBeVisible()
    await dialog.getByLabel(/slot 1,/i).check()

    const download = await Promise.all([
      page.waitForEvent('download'),
      dialog.getByRole('button', { name: /^download$/i }).click(),
    ]).then(([saved]) => saved)

    const path = await download.path()
    expect(path, 'the browser reported a download it did not save').not.toBeNull()

    // Read back out of the file on disk. Slot 1 holds two students from two
    // different verticals, ordered by roll number.
    expect(sheetRows(path)).toEqual([
      ['Roll number', 'Name', 'Slot', 'Category'],
      ['CB.SC.U4CSE99101', 'Ananya Rao', '1', 'Cultural Programme'],
      ['CB.SC.U4ECE99007', 'Vikram Nair', '1', 'Sports Meet'],
    ])
  })

  test('[P0] a removed record is absent from the saved file', async ({ page }) => {
    // Slot 3 has one present student and one whose mark was taken back. Exporting
    // the second would put a student on an official list of people who were
    // excused when the record says they were not.
    await installRoutes(page, 'admin')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 3,/i)).toBeVisible()
    await dialog.getByLabel(/slot 3,/i).check()

    const download = await Promise.all([
      page.waitForEvent('download'),
      dialog.getByRole('button', { name: /^download$/i }).click(),
    ]).then(([saved]) => saved)

    const rows = sheetRows(await download.path())
    expect(rows).toHaveLength(2)
    expect(rows.flat().join(' ')).not.toContain('Withdrawn Student')
  })

  test('[P0] a teacher can export too', async ({ page }) => {
    // The record list is already branch-scoped for a teacher, so the export
    // inherits the scope rather than carrying an access rule of its own.
    await installRoutes(page, 'teacher')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 2,/i)).toBeVisible()
    await dialog.getByLabel(/slot 2,/i).check()

    const download = await Promise.all([
      page.waitForEvent('download'),
      dialog.getByRole('button', { name: /^download$/i }).click(),
    ]).then(([saved]) => saved)

    expect(download.suggestedFilename()).toBe('2026-07-28-slot-2.xlsx')
  })

  test('[P1] only the slots marked on that day are offered', async ({ page }) => {
    await installRoutes(page, 'admin')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 1, 2 students/i)).toBeVisible()
    await expect(dialog.getByLabel(/slot 2, 1 students/i)).toBeVisible()
    await expect(
      dialog.getByLabel(/slot 4,/i),
      'a slot with no marks is offered, so it can be selected and downloaded empty',
    ).toHaveCount(0)
  })

  test('[P1] the dialog fits a phone without scrolling the page sideways', async ({ page }) => {
    // An administrator pulling a list between slots is doing it on a phone.
    await page.setViewportSize({ width: 390, height: 844 })
    await installRoutes(page, 'admin')
    const dialog = await openDialog(page)

    await expect(dialog.getByLabel(/slot 1,/i)).toBeVisible()
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, 'the export dialog makes the page scroll sideways on a phone').toBeLessThanOrEqual(1)

    const box = await dialog.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
  })
})
