import { lazy, Suspense } from 'react'
import { LoadingScreen } from '../../../components/ui/Spinner'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const TeacherAdminMarkScreen = lazy(() => import('./TeacherAdminMarkScreen'))

export default function TeacherAdminMarkRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<LoadingScreen label="Loading marking screen" />}>
        <TeacherAdminMarkScreen />
      </Suspense>
    </SessionBoundary>
  )
}
