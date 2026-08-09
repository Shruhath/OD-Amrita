import { rolesFor } from '../access'
import { lazyRoute } from '../lazyRoute'
import { Suspense } from 'react'
import { LoadingScreen } from '../../components/ui/Spinner'
import { SessionBoundary } from '../../features/auth/AuthGate'

const MarkScreen = lazyRoute(() => import('./MarkScreen'))

/**
 * The `/mark` route.
 *
 * Split out from the router so the screen itself is a lazy chunk. It is the
 * heaviest surface in the bundle and the one a student on the public dashboard
 * never opens, so importing it eagerly made every visitor download the marking
 * code to read a list of names.
 */
export default function MarkRoute() {
  return (
    <SessionBoundary allowedRoles={rolesFor('/mark')}>
      <Suspense fallback={<LoadingScreen label="Loading marking screen" />}>
        <MarkScreen />
      </Suspense>
    </SessionBoundary>
  )
}
