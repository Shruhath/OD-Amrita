import { lazyRoute } from '../lazyRoute'
import { Suspense } from 'react'
import { LoadingScreen } from '../../components/ui/Spinner'
import { SessionBoundary } from '../../features/auth/AuthGate'

const MarkScreen = lazyRoute(() => import('./MarkScreen'))

/**
 * The `/mark` route.
 *
 * COORDINATOR ONLY. Teacher and admin marking is a later story: it needs the
 * explicit slot picker that the mark service requires for those roles and that
 * this screen deliberately does not have, so admitting an admin here would put
 * them on a screen whose every mark the server then rejects.
 *
 * The boundary runs BEFORE the screen loads, so a non-coordinator issues no
 * scoped read and no status poll at all — not merely a hidden capture surface.
 */
export default function MarkRoute() {
  return (
    <SessionBoundary allowedRoles={['coordinator']}>
      <Suspense fallback={<LoadingScreen label="Loading marking screen" />}>
        <MarkScreen />
      </Suspense>
    </SessionBoundary>
  )
}
