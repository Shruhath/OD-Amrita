import { screen, waitFor, within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

/**
 * Picks an option from the application's dropdown.
 *
 * The replacement for `userEvent.selectOptions`, which only drives a native
 * `<select>`. Every call site passed a VALUE rather than a label, so this does
 * too — a label is a display string that can be reworded or repeat between
 * options, while the value is the id the screen actually acts on.
 *
 * It opens the control first, which is not ceremony: the list does not exist in
 * the DOM until it is opened, so a test that reached straight for an option
 * would be asserting against markup the user can never see either.
 */
export async function selectByValue(user: UserEvent, combobox: HTMLElement, value: string) {
  await user.click(combobox)
  // Waits rather than reading once, because these lists are usually fetched:
  // the control opens with nothing in it and fills when the query resolves. The
  // listbox is re-queried on each attempt so a re-render cannot leave this
  // holding a detached node.
  const option = await waitFor(() => {
    const listbox = screen.getByRole('listbox')
    const found = listbox.querySelector<HTMLElement>(`[data-value="${CSS.escape(value)}"]`)
    if (!found) {
      const offered = Array.from(listbox.querySelectorAll<HTMLElement>('[data-value]'))
        .map((node) => `${node.dataset.value} (${node.textContent})`)
        .join(', ')
      throw new Error(`no option with value ${JSON.stringify(value)}; the control offered: ${offered || 'nothing'}`)
    }
    return found
  })
  await user.click(option)
}

/** Picks by visible label, for the cases where the wording is the thing under test. */
export async function selectByLabel(user: UserEvent, combobox: HTMLElement, label: string | RegExp) {
  await user.click(combobox)
  const listbox = await screen.findByRole('listbox')
  await user.click(within(listbox).getByRole('option', { name: label }))
}

/**
 * Opens a dropdown and hands back its listbox, for tests that assert on WHICH
 * options are offered.
 *
 * The options do not exist in the DOM until the control is opened — which is
 * closer to the truth than the native control was, where a test could inspect
 * options no user had asked to see. Only one list is open at a time, so a test
 * comparing two controls must open them one after the other.
 */
export async function openOptions(user: UserEvent, combobox: HTMLElement): Promise<HTMLElement> {
  await user.click(combobox)
  return screen.findByRole('listbox')
}

/** The values a dropdown is currently offering, in order. */
export async function openedOptionValues(user: UserEvent, combobox: HTMLElement): Promise<string[]> {
  await user.click(combobox)
  const listbox = await screen.findByRole('listbox')
  return Array.from(listbox.querySelectorAll<HTMLElement>('[data-value]')).map((node) => node.dataset.value ?? '')
}
