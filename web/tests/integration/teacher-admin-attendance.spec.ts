import { expect, test } from '@playwright/test'
import { installPortalSession } from '../e2e/support/portal-session'
import { teacherMarkRoute } from '../support/factories/teacher-admin-marking'

const redTest = process.env.ATDD_ACTIVE_09 === '1' ? test : test.skip

test.describe('teacher/admin attendance real-stack scaffold', () => {
  redTest('[P0] teacher search-mark writes through the real API and appears in admin attendance', async ({ page }) => {
    await installPortalSession(page, 'teacher')

    await page.goto(teacherMarkRoute)

    await expect(page.getByRole('heading', { name: /mark attendance/i })).toBeVisible()
    await page.getByRole('combobox', { name: /slot/i }).selectOption('4')
    await page.getByRole('searchbox', { name: /student/i }).fill('CB.SC.U4CSE24')
    await page.getByRole('option', { name: /CB\.SC\.U4CSE/i }).first().click()
    await page.getByRole('button', { name: /mark present/i }).click()
    await expect(page.getByRole('status')).toContainText(/marked|present/i)

    await installPortalSession(page, 'admin')
    await page.goto('/portal/events/11111111-1111-4111-8111-111111111111')
    await expect(page.getByRole('heading', { name: /attendance/i })).toBeVisible()
    await expect(page.getByText(/CB\.SC\.U4CSE/i)).toBeVisible()
    await expect(page.getByText(/teacher@amrita\.edu/i)).toBeVisible()
  })

  redTest('[P0] unauthorized teacher branch mismatch is refused without an attendance row', async ({ page }) => {
    await installPortalSession(page, 'teacher')

    await page.goto(teacherMarkRoute)

    await page.getByRole('combobox', { name: /slot/i }).selectOption('4')
    await page.getByRole('searchbox', { name: /student/i }).fill('CB.SC.U4ECE24')
    await page.getByRole('option', { name: /CB\.SC\.U4ECE/i }).first().click()
    await page.getByRole('button', { name: /mark present/i }).click()

    await expect(page.getByRole('alert')).toContainText(/not allowed|forbidden|branch/i)
  })
})
