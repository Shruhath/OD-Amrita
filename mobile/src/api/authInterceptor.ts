/**
 * 401 auth interceptor — silent Supabase access-token refresh + retry-once.
 *
 * Auth model is HYBRID: the Supabase SDK owns sessions, refresh and revocation; the Go
 * backend verifies the Supabase JWT and returns `401 AUTH_TOKEN_INVALID` for an expired
 * OR a revoked token. This interceptor is the mobile client's response to that 401:
 *
 *   attach live bearer → on 401 ask Supabase to refresh (single-flight) → retry ONCE
 *   → if the session is dead (refresh fails/null, OR the retry still 401s) fire the re-auth
 *   seam once and stop.
 *
 * The interceptor is pure TS over two INJECTED collaborators — a Supabase-auth-like port
 * (`getSession`/`refreshSession`) and an HTTP transport — plus an `onReauthRequired`
 * callback. Nothing here reaches for a live Supabase project, a `.env`, or the global
 * `fetch`; that is what makes it deterministically unit-testable with fakes (AC-7).
 *
 * Boundary (AC-3): a transport that *rejects* (DNS/timeout/offline) is a thrown error, NOT
 * an HTTP 401 — it propagates unchanged and never triggers a refresh. Offline session
 * expiry is handled separately and is explicitly out of scope here.
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

/**
 * HTTP transport port: the real one wraps `fetch`; tests inject a scripted fake. Generic over
 * the concrete response `R` so a transport that yields a full `fetch` `Response` threads that
 * type through to callers — the app wiring stays type-safe instead of asserting with a cast.
 */
export type Transport<R extends ResponseLike = ResponseLike> = (
  path: string,
  init: RequestInit,
) => Promise<R>;

export interface AuthedRequestDeps<R extends ResponseLike = ResponseLike> {
  auth: SupabaseAuthPort;
  transport: Transport<R>;
  /** Fired exactly once per dead-session event; wired to the re-auth seam in app code. */
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
  if (token) {
    // Drop any case-variant of a caller-supplied Authorization header before setting ours:
    // a record holding both `authorization` and `Authorization` is folded by `fetch` into a
    // single malformed `Bearer x, Bearer <token>` credential (→ a spurious 401 loop).
    for (const k of Object.keys(headers)) {
      if (k.toLowerCase() === 'authorization') delete headers[k];
    }
    headers.Authorization = `Bearer ${token}`;
  }
  return { ...init, headers };
}

/**
 * A one-shot request body (a `ReadableStream`) is consumed by the first `fetch` and cannot be
 * replayed on the 401 retry. Detected by duck-typing (`ReadableStream` may be absent in some RN
 * runtimes). Reusable bodies (string/Blob/ArrayBuffer/URLSearchParams/FormData) retry fine.
 */
function isOneShotBody(body: BodyInit | null | undefined): boolean {
  return body != null && typeof (body as ReadableStream).getReader === 'function';
}

/**
 * Build the interceptor entrypoint. Single-flight refresh state is held PER FACTORY
 * INSTANCE (not module-global) so callers stay isolated and tests are parallel-safe.
 */
export function createAuthedRequest<R extends ResponseLike = ResponseLike>(
  deps: AuthedRequestDeps<R>,
): (path: string, init?: RequestInit) => Promise<R> {
  const { auth, transport, onReauthRequired } = deps;

  // AC-5 single-flight: N concurrent 401s share ONE refreshSession() call. The check-and-set
  // is synchronous, so even simultaneously-arriving 401s collapse onto the same in-flight
  // promise; `finally` clears it so a later 401 can refresh again.
  let inFlightRefresh: Promise<SessionLike | null> | null = null;

  // One-shot re-auth latch: a burst of concurrent dead-session 401s must fire onReauthRequired
  // EXACTLY once, not once per in-flight request. Re-armed whenever a request observes a live
  // session again (a non-401 response), so a later dead-session event can signal afresh.
  let reauthFired = false;

  function signalReauth(): never {
    if (!reauthFired) {
      reauthFired = true;
      onReauthRequired();
    }
    throw new SessionExpiredError();
  }

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

  return async function request(path: string, init?: RequestInit): Promise<R> {
    // AC-4: read the bearer from the LIVE session at call time, never a stale copy. A failure to
    // read the session (storage/decrypt error) is treated as "no token": the request goes out
    // unauthenticated and the normal 401 path drives refresh/re-auth rather than throwing raw.
    let token: string | undefined;
    try {
      token = (await auth.getSession()).data.session?.access_token;
    } catch {
      token = undefined;
    }
    const res = await send(path, init, token);

    // AC-6: everything that is not a 401 passes through untouched — no refresh, no retry. Seeing
    // a live session re-arms the one-shot re-auth latch.
    if (res.status !== 401) {
      reauthFired = false;
      return res;
    }

    // AC-5: one shared refresh for all concurrent 401s.
    const session = await refreshOnce();

    // AC-2: the discriminator is the REFRESH OUTCOME, not the 401 body. A dead session
    // (expired refresh token, or a network error during refresh) fires the re-auth seam
    // once and stops — no retry, no infinite loop.
    if (!session) {
      return signalReauth();
    }

    // A one-shot body was already consumed by the first send and cannot be replayed — surface a
    // clear error rather than fetch's cryptic "body stream already read".
    if (isOneShotBody(init?.body)) {
      throw new Error(
        'Cannot retry a 401 for a streamed request body (ReadableStream): use a string/Blob/' +
          'ArrayBuffer body so the request can be replayed after a token refresh.',
      );
    }

    // AC-1: retry EXACTLY once with the freshly refreshed bearer. Loop guard (AC-2): we never
    // re-enter the refresh path here.
    const retryRes = await send(path, init, session.access_token);

    // Decision (2026-07-07): a 401 that PERSISTS after a successful refresh means the session is
    // dead at the backend even though Supabase still minted a token — e.g. a 1.5c deny-list
    // forced logout Supabase does not know about yet. Treat it as dead: fire re-auth once and
    // stop. The loop guard is structural (no second refresh), so there is still no infinite loop.
    if (retryRes.status === 401) {
      return signalReauth();
    }
    reauthFired = false;
    return retryRes;
  };
}
