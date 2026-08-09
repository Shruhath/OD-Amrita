import { lazy, Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const EventsPortal = lazy(() => import('./EventsPortal'))

export default function EventsRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<LoadingScreen label="Loading events workspace" />}>
        <EventsPortal />
      </Suspense>
    </SessionBoundary>
  )
}
