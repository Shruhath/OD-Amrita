// Kept apart from session.ts on purpose: session.ts creates the Supabase client
// when it is imported, so anything that only needs this class (AuthGate, and
// through it every surface and test harness without auth configuration) must
// not import session.ts at runtime.

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
