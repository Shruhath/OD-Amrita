import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { Identity } from '../../api/client'
import { ThemeToggle } from '../../components/ThemeToggle'
import { ApiFailure, createEventApi } from '../../api/client'
import type { SessionPort } from './session'
import { hasPendingAuthCallback } from './callback'
import { clearPersistedState } from '../persistence/sessionState'
import { LoadingScreen } from '../../components/ui/Spinner'
import { AuthContext, useAuth, type AuthState } from './context'

export function AuthProvider({ children, session }: { children: ReactNode; session: SessionPort }) {
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

  useEffect(() => {
    void load()
    return session.subscribe(() => void load({ background: true }))
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
 */
function AuthBrand() {
  return (
    <>
      <div className="auth-toolbar">
        <ThemeToggle />
      </div>
      <img
        className="auth-brand"
        src="/brand/amrita-logo.png"
        alt="Amrita Vishwa Vidyapeetham"
        width={600}
        height={155}
      />
    </>
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
      <main className="center-state">
        <AuthBrand />
        <h1>Sign in to OD-Amrita</h1>
        <p role="alert">{auth.error}</p>
        <button type="button" onClick={() => void auth.reload()}>Try again</button>
      </main>
    )
  }
  if (!auth.identity) {
    return (
      <main className="center-state sign-in">
        <AuthBrand />
        <p className="eyebrow">Event administration</p>
        <h1>Sign in to OD-Amrita</h1>
        <p>Use your university Microsoft account to continue.</p>
        <button className="primary" type="button" onClick={() => void auth.signIn()}>
          Sign in with Microsoft
        </button>
        <p className="auth-hint">
          Use your university address — for example
          {' '}<code>cb.sc.u4cse24124@cb.students.amrita.edu</code>.
        </p>
      </main>
    )
  }
  if (!allowedRoles.includes(auth.identity.role)) {
    return (
      <main className="center-state">
        <AuthBrand />
        <p className="eyebrow">OD-Amrita portal</p>
        <h1>Access denied</h1>
        <p>{deniedReason(allowedRoles)}</p>
        <button type="button" onClick={() => void auth.signOut()}>Sign out</button>
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
