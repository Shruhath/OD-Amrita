import { expect, test } from '@playwright/test'

const eventName = 'Browser Persisted Event'

test('persists create and archive through the real API and Postgres across browser reloads', async ({ page }) => {
  await page.goto('/integration.html')
  await expect(page.getByRole('heading', { name: 'Events' })).toBeVisible()

  await page.getByRole('button', { name: 'New event' }).click()
  const createDialog = page.getByRole('dialog', { name: 'New event' })
  await createDialog.getByLabel('Event name').fill(eventName)
  await createDialog.getByLabel('Event date').fill('2026-11-05')
  await createDialog.getByRole('checkbox', { name: 'Computer Science and Engineering' }).check()
  await createDialog.getByRole('checkbox', { name: 'Electronics and Communication Engineering' }).check()
  await createDialog.getByRole('button', { name: 'Create event' }).click()

  const created = page.getByRole('row').filter({ hasText: eventName })
  await expect(created).toContainText('Active')
  await expect(created).toContainText('CSE')
  await expect(created).toContainText('ECE')

  await page.reload()
  const persisted = page.getByRole('row').filter({ hasText: eventName })
  await expect(persisted).toContainText('Active')
  await persisted.getByRole('button', { name: 'Archive' }).click()
  await page.getByRole('dialog', { name: `Archive ${eventName}` }).getByRole('button', { name: 'Archive event' }).click()
  await expect(persisted).toContainText('Archived')

  await page.reload()
  const archived = page.getByRole('row').filter({ hasText: eventName })
  await expect(archived).toContainText('Archived')
  await expect(archived.getByRole('button', { name: 'Archive' })).toHaveCount(0)
})

test('denies a real Student principal in both the API and portal', async ({ page, request }) => {
  const response = await request.get('http://127.0.0.1:8081/api/v1/admin/events', {
    headers: { Authorization: 'Bearer portal-student-token' },
  })
  expect(response.status()).toBe(403)
  await expect(response.json()).resolves.toMatchObject({ code: 'RBAC_FORBIDDEN' })

  const adminRequests: string[] = []
  page.on('request', (candidate) => {
    if (new URL(candidate.url()).pathname.startsWith('/api/v1/admin/')) adminRequests.push(candidate.url())
  })
  await page.goto('/integration.html?role=student')
  await expect(page.getByRole('heading', { name: 'Access denied' })).toBeVisible()
  expect(adminRequests).toEqual([])
})
