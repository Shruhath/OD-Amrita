import { expect, type Locator, type Page } from '@playwright/test'

/**
 * Drives the application's dropdown from a browser test.
 *
 * The replacement for Playwright's `selectOption`, which only speaks to a native
 * `<select>`. The app draws its own list, because the native one is rendered by
 * the operating system and cannot be styled or themed at all.
 *
 * The list does not exist in the page until the control is opened, so these
 * helpers open it — which is also what a real visitor does, making the test a
 * closer account of the interaction than `selectOption` ever was.
 */

/** Picks by option value — the id the screen acts on. */
export async function selectValue(page: Page, combobox: Locator, value: string) {
  await combobox.click()
  const option = page.getByRole('listbox').locator(`[data-value="${value}"]`)
  await expect(option).toBeVisible()
  await option.click()
  await expect(page.getByRole('listbox')).toBeHidden()
}

/**
 * Picks by the wording a visitor reads.
 *
 * A STRING matches exactly, which is what `selectOption({ label })` did and what
 * these call sites assume: Playwright's `name` is a substring match by default,
 * so picking "CSE" would be ambiguous the day a "CSE-AI" branch is added, and
 * would fail as an unhelpful strict-mode violation rather than as a wrong
 * choice. Pass a regular expression where a partial match is genuinely wanted.
 */
export async function selectLabel(page: Page, combobox: Locator, label: string | RegExp) {
  await combobox.click()
  const option = page
    .getByRole('listbox')
    .getByRole('option', typeof label === 'string' ? { name: label, exact: true } : { name: label })
  await expect(option).toBeVisible()
  await option.click()
  await expect(page.getByRole('listbox')).toBeHidden()
}
