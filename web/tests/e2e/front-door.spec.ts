import { expect, test } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json, portalOrigin } from './support/portal-session'
import { AA_CONTRAST, labelContrast } from './support/contrast'
import { publicEmptyDayAttendance, publicSearchResults } from '../support/factories/public-attendance'

/**
 * The front door at `/`.
 *
 * WHY THIS IS A BROWSER TEST AND NOT ONLY A COMPONENT ONE. The friction it fixes
 * is about the ADDRESS BAR: a visitor who is silently sent somewhere has to
 * notice they were moved and navigate back out of it, and neither the noticing
 * nor the navigating exists under a memory router. These assert the real URL
 * before and after each choice.
 *
 * `installPortalSession(page, null)` clears localStorage before the app boots,
 * so every journey here is a genuinely signed-out visitor.
 */

async function installFrontDoor(page: import('@playwright/test').Page) {
  await installPortalSession(page, null)
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    if (request.method() === 'OPTIONS') return fulfilPreflight(route)
    const path = new URL(request.url()).pathname

    if (path.endsWith('/public/od-records')) return json(route, publicEmptyDayAttendance())
    if (path.endsWith('/public/students/search')) return json(route, publicSearchResults())
    // A signed-out visitor has no identity to read. Answering 401 is what the
    // real API does, and it is what makes "the front door does not depend on a
    // session" a real assertion rather than one propped up by a mock.
    return json(route, { code: 'UNAUTHORIZED', message: 'no session' }, 401)
  })
}

test.describe('the front door', () => {
  // FRONT-E2E-001 — the root STAYS the root. Nobody is redirected anywhere.
  //
  // The defect: `/` sent a visitor with no usable role to the public dashboard,
  // so a coordinator opening the app on their phone landed on a student screen
  // and had to edit the URL to get to the marking screen.
  test('[P0] a signed-out visitor stays on / and is offered both ways in', async ({ page }) => {
    await installFrontDoor(page)
    await page.setViewportSize({ width: 390, height: 844 })

    await page.goto(`${portalOrigin}/`)

    await expect(page.getByRole('button', { name: /sign in with microsoft/i })).toBeVisible()
    await expect(page.getByRole('link', { name: /public dashboard/i })).toBeVisible()

    // THE ASSERTION THAT MATTERS: the address bar did not move. `/attendance`
    // here would mean the visitor was sent to a screen they never chose.
    await expect(page).toHaveURL(`${portalOrigin}/`)

    // Give any late redirect a chance to fire before believing the above — a
    // redirect that waits for the session check would otherwise pass this.
    await page.waitForTimeout(600)
    await expect(page).toHaveURL(`${portalOrigin}/`)

    // The one screen every student opens must fit a phone without the page
    // scrolling sideways.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, 'the front door scrolls sideways on a phone').toBeLessThanOrEqual(1)
  })

  // FRONT-E2E-002 — the public dashboard is one click away, and it is a real
  // navigation to a real URL.
  /**
   * THE SIGN-IN BUTTON IS ACTUALLY READABLE, in both themes.
   *
   * This is a contrast assertion rather than a colour one on purpose. The button
   * lost its fill to a one-character CSS mistake — a `:not()` written without
   * `:where()`, which raised an element rule above the class that paints it —
   * and what reached the screen was white text on white in the light theme and
   * black on black in the dark one, on the primary action of the first screen
   * anybody opens. Nothing else in the suite noticed: the button was present,
   * named, focusable and clickable throughout.
   *
   * Naming the expected colour would only pin today's palette. WCAG AA for
   * ordinary text is 4.5:1, and that is the property that was actually broken.
   */
  for (const theme of ['light', 'dark'] as const) {
    test(`[P0] the sign-in button is legible against its own fill (${theme})`, async ({ page }) => {
      await installFrontDoor(page)
      await page.addInitScript((value) => localStorage.setItem('od-amrita-theme', value), theme)
      await page.goto('/')

      const signIn = page.getByRole('button', { name: /sign in with microsoft/i })
      await expect(signIn).toBeVisible()

      const contrast = await labelContrast(signIn)

      expect(
        contrast,
        `the sign-in button's label is unreadable against its own background in the ${theme} theme`,
      ).toBeGreaterThanOrEqual(AA_CONTRAST)
    })
  }

  test('[P0] the public dashboard choice lands on /attendance', async ({ page }) => {
    await installFrontDoor(page)

    await page.goto(`${portalOrigin}/`)
    await page.getByRole('link', { name: /public dashboard/i }).click()

    await expect(page).toHaveURL(`${portalOrigin}/attendance`)
    await expect(page.getByRole('searchbox', { name: /roll number|name|student/i })).toBeVisible()
  })

  // FRONT-E2E-003 — the back button works, which is the whole reason the choice
  // is a link rather than a button that navigates.
  //
  // A visitor who takes the wrong door must be able to get back to the other
  // one. That is the friction in its smallest form.
  test('[P0] a visitor can go back from the dashboard to the front door', async ({ page }) => {
    await installFrontDoor(page)

    await page.goto(`${portalOrigin}/`)
    await page.getByRole('link', { name: /public dashboard/i }).click()
    await expect(page).toHaveURL(`${portalOrigin}/attendance`)

    await page.goBack()

    await expect(page).toHaveURL(`${portalOrigin}/`)
    await expect(page.getByRole('button', { name: /sign in with microsoft/i })).toBeVisible()
  })

  // FRONT-E2E-004 — SIGNING OUT COMES BACK HERE.
  //
  // The defect: signing out cleared the identity but not the address bar, so a
  // coordinator stayed on `/mark` and was handed a card offering the one thing
  // they had just declined — sign in again — with no route to the public board
  // short of typing a URL. This is an address-bar defect, so it is asserted in a
  // browser: the URL after signing out is the claim.
  test('[P0] signing out returns a coordinator to the front door, not to a sign-in wall', async ({ page }) => {
    await installPortalSession(page, 'coordinator')
    await page.route('**/api/v1/**', async (route) => {
      const request = route.request()
      if (request.method() === 'OPTIONS') return fulfilPreflight(route)
      const path = new URL(request.url()).pathname

      if (path.endsWith('/auth/me')) {
        return json(route, {
          user_id: '99999999-9999-4999-8999-999999999999',
          email: 'coordinator@amrita.edu',
          name: 'E2E coordinator',
          role: 'coordinator',
        })
      }
      if (path.endsWith('/public/od-records')) return json(route, publicEmptyDayAttendance())
      return json(route, { data: [], total: 0, page: 1, page_size: 20 })
    })

    await page.goto(`${portalOrigin}/mark`)
    await page.getByRole('button', { name: /account:/i }).click()
    await page.getByRole('menuitem', { name: /sign out/i }).click()

    await expect(page).toHaveURL(`${portalOrigin}/`)
    await expect(page.getByRole('button', { name: /sign in with microsoft/i })).toBeVisible()
    await expect(page.getByRole('link', { name: /public dashboard/i })).toBeVisible()

    // `replace`, not a push: Back must not walk into the marking route and get
    // bounced straight out of it again, which reads as the button not working.
    await page.goBack()
    await expect(page).not.toHaveURL(`${portalOrigin}/mark`)
  })
})
