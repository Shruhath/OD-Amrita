import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { motion } from 'motion/react'
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

function AuthBrand() {
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
          <div className="bg-status-error-bg text-status-error-fg p-4 rounded-xl mb-6 text-sm font-medium">
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
  if (!auth.identity) {
    return (
      <main className="min-h-screen flex items-center justify-center p-4">
        <motion.div 
          initial={{ opacity: 0, y: 20, scale: 0.95 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          transition={{ type: "spring", damping: 25, stiffness: 300 }}
          className="w-full max-w-md bg-surface border border-border rounded-2xl shadow-sm p-8 flex flex-col items-center text-center"
        >
          <AuthBrand />
          <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-2">Event administration</p>
          <h1 className="text-2xl font-bold mb-2 tracking-tight">Sign in to OD-Amrita</h1>
          <p className="text-foreground-muted mb-8 leading-relaxed">
            Use your university Microsoft account to continue.
          </p>
          <button 
            type="button" 
            onClick={() => void auth.signIn()}
            className="primary w-full py-3.5 flex items-center justify-center gap-3"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 21 21">
              <rect x="1" y="1" width="9" height="9" fill="#f25022"/>
              <rect x="11" y="1" width="9" height="9" fill="#7fba00"/>
              <rect x="1" y="11" width="9" height="9" fill="#00a4ef"/>
              <rect x="11" y="11" width="9" height="9" fill="#ffb900"/>
            </svg>
            Sign in with Microsoft
          </button>
          <p className="text-xs text-foreground-muted mt-6 max-w-xs leading-relaxed">
            Use your university address — for example <br/>
            <code className="bg-canvas/50 px-1 py-0.5 rounded font-mono text-[10px] mt-1 inline-block border border-border/50">cb.sc.u4cse24124@cb.students.amrita.edu</code>.
          </p>
        </motion.div>
      </main>
    )
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
