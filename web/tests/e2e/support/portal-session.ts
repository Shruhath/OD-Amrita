import type { Page, Route } from '@playwright/test'

// Shared browser-journey scaffolding: a seeded Supabase session and CORS-correct
// route fulfillment. Both are required for ANY mocked portal journey — the app
// renders the sign-in screen without a persisted session, and the dev server
// (127.0.0.1:4173) calls the API on a different origin, so a fulfilled response
// without the allow headers is rejected by the browser before the app sees it.

export const portalOrigin = 'http://127.0.0.1:4173'

const sessionUserID = '99999999-9999-4999-8999-999999999999'

export type PortalRole = 'admin' | 'teacher' | 'coordinator' | 'student'

/** Fulfills a route with JSON the browser will actually accept cross-origin. */
export function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    status,
    contentType: 'application/json',
    headers: {
      'Access-Control-Allow-Origin': portalOrigin,
      'Access-Control-Allow-Headers': 'Authorization, Content-Type',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    },
    body: JSON.stringify(body),
  })
}

/** Answers the CORS preflight the browser sends before a cross-origin POST. */
export async function fulfilPreflight(route: Route) {
  await route.fulfill({
    status: 204,
    headers: {
      'Access-Control-Allow-Origin': portalOrigin,
      'Access-Control-Allow-Headers': 'Authorization, Content-Type',
      'Access-Control-Allow-Methods': 'GET, POST, OPTIONS',
    },
    body: '',
  })
}

/**
 * Seeds a persisted Supabase session before the app boots, so the portal renders
 * as the given role instead of the sign-in screen. Pass null for a signed-out
 * journey.
 */
export async function installPortalSession(page: Page, role: PortalRole | null) {
  await page.addInitScript(({ sessionRole, userID }) => {
    localStorage.clear()
    if (sessionRole === null) return
    localStorage.setItem(
      'sb-test-auth-token',
      JSON.stringify({
        access_token: 'e2e-access-token',
        refresh_token: 'e2e-refresh-token',
        token_type: 'bearer',
        expires_in: 3600,
        expires_at: 4102444800,
        user: {
          id: userID,
          email: `${sessionRole}@amrita.edu`,
          role: 'authenticated',
          app_metadata: { provider: 'azure', providers: ['azure'] },
          user_metadata: { name: `E2E ${sessionRole}` },
        },
      }),
    )
  }, { sessionRole: role, userID: sessionUserID })

  await page.route('**/auth/v1/**', async (route) => {
    if (role === null) {
      await json(route, { message: 'No session' }, 401)
      return
    }
    await json(route, {
      id: sessionUserID,
      email: `${role}@amrita.edu`,
      role: 'authenticated',
      user_metadata: { name: `E2E ${role}` },
    })
  })
}
