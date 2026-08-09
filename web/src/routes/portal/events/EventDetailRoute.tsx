import { lazy, Suspense } from 'react'
import { useParams } from 'react-router-dom'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { LoadingScreen } from '../../../components/ui/Spinner'

const EventDetailPortal = lazy(() => import('./EventDetailPortal'))

export default function EventDetailRoute() {
  const { event_id: eventID = '' } = useParams()

  return (
    <SessionBoundary>
      <Suspense fallback={<LoadingScreen label="Loading event" />}>
        {/* Keyed by the event id so navigating from one event to another
            REMOUNTS the portal. React Router reuses a component instance when
            only the param changes, which would carry the previous event's
            half-typed category name — and its "already exists" rejection —
            onto an event where that name may be perfectly free. */}
        <EventDetailPortal key={eventID} />
      </Suspense>
    </SessionBoundary>
  )
}
