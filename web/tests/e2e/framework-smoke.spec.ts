import { expect, test } from '@playwright/test'

test('serves the current web application shell', async ({ page }) => {
  await page.goto('/')

  await expect(page.getByRole('heading', { name: 'OD-Amrita' })).toBeVisible()
})
