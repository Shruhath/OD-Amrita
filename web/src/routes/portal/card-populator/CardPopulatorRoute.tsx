import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const CardPopulatorPortal = lazy(() => import('./CardPopulatorPortal'))

export default function CardPopulatorRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading card populator…</p></main>}>
        <CardPopulatorPortal />
      </Suspense>
    </SessionBoundary>
  )
}
