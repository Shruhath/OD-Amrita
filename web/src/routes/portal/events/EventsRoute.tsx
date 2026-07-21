import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const EventsPortal = lazy(() => import('./EventsPortal'))

export default function EventsRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading events workspace…</p></main>}>
        <EventsPortal />
      </Suspense>
    </SessionBoundary>
  )
}
