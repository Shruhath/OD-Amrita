import { lazy, Suspense } from 'react'

const PublicAttendanceScreen = lazy(() => import('./PublicAttendanceScreen'))

/**
 * The one route in this app with NO SessionBoundary.
 *
 * That omission is the whole point and is deliberate rather than forgotten:
 * SessionBoundary defaults `allowedRoles` to `['admin']`, so wrapping this route
 * "for consistency" would render "Access denied" to every student the page
 * exists for. The screen calls only the two public endpoints through
 * createPublicApi(), which has no SessionPort, so there is no session for a
 * boundary to check.
 *
 * Lazy-loaded so the dashboard's own code is a separate chunk (AR15). Splitting
 * the privileged portal out of the public entry point as well needs every route
 * in router.tsx lazy-loaded — a wider refactor that is not part of this story.
 */
export default function PublicAttendanceRoute() {
  return (
    <Suspense fallback={<main className="center-state"><p role="status">Loading attendance…</p></main>}>
      <PublicAttendanceScreen />
    </Suspense>
  )
}
