import { rolesFor } from '../../access'
import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const EventsPortal = lazyRoute(() => import('./EventsPortal'))

export default function EventsRoute() {
  return (
    <SessionBoundary allowedRoles={rolesFor('/portal/events')}>
      <Suspense fallback={<LoadingScreen label="Loading events workspace" />}>
        <EventsPortal />
      </Suspense>
    </SessionBoundary>
  )
}
