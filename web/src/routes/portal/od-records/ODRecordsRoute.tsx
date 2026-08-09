import { Suspense } from 'react'
import { lazyRoute } from '../../lazyRoute'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { rolesFor } from '../../access'

const ODRecordsPortal = lazyRoute(() => import('./ODRecordsPortal'))

export default function ODRecordsRoute() {
  return <SessionBoundary allowedRoles={rolesFor('/portal/od-records')}><Suspense fallback={<LoadingScreen label="Loading records" />}><ODRecordsPortal /></Suspense></SessionBoundary>
}
