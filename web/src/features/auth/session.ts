import type { Session } from '@supabase/supabase-js'
import { SUPABASE_STORAGE_KEY, supabase } from '../../lib/supabase/client'

export type SessionPort = {
  accessToken(): Promise<string | null>
  refresh(): Promise<string | null>
  signIn(): Promise<void>
  signOut(): Promise<void>
  subscribe(listener: (token: string | null) => void): () => void
}

export const browserSession: SessionPort = {
  async accessToken() {
    const { data, error } = await supabase.auth.getSession()
    if (error) throw error
    return data.session?.access_token ?? null
  },

  async refresh() {
    const { data, error } = await supabase.auth.refreshSession()
    if (error) return null
    return data.session?.access_token ?? null
  },

  async signIn() {
    const { error } = await supabase.auth.signInWithOAuth({
      provider: 'azure',
      options: {
        // The ROOT, not a portal screen. `/portal/*` is admin-gated, so sending
        // everyone there means a coordinator finishes signing in and is told
        // "Access denied" by the very screen the redirect chose for them. The
        // root renders RoleLanding, which is the one place that knows a role and
        // can route on it — see routes/RoleLanding.tsx.
        redirectTo: new URL('/', window.location.origin).toString(),
        scopes: 'openid email profile',
      },
    })
    if (error) throw error
  },

  async signOut() {
    // The normal path: the auth server revokes the session, and Supabase then
    // drops it from this device.
    const remote = await settle(supabase.auth.signOut(), REMOTE_SIGN_OUT_TIMEOUT_MS)
    if (remote === null) return

    // The server could not be reached (or refused). Supabase keeps the local
    // session in that case, which on a phone passed between coordinators means
    // the next person inherits this account. End it on THIS device regardless:
    // first through Supabase's own local-only sign-out, then — because that
    // still sends a request, and waits on the same auth lock as the call that
    // just failed — by removing the stored session directly.
    await settle(supabase.auth.signOut({ scope: 'local' }), LOCAL_SIGN_OUT_TIMEOUT_MS)
    throw new SignOutError(remote, forgetSessionOnThisDevice())
  },

  subscribe(listener) {
    const { data } = supabase.auth.onAuthStateChange((_event, session: Session | null) => {
      listener(session?.access_token ?? null)
    })
    return () => data.subscription.unsubscribe()
  },
}

/**
 * A sign-out that did not complete normally.
 *
 * `localCleared` says whether the session is at least gone from this device. It
 * is true when only the server could not be told — the device is signed out,
 * but the session may stay valid elsewhere until it expires.
 */
export class SignOutError extends Error {
  readonly localCleared: boolean

  constructor(cause: unknown, localCleared: boolean) {
    super(cause instanceof Error ? cause.message : 'Sign-out did not complete.', { cause })
    this.name = 'SignOutError'
    this.localCleared = localCleared
  }
}

/**
 * Bounds the wait on the auth server. A request on a flaky network can hang for
 * minutes rather than fail, and a sign-out tapped on a shared phone must finish
 * while the person who tapped it is still holding it.
 */
export const REMOTE_SIGN_OUT_TIMEOUT_MS = 8_000
export const LOCAL_SIGN_OUT_TIMEOUT_MS = 3_000

/** Resolves to null on success, or to the failure — a returned error, a throw, or a timeout. */
async function settle(call: Promise<{ error: unknown }>, timeoutMs: number): Promise<unknown> {
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<{ error: unknown }>((resolve) => {
    timer = setTimeout(() => resolve({ error: new Error('The sign-out request timed out.') }), timeoutMs)
  })
  try {
    const { error } = await Promise.race([call, timeout])
    return error ?? null
  } catch (cause) {
    return cause ?? new Error('Sign-out failed.')
  } finally {
    clearTimeout(timer)
  }
}

/** Removes every key Supabase stores for the session. True when none remain. */
function forgetSessionOnThisDevice(): boolean {
  try {
    const keys = [SUPABASE_STORAGE_KEY, `${SUPABASE_STORAGE_KEY}-code-verifier`, `${SUPABASE_STORAGE_KEY}-user`]
    keys.forEach((key) => window.localStorage.removeItem(key))
    return keys.every((key) => window.localStorage.getItem(key) === null)
  } catch {
    return false
  }
}
