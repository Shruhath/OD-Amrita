import { Suspense } from 'react'
import { lazyRoute } from '../../lazyRoute'
import { rolesFor } from '../../access'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { LoadingScreen } from '../../../components/ui/Spinner'

const LivePortal = lazyRoute(() => import('./LivePortal'))

export default function LiveRoute() {
  return (
    <SessionBoundary allowedRoles={rolesFor('/portal/live')}>
      <Suspense fallback={<LoadingScreen label="Loading live attendance" />}>
        <LivePortal />
      </Suspense>
    </SessionBoundary>
  )
}
