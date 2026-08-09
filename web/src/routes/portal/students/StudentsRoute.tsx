import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { LoadingScreen } from '../../../components/ui/Spinner'

const StudentsPortal = lazyRoute(() => import('./StudentsPortal'))

export default function StudentsRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<LoadingScreen label="Loading student roster" />}>
        <StudentsPortal />
      </Suspense>
    </SessionBoundary>
  )
}
