/**
 * Is the browser sitting on an OAuth redirect that has not been exchanged yet?
 *
 * Supabase is configured with `detectSessionInUrl`, so when the provider sends
 * the browser back it strips the credential out of the URL and fires an auth
 * state change — but that exchange is a round trip, and for its duration
 * `getSession()` truthfully answers "no session".
 *
 * Without this, the landing screen reads that honest "no session", concludes the
 * visitor is signed out, and renders the sign-in card at the exact moment they
 * have just finished signing in. They see a sign-in button flash past on the way
 * to the screen they asked for, which reads as a failed login.
 *
 * The PKCE flow this app uses returns `?code=`; the implicit flow returns
 * `#access_token=`. Both are matched so the guard does not quietly stop working
 * if the flow is ever switched. An `?error=` is deliberately NOT treated as
 * pending: a refused consent has nothing left to exchange, and waiting on it
 * would hang the screen on a spinner forever.
 */
export function hasPendingAuthCallback(
  search: string = typeof window === 'undefined' ? '' : window.location.search,
  hash: string = typeof window === 'undefined' ? '' : window.location.hash,
): boolean {
  if (new URLSearchParams(search).has('code')) return true
  return /(^|[#&])access_token=/.test(hash)
}
