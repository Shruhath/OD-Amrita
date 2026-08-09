import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { LoadingScreen } from '../../../components/ui/Spinner'

const TeachersPortal = lazyRoute(() => import('./TeachersPortal'))

export default function TeachersRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<LoadingScreen label="Loading teachers workspace" />}>
        <TeachersPortal />
      </Suspense>
    </SessionBoundary>
  )
}
