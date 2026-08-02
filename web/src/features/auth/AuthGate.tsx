import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { Identity } from '../../api/client'
import { ApiFailure, createEventApi } from '../../api/client'
import type { SessionPort } from './session'
import { AuthContext, useAuth, type AuthState } from './context'

export function AuthProvider({ children, session }: { children: ReactNode; session: SessionPort }) {
  const api = useMemo(() => createEventApi(session), [session])
  const [identity, setIdentity] = useState<Identity | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const token = await session.accessToken()
      if (!token) {
        setIdentity(null)
        return
      }
      setIdentity(await api.identity())
    } catch (cause) {
      setIdentity(null)
      // The API client has already attempted one refresh and cleared an invalid
      // session on 401. Return to the sign-in state instead of presenting a
      // retry loop for credentials that no longer exist.
      setError(cause instanceof ApiFailure && cause.status === 401
        ? null
        : cause instanceof ApiFailure
          ? cause.body.message
          : 'Authentication could not be verified.')
    } finally {
      setLoading(false)
    }
  }, [api, session])

  useEffect(() => {
    void load()
    return session.subscribe(() => void load())
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
      setIdentity(null)
    },
    reload: load,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

/** The university lockup that heads every signed-out surface. */
function AuthBrand() {
  return (
    <img
      className="auth-brand"
      src="/brand/amrita-logo.png"
      alt="Amrita Vishwa Vidyapeetham"
      width={600}
      height={155}
    />
  )
}

export function SessionBoundary({ children }: { children: ReactNode }) {
  const auth = useAuth()
  if (auth.loading) {
    return <main className="center-state"><p role="status">Checking your session…</p></main>
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
  if (auth.identity.role !== 'admin') {
    return (
      <main className="center-state">
        <AuthBrand />
        <p className="eyebrow">OD-Amrita portal</p>
        <h1>Access denied</h1>
        <p>Administrator access is required for event administration.</p>
        <button type="button" onClick={() => void auth.signOut()}>Sign out</button>
      </main>
    )
  }
  return children
}
