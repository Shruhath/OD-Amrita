import { Link } from 'react-router-dom'
import { AuthBrand } from '../features/auth/AuthGate'
import { useAuth } from '../features/auth/context'

/**
 * The front door.
 *
 * WHAT IT REPLACED. `/` used to hand a signed-out visitor the sign-in card and
 * nothing else, so the app presented itself as staff-only — and the one screen
 * built for students, the public board, was reachable only by knowing to type
 * `/attendance`. Meanwhile the roles that DO sign in were each computed a
 * destination they never chose, so anyone whose role had not been assigned yet
 * landed on the public dashboard and had to edit the URL to get anywhere else.
 *
 * Both of those are the same defect: the app deciding where somebody wanted to
 * go before it had any way of knowing. This page asks instead. It offers the
 * two things a visitor can actually be here for, and each is a real choice:
 *
 *   - Sign in, and THEN be sent to the screen the role admits (RoleLanding
 *     still owns that map — signing in is what makes the answer knowable).
 *   - Open the public board, which needs no account at all.
 *
 * NOTHING AUTO-REDIRECTS FROM HERE. A visitor with no session stays on this
 * page until they pick, which is the whole point: a coordinator opening the app
 * on their phone must not have to notice they were sent somewhere and navigate
 * back out of it.
 */
export default function HomeLanding() {
  const auth = useAuth()

  return (
    <main className="center-state home-landing">
      {/*
        The theme control comes from AuthBrand and ONLY from there. This page
        used to pin one of its own in the top-right corner as well, and the two
        sat on top of each other — the same button drawn twice, half-overlapping,
        on the first screen anybody sees. AuthBrand carries it for every
        signed-out surface (this page, the session-error card, access denied), so
        a second one here is not a fallback, it is a duplicate.
      */}
      <AuthBrand />
      <p className="eyebrow">On-duty attendance</p>
      <h1>OD-Amrita</h1>
      <p className="home-lede">
        On-duty attendance for events. Staff sign in to mark and manage records; students can check
        the day's list without an account.
      </p>

      <div className="home-actions">
        <button className="primary" type="button" onClick={() => void auth.signIn()}>
          Sign in with Microsoft
        </button>
        {/*
          A real <Link>, not a button that navigates. It is a destination, so it
          must be openable in a new tab, shareable, and reachable by the browser
          controls a student actually uses — none of which a click handler gives.
        */}
        <Link className="home-secondary" to="/attendance">
          Open the public dashboard
        </Link>
      </div>

      <p className="auth-hint">
        Staff: use your university address — for example
        {' '}<code>cb.sc.u4cse24124@cb.students.amrita.edu</code>.
      </p>

      {/*
        A failed session check is shown HERE rather than swallowed. Without it a
        visitor whose token was rejected sees a working-looking front page,
        presses Sign in, and is bounced back to it with no explanation.
      */}
      {auth.error && (
        <p className="public-alert" role="alert">{auth.error}</p>
      )}
    </main>
  )
}
