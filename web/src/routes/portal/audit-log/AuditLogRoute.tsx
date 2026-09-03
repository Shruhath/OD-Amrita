import { Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'
import { rolesFor } from '../../access'
import { lazyRoute } from '../../lazyRoute'

const AuditLogPortal = lazyRoute(() => import('./AuditLogPortal'))

export default function AuditLogRoute() {
  return (
    <SessionBoundary allowedRoles={rolesFor('/portal/audit-log')}>
      <Suspense fallback={<LoadingScreen label="Loading audit log" />}>
        <AuditLogPortal />
      </Suspense>
    </SessionBoundary>
  )
}
