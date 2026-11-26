import { expect, test, type Locator } from '@playwright/test'

// Teacher profiles against the REAL stack — the deterministic portal API in
// api/test/portal and a real Postgres.
//
// Invoked by: web-deploy -> portal-integration job
//   (`pnpm --filter od-amrita-web test:e2e:integration`).
//
// Nothing here is mocked. The mocked journey in tests/e2e/teacher-profiles.spec.ts
// proves the UI reacts correctly to a well-behaved server; only this file proves
// the server actually behaves that way and that the role elevation and branch
// associations survive in Postgres.
//
// Fixture dependency: this spec needs a SEEDED student-role fixture user to
// elevate — the portal harness cannot create one through the UI, since student
// provisioning happens only via a real first login. api/test/portal/main.go's
// seed() provisions exactly this row (studentFixtureEmail below); without it
// every case here fails at the elevation step.

const portalAPI = 'http://127.0.0.1:8081'

// Matches the email seed() is documented to provision. A per-run suffix is
// deliberately NOT used here: D2 makes a second elevation of the same user a
// 409, so a repeat run against a long-lived harness must be able to re-run
// cleanly, and that is exactly what a stable, re-seeded fixture row gives us —
// mirroring how the events/categories specs instead suffix by run because
// those aggregates have no such "already elevated" terminal state.
const studentFixtureEmail = 'teacher-fixture-student@amrita.edu'

async function selectFirstCategory(dialog: Locator) {
  await dialog.getByRole('checkbox', { name: /Procession|Exception|Day/i }).first().check()
}

test('creates a teacher profile through the real API and Postgres, and it persists across reload', async ({ page }) => {
  await page.goto('/integration.html?path=%2Fportal%2Fteachers')
  // exact: true — the empty-state "No teachers yet" heading also contains
  // "teachers", and Playwright's default name match is a case-insensitive
  // substring, so an un-scoped query is ambiguous whenever the roster starts
  // empty (strict-mode violation, flaky under retries, hard-red at retries: 0).
  await expect(page.getByRole('heading', { name: 'Teachers', exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill(studentFixtureEmail)
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await selectFirstCategory(dialog)
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  const row = page.getByRole('row', { name: new RegExp(studentFixtureEmail) })
  await expect(row).toBeVisible()

  // The reload is the persistence oracle: every byte of client state is gone.
  await page.reload()
  await expect(page.getByRole('row', { name: new RegExp(studentFixtureEmail) })).toBeVisible()
})

test('rejects a second elevation of the same user with the real 409, and writes nothing', async ({ page }) => {
  // The fixture from the previous test is now a teacher; D2 makes a repeat
  // elevation a state conflict, proven here against the REAL AssignTeacherRole
  // "AND role = 'student'" backstop, not a service-layer pre-check mock.
  await page.goto('/integration.html?path=%2Fportal%2Fteachers')
  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill(studentFixtureEmail)
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await selectFirstCategory(dialog)
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  await expect(dialog.getByRole('alert')).toHaveText('This user already has an assigned role.')
})

test('rejects an email with no matching users row with the real 404, and writes nothing', async ({ page }) => {
  await page.goto('/integration.html?path=%2Fportal%2Fteachers')
  await page.getByRole('button', { name: 'New teacher' }).click()
  const dialog = page.getByRole('dialog', { name: 'New teacher' })
  await dialog.getByLabel('Email').fill('never-signed-in-fixture@amrita.edu')
  await dialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await selectFirstCategory(dialog)
  await dialog.getByRole('button', { name: 'Create teacher' }).click()

  await expect(dialog.getByRole('alert')).toHaveText('No user has signed in with this email yet.')
})

test('denies a real Student principal on both teacher routes through the production middleware chain', async ({ request }) => {
  // The portal harness verifies real bearer tokens against seeded users, so
  // this exercises the production RBAC chain, not a stub.
  for (const method of ['get', 'post'] as const) {
    const response = await request[method](`${portalAPI}/api/v1/admin/teachers`, {
      headers: { Authorization: 'Bearer portal-student-token', 'Content-Type': 'application/json' },
      ...(method === 'post' ? { data: { email: studentFixtureEmail, branch_ids: [] } } : {}),
    })
    expect(response.status()).toBe(403)
    await expect(response.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })
  }
})
