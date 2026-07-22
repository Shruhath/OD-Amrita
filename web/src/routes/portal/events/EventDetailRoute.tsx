import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const EventDetailPortal = lazy(() => import('./EventDetailPortal'))

export default function EventDetailRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading event…</p></main>}>
        <EventDetailPortal />
      </Suspense>
    </SessionBoundary>
  )
}
