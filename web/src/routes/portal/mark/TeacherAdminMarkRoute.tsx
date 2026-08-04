import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const TeacherAdminMarkScreen = lazy(() => import('./TeacherAdminMarkScreen'))

export default function TeacherAdminMarkRoute() {
  return (
    <SessionBoundary allowedRoles={['teacher', 'admin']}>
      <Suspense fallback={<main className="center-state"><p role="status">Loading marking screen…</p></main>}>
        <TeacherAdminMarkScreen />
      </Suspense>
    </SessionBoundary>
  )
}
