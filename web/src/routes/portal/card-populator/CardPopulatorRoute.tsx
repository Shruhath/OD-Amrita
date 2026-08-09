import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { LoadingScreen } from '../../../components/ui/Spinner'

const CardPopulatorPortal = lazyRoute(() => import('./CardPopulatorPortal'))

export default function CardPopulatorRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<LoadingScreen label="Loading card populator" />}>
        <CardPopulatorPortal />
      </Suspense>
    </SessionBoundary>
  )
}
