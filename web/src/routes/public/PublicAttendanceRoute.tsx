import { lazyRoute } from '../lazyRoute'
import { Component, Suspense, type ReactNode } from 'react'
import { LoadingScreen } from '../../components/ui/Spinner'

const PublicAttendanceScreen = lazyRoute(() => import('./PublicAttendanceScreen'))

/**
 * Catches a failed chunk load.
 *
 * `lazy()` throws its rejection to the nearest boundary, and there is none above
 * this route — so a phone dropping off wifi mid-load, or a redeploy invalidating
 * the hashed chunk in an already-open tab, unmounts the tree to a blank white
 * page. On every other screen that is recoverable by signing in again; here
 * there is no sign-in and no other affordance, so a blank page is the end of the
 * road for a student who just wants to know whether they were marked.
 */
class ChunkBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <main className="center-state">
        <div className="public-alert" role="alert">
          <p>This page could not finish loading. Check your connection and reload.</p>
          <div className="public-alert-actions">
            <button type="button" onClick={() => window.location.reload()}>Reload</button>
          </div>
        </div>
      </main>
    )
  }
}

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
    <ChunkBoundary>
      <Suspense fallback={<LoadingScreen label="Loading attendance" />}>
        <PublicAttendanceScreen />
      </Suspense>
    </ChunkBoundary>
  )
}
