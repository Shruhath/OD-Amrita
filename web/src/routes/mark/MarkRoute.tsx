import { rolesFor } from '../access'
import { lazyRoute } from '../lazyRoute'
import { Suspense } from 'react'
import { PortalShell } from '../../components/PortalShell'
import { LoadingScreen } from '../../components/ui/Spinner'
import { SessionBoundary } from '../../features/auth/AuthGate'
import { useAuth } from '../../features/auth/context'

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
      <MarkRouteContent />
    </SessionBoundary>
  )
}

function MarkRouteContent() {
  const { identity } = useAuth()
  const screen = (
    <Suspense fallback={<LoadingScreen label="Loading marking screen" />}>
      <MarkScreen embeddedInPortal={identity?.role === 'admin'} />
    </Suspense>
  )

  return identity?.role === 'admin'
    ? <PortalShell>{screen}</PortalShell>
    : screen
}
