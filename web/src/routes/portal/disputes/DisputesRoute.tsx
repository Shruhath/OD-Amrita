import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const DisputesPortal = lazy(() => import('./DisputesPortal'))

export default function DisputesRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<p role="status">Loading disputes…</p>}>
        <DisputesPortal />
      </Suspense>
    </SessionBoundary>
  )
}
