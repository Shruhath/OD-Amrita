import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const DisputesPortal = lazyRoute(() => import('./DisputesPortal'))

export default function DisputesRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<LoadingScreen label="Loading disputes" />}>
        <DisputesPortal />
      </Suspense>
    </SessionBoundary>
  )
}
