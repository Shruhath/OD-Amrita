import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Navigate } from 'react-router-dom'
import { motion } from 'motion/react'
import type { Identity } from '../../api/client'
import { ThemeToggle } from '../../components/ThemeToggle'
import { ApiFailure, createEventApi } from '../../api/client'
import type { SessionPort } from './session'
import { hasPendingAuthCallback } from './callback'
import { clearPersistedState } from '../persistence/sessionState'
import { LoadingScreen } from '../../components/ui/Spinner'
import { AuthContext, useAuth, type AuthState } from './context'

export function AuthProvider({
  children,
  session,
  onSignOut,
}: {
  children: ReactNode
  session: SessionPort
  /**
   * Run after the session is torn down, before the identity is dropped.
   *
   * Passed in rather than reached for with useQueryClient() so this component
   * keeps working where it is mounted without a QueryClientProvider — which is
   * how several of its own tests mount it, and which a hook would turn into a
   * throw. Application.tsx supplies the real one.
   */
  onSignOut?: () => void
}) {
  const api = useMemo(() => createEventApi(session), [session])
  const [identity, setIdentity] = useState<Identity | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  /**
   * Resolves the signed-in identity.
   *
   * `background` is the difference between the FIRST resolution and a
   * REVALIDATION, and it matters far more than it looks. Supabase fires an auth
   * state change whenever it refreshes a token — including when a phone returns
   * to the browser after the app was in the background. Every one of those used
   * to set `loading` true, which made SessionBoundary swap the screen for the
   * loading state, which UNMOUNTS the entire route beneath it. The screen was
   * then rebuilt from scratch: a half-filled form, a loaded roster, a chosen
   * event — all gone, because switching apps for a moment was indistinguishable
   * from signing in for the first time.
   *
   * A background revalidation therefore keeps the current identity on screen and
   * swaps it only if the answer actually changed.
   */
  const load = useCallback(async ({ background = false } = {}) => {
    if (!background) {
      setLoading(true)
      setError(null)
    }
    try {
      const token = await session.accessToken()
      if (!token) {
        setIdentity(null)
        return
      }
      setIdentity(await api.identity())
      setError(null)
    } catch (cause) {
      const unauthorized = cause instanceof ApiFailure && cause.status === 401
      // A REVALIDATION THAT FAILED IS NOT A SIGN-OUT. A phone moving between
      // wifi and mobile data drops requests routinely, and tearing the screen
      // down for that would lose the operator's work over a blip they never saw.
      // Only a 401 — the server actively refusing the credential — clears it;
      // anything else leaves the last known identity in place and stays quiet,
      // and the next revalidation or user action surfaces the problem honestly.
      if (background && !unauthorized) return

      setIdentity(null)
      // The API client has already attempted one refresh and cleared an invalid
      // session on 401. Return to the sign-in state instead of presenting a
      // retry loop for credentials that no longer exist.
      setError(unauthorized
        ? null
        : cause instanceof ApiFailure
          ? cause.body.message
          : 'Authentication could not be verified.')
    } finally {
      if (!background) setLoading(false)
    }
  }, [api, session])

  /**
   * Mirrors `identity` for the subscription below, which must read the CURRENT
   * value without re-subscribing every time it changes. A ref rather than a
   * dependency: putting `identity` in the effect's deps would tear down and
   * rebuild the Supabase subscription on every sign-in.
   */
  const identityRef = useRef<Identity | null>(null)
  identityRef.current = identity

  useEffect(() => {
    void load()

    /**
     * A TOKEN REFRESH IS NOT A SIGN-IN, AND MUST NOT COST A ROUND TRIP.
     *
     * This used to call `load({ background: true })` on every Supabase auth
     * event. Supabase fires one on every token refresh and every time a phone
     * returns from the background, so a person working through a morning
     * produced dozens of `/auth/me` calls — and each of those was, on the
     * server, a row update plus an audit insert plus two transactions. It was
     * measured in production as the single most expensive query in the
     * database.
     *
     * The only two transitions that can change who the user is are:
     *
     *   token gone      -> signed out, or the session expired. Re-resolve, which
     *                      clears the identity and returns to the sign-in card.
     *   token, no identity -> a fresh sign-in (or a first resolve that failed).
     *                      Re-resolve to learn the role.
     *
     * A new token while an identity is already held is a REFRESH of the session
     * we already resolved. The subject cannot change without a sign-out first,
     * so there is nothing to learn and the call is skipped.
     *
     * What this gives up: a role changed by an admin mid-session no longer
     * lands on the next token refresh — it lands on the next page load or
     * sign-in. The server-side guards are unaffected; every protected request
     * still loads the principal and still checks the revocation deny-list, so a
     * demoted user is refused by the API regardless of what this screen shows.
     */
    return session.subscribe((token) => {
      if (!token || !identityRef.current) {
        void load({ background: true })
      }
    })
  }, [load, session])

  const value: AuthState = {
    api,
    identity,
    loading,
    error,
    async signIn() {
      setError(null)
      try {
        await session.signIn()
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : 'Microsoft sign-in could not start.')
      }
    },
    async signOut() {
      await session.signOut()
      // A coordinator's phone is passed around during an event, so the next
      // person to sign in must not inherit the previous one's screen — including
      // the card populator's per-student progress.
      clearPersistedState()
      // The same rule, for the half of the screen that never touched storage.
      // clearPersistedState only reaches what this app wrote to sessionStorage;
      // every answer the API gave — rosters, marks, disputes, the live board —
      // lives in TanStack's in-memory cache, which survives a sign-out untouched.
      // The next person to sign in on the same phone would be handed the
      // previous coordinator's data on screen, from cache, before their own
      // first request came back.
      onSignOut?.()
      setIdentity(null)
    },
    reload: load,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

/**
 * The "we do not know yet" state, shared by every gate.
 *
 * One component rather than the same markup in each caller, so the whole app
 * agrees on what waiting looks like — and so RoleLanding, which decides where to
 * send someone, cannot render a DIFFERENT waiting state from the boundary that
 * then receives them.
 */
export function SessionLoading() {
  return <LoadingScreen label="Checking your session" />
}

/**
 * The university lockup that heads every signed-out surface, plus the theme
 * control — these cards have no chrome of their own, so without it the toggle
 * would be unreachable for anyone who is not signed in.
 *
 * Exported because the front door renders it too: HomeLanding is the screen a
 * signed-out visitor actually lands on, and it is not inside any gate.
 */
export function AuthBrand() {
  return (
    <div className="relative w-full flex justify-end mb-8">
      <div className="absolute top-0 right-0">
        <ThemeToggle />
      </div>
      <div className="w-full flex justify-center mt-12 mb-4">
        <img
          className="h-16 md:h-20 w-auto object-contain transition-all duration-300"
          src="/brand/amrita-logo.png"
          alt="Amrita Vishwa Vidyapeetham"
          width={600}
          height={155}
        />
      </div>
    </div>
  )
}

/**
 * Gates a route on being signed in AND holding one of `allowedRoles`.
 *
 * The default is `['admin']` deliberately: every portal route calls this with no
 * roles at all, so the default IS their access rule. Widening it — to "any
 * signed-in user", or by dropping the check — would expose every admin screen to
 * every student in the university, which is why the marking surface passes its
 * own role rather than the default being relaxed to accommodate it.
 */
export function SessionBoundary({
  children,
  allowedRoles = ['admin'],
}: {
  children: ReactNode
  allowedRoles?: readonly string[]
}) {
  const auth = useAuth()
  if (auth.loading || (!auth.identity && hasPendingAuthCallback())) {
    return <SessionLoading />
  }
  if (auth.error) {
    return (
      <main className="min-h-screen flex items-center justify-center p-4">
        <motion.div 
          initial={{ opacity: 0, y: 20, scale: 0.95 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          transition={{ type: "spring", damping: 25, stiffness: 300 }}
          className="w-full max-w-md bg-surface border border-border rounded-2xl shadow-sm p-8 text-center"
        >
          <AuthBrand />
          <h1 className="text-2xl font-bold mb-4 tracking-tight">Sign in to OD-Amrita</h1>
          <div role="alert" className="bg-status-error-bg text-status-error-fg p-4 rounded-xl mb-6 text-sm font-medium">
            {auth.error}
          </div>
          <button 
            type="button" 
            onClick={() => void auth.reload()}
            className="primary w-full py-3.5"
          >
            Try again
          </button>
        </motion.div>
      </main>
    )
  }
  // NO SESSION — GO TO THE FRONT DOOR, don't render a sign-in card here.
  //
  // This used to be its own bare sign-in card, shown at whatever guarded URL the
  // visitor happened to be on. That is where signing out left a coordinator: the
  // account menu clears the identity but not the address bar, so they stayed on
  // `/mark` looking at a card that offered exactly one thing — sign in again —
  // with no way to the public board short of typing a URL. Somebody who just
  // signed out is the LEAST likely person to want to sign straight back in.
  //
  // Redirecting to `/` reaches HomeLanding, which offers both of the things a
  // signed-out visitor can actually want. Nothing is lost by leaving the guarded
  // URL behind: the provider's redirect names the root for everybody (it cannot
  // know a role yet — see RoleLanding), so the old card never returned anyone to
  // the page they were on either.
  //
  // `replace`, so Back does not walk into the guarded route and bounce out again.
  if (!auth.identity) {
    return <Navigate to="/" replace />
  }
  if (!allowedRoles.includes(auth.identity.role)) {
    return (
      <main className="min-h-screen flex items-center justify-center p-4">
        <motion.div 
          initial={{ opacity: 0, y: 20, scale: 0.95 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          transition={{ type: "spring", damping: 25, stiffness: 300 }}
          className="w-full max-w-md bg-surface border border-border rounded-2xl shadow-sm p-8 text-center"
        >
          <AuthBrand />
          <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-2">OD-Amrita portal</p>
          <h1 className="text-2xl font-bold mb-4 tracking-tight">Access denied</h1>
          <p className="text-foreground-muted mb-8 leading-relaxed">
            {deniedReason(allowedRoles)}
          </p>
          <button 
            type="button" 
            onClick={() => void auth.signOut()}
            className="secondary w-full py-3.5"
          >
            Sign out
          </button>
        </motion.div>
      </main>
    )
  }
  return children
}

/** Says which access the screen needs, so a refusal is never a dead end. */
function deniedReason(allowedRoles: readonly string[]): string {
  if (allowedRoles.length === 1 && allowedRoles[0] === 'coordinator') {
    return 'Coordinator access is required to mark attendance. Ask an administrator to assign you as a coordinator.'
  }
  if (allowedRoles.length === 1 && allowedRoles[0] === 'admin') {
    return 'Administrator access is required for event administration.'
  }
  return `This screen requires one of these roles: ${allowedRoles.join(', ')}.`
}
