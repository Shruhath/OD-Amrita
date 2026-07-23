import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const TeachersPortal = lazy(() => import('./TeachersPortal'))

export default function TeachersRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading teachers workspace…</p></main>}>
        <TeachersPortal />
      </Suspense>
    </SessionBoundary>
  )
}
