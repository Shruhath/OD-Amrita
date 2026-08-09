import { lazyRoute } from '../../lazyRoute'
import { Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const TeacherAdminMarkScreen = lazyRoute(() => import('./TeacherAdminMarkScreen'))

export default function TeacherAdminMarkRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<LoadingScreen label="Loading marking screen" />}>
        <TeacherAdminMarkScreen />
      </Suspense>
    </SessionBoundary>
  )
}
