import { expect, test } from '@playwright/test'

/**
 * The real-stack teacher/admin search-mark and attendance-view journey.
 *
 * Runs the real `web/` app against the real Go API and real Postgres through
 * `api/test/portal/main.go`, using the same `/integration.html?role=X&path=Y`
 * harness entry every other real-stack spec in this directory uses (see
 * coordinator-marking.spec.ts and teacher-profiles.spec.ts) — NOT the mocked
 * `installPortalSession` helper from tests/e2e/support, which only fakes the
 * Supabase auth endpoint and never talks to this harness's fixed verifier.
 *
 * TWO HARNESS FACTS this depends on, both seeded by api/test/portal/main.go:
 *
 *  1. A `role = 'teacher'` principal behind `portal-teacher-token`, already
 *     branch-scoped to CSE via `user_branches`.
 *  2. An active event dated TODAY (the same `93000000-...0001` marking event
 *     the coordinator journey uses) with a CSE-branch association, plus a CSE
 *     roster (CB.SC.U4CSE24001-007) and one ECE student
 *     (CB.SC.U4ECE24001) outside the teacher's branch.
 */

const portalAPI = 'http://127.0.0.1:8081'
const MARKING_EVENT_ID = '93000000-0000-4000-8000-000000000001'

async function openTeacherMarkScreen(page: import('@playwright/test').Page) {
  await page.goto('/integration.html?role=teacher&path=%2Fportal%2Fmark')
  await expect(page.getByRole('heading', { name: /mark attendance/i })).toBeVisible()
}

test.describe('real stack teacher/admin search marking', () => {
  test('[P0] teacher search-marks with an explicit slot and no device capture controls', async ({ page }, testInfo) => {
    // A DIFFERENT student per attempt, matching coordinator-marking.spec.ts's
    // reasoning: the harness clears od_records once at boot, not per test, and
    // CI retries — re-marking the same student in the same slot would return
    // SCAN_DUPLICATE and turn any flake into a hard red. CB.SC.U4CSE24006/007
    // are dedicated to this spec (seeded in api/test/portal/main.go) —
    // CSE24001-003 belongs to coordinator-marking.spec.ts and CSE24004-005 to
    // capture-credits.spec.ts; reusing either pool put two legitimate marks in
    // the same unfiltered admin attendance view this test reads.
    const rolls = ['CB.SC.U4CSE24006', 'CB.SC.U4CSE24007']
    const roll = rolls[testInfo.retry % rolls.length]

    await openTeacherMarkScreen(page)

    await expect(page.getByRole('button', { name: /tap|nfc/i })).toHaveCount(0)
    await expect(page.getByRole('button', { name: /camera|scan/i })).toHaveCount(0)

    // Pin the event EXPLICITLY rather than accepting whichever one the select
    // defaults to. The markable-events query orders by `event_date ASC`, so the
    // default is simply the teacher's earliest active event — and every story
    // that seeds another active event in the CSE branch silently moves it. This
    // test marks on one event and then asserts against a DIFFERENT event's
    // attendance view (MARKING_EVENT_ID); leaving the selection implicit made
    // those two the same event only by luck. Selecting it here makes the two
    // ends agree by construction, which is the property the test is really about.
    await page.getByRole('combobox', { name: /event/i }).selectOption(MARKING_EVENT_ID)

    await page.getByRole('combobox', { name: /slot/i }).selectOption('4')
    await page.getByRole('searchbox', { name: /student/i }).fill(roll)
    await page.getByRole('option', { name: new RegExp(roll.replace(/\./g, '\\.'), 'i') }).click()
    await page.getByRole('button', { name: /mark present/i }).click()

    await expect(page.getByRole('status')).toContainText(/marked|present/i)

    await page.goto(`/integration.html?role=admin&path=%2Fportal%2Fevents%2F${MARKING_EVENT_ID}`)
    await expect(page.getByRole('heading', { name: /attendance/i })).toBeVisible()

    // Assert against THIS student's row, not the whole table. The admin view is
    // unfiltered, so it also carries every other real-stack spec's marks — and
    // "marked by search" is the common case among them, so a table-wide
    // `getByRole('cell', { name: 'search' })` matches all of them at once. Row
    // scoping is also the stronger assertion: table-wide checks could be
    // satisfied by three DIFFERENT rows (the roll in one, the teacher in
    // another, the method in a third) and still pass while the row this test
    // actually wrote was wrong.
    const attendanceTable = page.locator('.table-wrap')
    const row = attendanceTable
      .locator('tbody tr')
      .filter({ hasText: new RegExp(roll.replace(/\./g, '\\.'), 'i') })
    await expect(row).toHaveCount(1)
    await expect(row.getByText('Portal Teacher')).toBeVisible()
    await expect(row.getByRole('cell', { name: /^teacher$/i })).toBeVisible()
    await expect(row.getByRole('cell', { name: 'search', exact: true })).toBeVisible()
  })

  test('[P0] a teacher outside the student branch is refused without an attendance row', async ({ page, request }) => {
    const before = await request.get(
      `${portalAPI}/api/v1/admin/events/${MARKING_EVENT_ID}/attendance?slot=7`,
      { headers: { Authorization: 'Bearer portal-admin-token' } },
    )
    expect(before.status()).toBe(200)
    const beforeTotal = (await before.json()).total

    await openTeacherMarkScreen(page)

    await page.getByRole('combobox', { name: /slot/i }).selectOption('7')
    await page.getByRole('searchbox', { name: /student/i }).fill('CB.SC.U4ECE24001')
    await page.getByRole('option', { name: /CB\.SC\.U4ECE24001/i }).click()
    await page.getByRole('button', { name: /mark present/i }).click()

    await expect(page.getByRole('alert')).toContainText(/not allowed|forbidden|branch/i)

    const after = await request.get(
      `${portalAPI}/api/v1/admin/events/${MARKING_EVENT_ID}/attendance?slot=7`,
      { headers: { Authorization: 'Bearer portal-admin-token' } },
    )
    expect((await after.json()).total).toBe(beforeTotal)
  })
})
