/**
 * 401 auth interceptor — silent Supabase access-token refresh + retry-once (Story 1.6).
 *
 * Auth model is HYBRID: the Supabase SDK owns sessions, refresh and revocation; the Go
 * backend verifies the Supabase JWT and returns `401 AUTH_TOKEN_INVALID` for an expired
 * OR a revoked token. This interceptor is the mobile client's response to that 401:
 *
 *   attach live bearer → on 401 ask Supabase to refresh (single-flight) → retry ONCE
 *   → if the session is dead (refresh fails / null) fire the re-auth seam and stop.
 *
 * The interceptor is pure TS over two INJECTED collaborators — a Supabase-auth-like port
 * (`getSession`/`refreshSession`) and an HTTP transport — plus an `onReauthRequired`
 * callback. Nothing here reaches for a live Supabase project, a `.env`, or the global
 * `fetch`; that is what makes it deterministically unit-testable with fakes (AC-7).
 *
 * Boundary (AC-3): a transport that *rejects* (DNS/timeout/offline) is a thrown error, NOT
 * an HTTP 401 — it propagates unchanged and never triggers a refresh. Offline session
 * expiry is Story 4.5's job, explicitly out of scope here.
 */

/** Thrown when the Supabase session is dead (refresh failed or yielded no session). */
export class SessionExpiredError extends Error {
  constructor(message = 'Supabase session expired or revoked; re-authentication required') {
    super(message);
    this.name = 'SessionExpiredError';
  }
}

/** The single field of a Supabase session the interceptor needs — kept narrow for testability. */
export interface SessionLike {
  access_token: string;
}

/**
 * The slice of `supabase.auth` the interceptor depends on. The real client satisfies this
 * structurally; tests inject a fake. Both methods mirror supabase-js's
 * `{ data: { session }, error }` shape — a null session OR a non-null error means "dead".
 */
export interface SupabaseAuthPort {
  getSession(): Promise<{ data: { session: SessionLike | null }; error: unknown }>;
  refreshSession(): Promise<{ data: { session: SessionLike | null }; error: unknown }>;
}

/** Minimal response contract — only the status drives interceptor decisions. */
export type ResponseLike = { status: number };

/** HTTP transport port: the real one wraps `fetch`; tests inject a scripted fake. */
export type Transport = (path: string, init: RequestInit) => Promise<ResponseLike>;

export interface AuthedRequestDeps {
  auth: SupabaseAuthPort;
  transport: Transport;
  /** Fired exactly once when the session is dead; wired to the re-auth seam in app code. */
  onReauthRequired: () => void;
}

/** Merge caller headers (Headers | array | record | undefined) with the bearer into a plain record. */
function withBearer(init: RequestInit | undefined, token: string | undefined): RequestInit {
  const headers: Record<string, string> = {};
  const existing = init?.headers;
  if (existing) {
    if (Array.isArray(existing)) {
      for (const [k, v] of existing) headers[k] = v;
    } else if (typeof (existing as Headers).forEach === 'function') {
      (existing as Headers).forEach((v, k) => {
        headers[k] = v;
      });
    } else {
      Object.assign(headers, existing as Record<string, string>);
    }
  }
  if (token) headers.Authorization = `Bearer ${token}`;
  return { ...init, headers };
}

/**
 * Build the interceptor entrypoint. Single-flight refresh state is held PER FACTORY
 * INSTANCE (not module-global) so callers stay isolated and tests are parallel-safe.
 */
export function createAuthedRequest(
  deps: AuthedRequestDeps,
): (path: string, init?: RequestInit) => Promise<ResponseLike> {
  const { auth, transport, onReauthRequired } = deps;

  // AC-5 single-flight: N concurrent 401s share ONE refreshSession() call. The check-and-set
  // is synchronous, so even simultaneously-arriving 401s collapse onto the same in-flight
  // promise; `finally` clears it so a later 401 can refresh again.
  let inFlightRefresh: Promise<SessionLike | null> | null = null;

  function refreshOnce(): Promise<SessionLike | null> {
    if (!inFlightRefresh) {
      inFlightRefresh = auth
        .refreshSession()
        .then(({ data }) => data.session ?? null)
        .catch(() => null)
        .finally(() => {
          inFlightRefresh = null;
        });
    }
    return inFlightRefresh;
  }

  async function send(path: string, init: RequestInit | undefined, token: string | undefined) {
    return transport(path, withBearer(init, token));
  }

  return async function request(path: string, init?: RequestInit): Promise<ResponseLike> {
    // AC-4: read the bearer from the LIVE session at call time, never a stale copy.
    const token = (await auth.getSession()).data.session?.access_token;
    const res = await send(path, init, token);

    // AC-6: everything that is not a 401 passes through untouched — no refresh, no retry.
    if (res.status !== 401) return res;

    // AC-5: one shared refresh for all concurrent 401s.
    const session = await refreshOnce();

    // AC-2: the discriminator is the REFRESH OUTCOME, not the 401 body. A dead session
    // (revoked via 1.5c forced logout, or expired refresh token) fires the re-auth seam
    // once and stops — no retry, no infinite loop.
    if (!session) {
      onReauthRequired();
      throw new SessionExpiredError();
    }

    // AC-1: retry EXACTLY once with the freshly refreshed bearer. This is the loop guard
    // (AC-2): we do not re-enter the refresh path, so a post-refresh 401 is returned as-is.
    return send(path, init, session.access_token);
  };
}
