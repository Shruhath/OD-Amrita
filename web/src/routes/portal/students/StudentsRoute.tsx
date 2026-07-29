import { lazy, Suspense } from 'react'
import { SessionBoundary } from '../../../features/auth/AuthGate'

const StudentsPortal = lazy(() => import('./StudentsPortal'))

export default function StudentsRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading student roster…</p></main>}>
        <StudentsPortal />
      </Suspense>
    </SessionBoundary>
  )
}
