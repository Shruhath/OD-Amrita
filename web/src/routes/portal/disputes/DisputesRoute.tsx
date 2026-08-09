import { lazy, Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const DisputesPortal = lazy(() => import('./DisputesPortal'))

export default function DisputesRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<LoadingScreen label="Loading disputes" />}>
        <DisputesPortal />
      </Suspense>
    </SessionBoundary>
  )
}
