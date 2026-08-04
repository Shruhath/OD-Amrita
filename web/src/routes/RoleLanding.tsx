import { Navigate } from 'react-router-dom'
import { useAuth } from '../features/auth/context'

/**
 * Sends each signed-in role to the screen it can actually use.
 *
 * A coordinator can open none of the `/portal/*` screens — they are admin-gated
 * — so landing them on the events list would show "Access denied" to someone
 * whose only job is to mark attendance. Admin routing is unchanged, and anyone
 * not signed in still reaches the portal's own sign-in surface.
 */
export default function RoleLanding() {
  const auth = useAuth()
  if (auth.loading) {
    return <main className="center-state"><p role="status">Checking your session…</p></main>
  }
  if (auth.identity?.role === 'coordinator') return <Navigate to="/mark" replace />
  if (auth.identity?.role === 'teacher') return <Navigate to="/portal/mark" replace />
  return <Navigate to="/portal/events" replace />
}
