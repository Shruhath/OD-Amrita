import { Navigate } from 'react-router-dom'
import { useAuth } from '../features/auth/context'
import { hasPendingAuthCallback } from '../features/auth/callback'
import { SessionLoading } from '../features/auth/AuthGate'
import HomeLanding from './HomeLanding'

/**
 * Where each role is sent when it arrives with no particular destination.
 *
 * This is the ONLY place the mapping exists. It has to be, because the sign-in
 * redirect cannot know a role: the browser leaves for Microsoft before the app
 * has ever called `/auth/me`, so whatever URL it names is necessarily the same
 * one for everybody. Naming a portal screen there meant every coordinator
 * finished signing in and was shown "Access denied" by an admin-gated route.
 * The redirect now names the root, and the answer is computed here instead.
 *
 * Each destination is the screen whose OWN boundary admits that role — see
 * MarkRoute (coordinator), TeacherAdminMarkRoute (teacher, admin) and
 * EventsRoute (admin). If a landing here and the boundary there ever disagree,
 * the result is a redirect straight into a refusal, which is the defect this map
 * exists to prevent; RoleLanding.test.tsx holds the two together.
 */
const LANDING_BY_ROLE: Record<string, string> = {
  admin: '/portal/events',
  teacher: '/portal/od-records',
  coordinator: '/mark',
  // A student has no authenticated surface of their own. The public dashboard is
  // the one screen that answers their actual question — "was I marked?" — and it
  // is read-only, so landing there discloses nothing their own roll number would
  // not. The alternative is the admin events list refusing them, which tells a
  // student the app is broken rather than that it is not for them.
  student: '/attendance',
}

/**
 * The catch-all route: sends each signed-in role to the screen it can use.
 *
 * Reached at `/` after sign-in, and by any unmatched path.
 */
export default function RoleLanding() {
  const auth = useAuth()

  // Still resolving. The second half matters as much as the first: immediately
  // after the provider redirects back, the session genuinely does not exist yet
  // (the code has not been exchanged), so `identity` is null for a real reason
  // that is NOT "signed out". Without the guard the visitor is shown the sign-in
  // card one beat after signing in.
  if (auth.loading || (!auth.identity && hasPendingAuthCallback())) {
    return <SessionLoading />
  }

  if (auth.identity) {
    // An unrecognised role is a real possibility — roles are assigned in the
    // database, not by this bundle — so it falls back to the public dashboard
    // rather than to an admin screen that would refuse it.
    return <Navigate to={LANDING_BY_ROLE[auth.identity.role] ?? '/attendance'} replace />
  }

  // Signed out: the front door, and NO redirect.
  //
  // Rendered here rather than navigated to, for the same reason the sign-in card
  // used to be: bouncing a signed-out visitor onto a portal url puts an admin
  // address in their history and their address bar, which then looks like where
  // they are supposed to be.
  //
  // It is a landing page rather than a bare sign-in card because the two things
  // a visitor can be here for are not the same thing, and the app cannot tell
  // which one they are until they say. Sending them anywhere by default gets it
  // wrong for everybody else — which is what sending them to the public board
  // did to every coordinator opening the app on their phone.
  return <HomeLanding />
}
