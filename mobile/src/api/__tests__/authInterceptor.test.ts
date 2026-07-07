/**
 * ATDD red-phase scaffold — Story 1.6: Mobile auth interceptor (silent 401 refresh).
 *
 * TDD RED PHASE. Every scenario is a `test(...)` with the real, expected-behavior
 * assertions already written. The suite is RED today for two reasons:
 *   1. `../authInterceptor` does not exist yet (import fails at collection).
 *   2. jest-expo / @types/jest are not installed yet (documented in mobile/jest.config.js).
 * The dev activates each scenario one-by-one during green (remove `.skip`, implement, watch it pass).
 *
 * PRINCIPLE: the interceptor is pure TS logic over TWO INJECTED PORTS — a Supabase-auth-like
 * port (`getSession` / `refreshSession`) and an HTTP transport — plus a re-auth callback.
 * No live Supabase, no network, no `.env` (AC-7). All collaborators here are fakes.
 *
 * SEAM CONTRACT this file pins (see the ATDD checklist "Planned seam under test"):
 *   createAuthedRequest({ auth, transport, onReauthRequired }) -> (path, init?) => Promise<{status}>
 * `authInterceptor.ts` MAY be folded into `client.ts` as long as these symbols are
 * re-exported from `../authInterceptor`. Single-flight refresh state is held PER FACTORY
 * INSTANCE (not module-global) so tests stay isolated — a deliberate refinement of the
 * Dev Notes' "module-level promise" wording.
 */

import {
  createAuthedRequest,
  SessionExpiredError,
  type SupabaseAuthPort,
  type SessionLike,
  type Transport,
} from '../authInterceptor';

// ---------------------------------------------------------------------------
// Test doubles (deterministic, no network) — data factories per data-factories.md
// ---------------------------------------------------------------------------

type TransportCall = { path: string; init: RequestInit; authorization: string | null };

/** Reads the Authorization header regardless of how the interceptor shaped `init.headers`. */
function authHeaderOf(init: RequestInit | undefined): string | null {
  const h = init?.headers;
  if (!h) return null;
  if (typeof (h as Headers).get === 'function') return (h as Headers).get('authorization');
  if (Array.isArray(h)) {
    const found = (h as [string, string][]).find(([k]) => k.toLowerCase() === 'authorization');
    return found ? found[1] : null;
  }
  const rec = h as Record<string, string>;
  const key = Object.keys(rec).find((k) => k.toLowerCase() === 'authorization');
  return key ? rec[key] : null;
}

/** Transport that returns a scripted status per call (last status repeats) and records every call. */
function scriptedTransport(statuses: number[]) {
  const calls: TransportCall[] = [];
  let i = 0;
  const transport: Transport = async (path, init) => {
    calls.push({ path, init, authorization: authHeaderOf(init) });
    const status = statuses[Math.min(i, statuses.length - 1)];
    i += 1;
    return { status };
  };
  return { transport, calls };
}

/** Transport that rejects (simulates offline / DNS / timeout — NOT an HTTP 401). */
function rejectingTransport(error: Error) {
  const calls: TransportCall[] = [];
  const transport: Transport = async (path, init) => {
    calls.push({ path, init, authorization: authHeaderOf(init) });
    throw error;
  };
  return { transport, calls };
}

function session(token: string): SessionLike {
  return { access_token: token };
}

/**
 * Fake Supabase auth port.
 * - `sessions`: values `getSession()` yields in order (last value repeats).
 * - `refresh`: the `refreshSession()` outcome (or a custom async impl for single-flight timing).
 */
function fakeAuth(opts: {
  sessions: (SessionLike | null)[];
  refresh?:
    | { data: { session: SessionLike | null }; error: unknown }
    | (() => Promise<{ data: { session: SessionLike | null }; error: unknown }>);
}) {
  let getCount = 0;
  const refresh = { count: 0 };
  const auth: SupabaseAuthPort = {
    async getSession() {
      const s = opts.sessions[Math.min(getCount, opts.sessions.length - 1)] ?? null;
      getCount += 1;
      return { data: { session: s }, error: null };
    },
    async refreshSession() {
      refresh.count += 1;
      const r = opts.refresh ?? { data: { session: null }, error: null };
      return typeof r === 'function' ? r() : r;
    },
  };
  return { auth, refresh, getCount: () => getCount };
}

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// ---------------------------------------------------------------------------
// AC-1 — 401 → refresh → retry ONCE with the new bearer
// ---------------------------------------------------------------------------
describe('AC-1: silent refresh + retry-once on 401', () => {
  test('401 then successful refresh retries the original request once with the refreshed token', async () => {
    const { auth, refresh } = fakeAuth({
      sessions: [session('access-old')],
      refresh: { data: { session: session('access-new') }, error: null },
    });
    const { transport, calls } = scriptedTransport([401, 200]);
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });
    const res = await request('/api/v1/auth/me');

    expect(res.status).toBe(200); // the retry's response is what the caller receives
    expect(refresh.count).toBe(1); // refreshed exactly once
    expect(calls).toHaveLength(2); // original + one retry, no more
    expect(calls[0].authorization).toBe('Bearer access-old'); // first attempt used the current token
    expect(calls[1].authorization).toBe('Bearer access-new'); // retry used the REFRESHED token
    expect(onReauthRequired).not.toHaveBeenCalled(); // refresh succeeded → no re-auth
  });
});

// ---------------------------------------------------------------------------
// AC-4 — every outbound request carries the live bearer (never a stale copy)
// ---------------------------------------------------------------------------
describe('AC-4: bearer attach from the live session', () => {
  test('a 200 request carries Authorization: Bearer <current token> and never refreshes', async () => {
    const { auth, refresh } = fakeAuth({ sessions: [session('access-live')] });
    const { transport, calls } = scriptedTransport([200]);

    const request = createAuthedRequest({ auth, transport, onReauthRequired: jest.fn() });
    const res = await request('/api/v1/auth/me');

    expect(res.status).toBe(200);
    expect(calls).toHaveLength(1);
    expect(calls[0].authorization).toBe('Bearer access-live');
    expect(refresh.count).toBe(0); // happy path never touches refresh
  });

  test('the token is read live per call, not cached from an earlier call', async () => {
    // getSession yields A then B: proves the interceptor re-reads the session each call
    const { auth } = fakeAuth({ sessions: [session('token-A'), session('token-B')] });
    const { transport, calls } = scriptedTransport([200]);

    const request = createAuthedRequest({ auth, transport, onReauthRequired: jest.fn() });
    await request('/api/v1/auth/me');
    await request('/api/v1/auth/me');

    expect(calls[0].authorization).toBe('Bearer token-A');
    expect(calls[1].authorization).toBe('Bearer token-B'); // NOT a cached 'token-A'
  });
});

// ---------------------------------------------------------------------------
// AC-6 — non-401 responses pass through untouched; refresh never engages
// ---------------------------------------------------------------------------
describe('AC-6: non-401 passthrough', () => {
  const passthroughStatuses = [200, 400, 403, 404, 409, 429, 500];
  passthroughStatuses.forEach((status) => {
    test(`status ${status} is returned untouched and never triggers a refresh`, async () => {
      const { auth, refresh } = fakeAuth({ sessions: [session('access-tok')] });
      const { transport, calls } = scriptedTransport([status]);

      const request = createAuthedRequest({ auth, transport, onReauthRequired: jest.fn() });
      const res = await request('/api/v1/auth/me');

      expect(res.status).toBe(status);
      expect(calls).toHaveLength(1); // no retry
      expect(refresh.count).toBe(0); // ONLY 401 engages refresh
    });
  });
});

// ---------------------------------------------------------------------------
// AC-2 — refresh fails / no session → no retry, re-auth signal, typed throw
// ---------------------------------------------------------------------------
describe('AC-2: dead session → re-auth fallback, no retry loop', () => {
  test('refresh yielding a null session fires re-auth once, throws SessionExpiredError, and does NOT retry', async () => {
    const { auth, refresh } = fakeAuth({
      sessions: [session('access-old')],
      refresh: { data: { session: null }, error: null },
    });
    const { transport, calls } = scriptedTransport([401]);
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });

    await expect(request('/api/v1/auth/me')).rejects.toBeInstanceOf(SessionExpiredError);
    expect(refresh.count).toBe(1);
    expect(calls).toHaveLength(1); // never retried the request
    expect(onReauthRequired).toHaveBeenCalledTimes(1); // re-auth seam fired exactly once
  });

  test('refresh returning an error (revoked session) is treated as dead: same fallback, discriminator is the refresh outcome not the 401 body', async () => {
    const { auth, refresh } = fakeAuth({
      sessions: [session('access-old')],
      refresh: { data: { session: null }, error: new Error('refresh_token_not_found') },
    });
    const { transport, calls } = scriptedTransport([401]);
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });

    await expect(request('/api/v1/auth/me')).rejects.toBeInstanceOf(SessionExpiredError);
    expect(refresh.count).toBe(1);
    expect(calls).toHaveLength(1);
    expect(onReauthRequired).toHaveBeenCalledTimes(1);
  });

  test('loop guard: a retried request that 401s again is not retried a third time (no infinite loop)', async () => {
    // refresh SUCCEEDS, but the retry still 401s (e.g. token revoked mid-flight).
    const { auth, refresh } = fakeAuth({
      sessions: [session('access-old')],
      refresh: { data: { session: session('access-new') }, error: null },
    });
    const { transport, calls } = scriptedTransport([401, 401]); // both attempts 401
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });
    const res = await request('/api/v1/auth/me');

    // Per the story pseudocode the post-refresh 401 is RETURNED (loop guard: `init.__retried`).
    // If the dev instead chooses to THROW on a post-refresh 401, swap this line for a rejects assertion —
    // the invariants below (single refresh, exactly two transport calls) are the non-negotiable part.
    expect(res.status).toBe(401);
    expect(refresh.count).toBe(1); // refreshed once, NOT again
    expect(calls).toHaveLength(2); // original + one retry only — never a third attempt
  });
});

// ---------------------------------------------------------------------------
// AC-5 — concurrent 401s share ONE refresh (single-flight)
// ---------------------------------------------------------------------------
describe('AC-5: single-flight refresh under concurrency', () => {
  test('two concurrent 401s trigger exactly one refreshSession; both retry against the shared new token', async () => {
    const gate = deferred<{ data: { session: SessionLike | null }; error: unknown }>();
    const { auth, refresh } = fakeAuth({
      sessions: [session('access-old')],
      refresh: () => gate.promise, // refresh stays in-flight until we open the gate
    });
    const { transport, calls } = scriptedTransport([401, 401, 200, 200]); // 2 initial 401s, then 2 retried 200s
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });

    // Fire both WITHOUT awaiting so both reach the refresh await while it is pending.
    const p1 = request('/api/v1/scans');
    const p2 = request('/api/v1/events');
    await Promise.resolve(); // let both hit their first 401 and enqueue on the single-flight refresh
    gate.resolve({ data: { session: session('access-new') }, error: null });

    const [r1, r2] = await Promise.all([p1, p2]);

    expect(r1.status).toBe(200);
    expect(r2.status).toBe(200);
    expect(refresh.count).toBe(1); // SINGLE-FLIGHT: one refresh for N concurrent 401s
    expect(calls).toHaveLength(4); // 2 initial + 2 retries
    expect(calls[2].authorization).toBe('Bearer access-new'); // retries used the shared refreshed token
    expect(calls[3].authorization).toBe('Bearer access-new');
    expect(onReauthRequired).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// AC-3 — offline boundary: a transport REJECTION is not a 401 (Story 4.5 territory)
// ---------------------------------------------------------------------------
describe('AC-3: transport rejection (offline) is not a 401', () => {
  test('a thrown network error propagates unchanged and never triggers refresh or re-auth', async () => {
    const netErr = new Error('Network request failed');
    const { auth, refresh } = fakeAuth({ sessions: [session('access-tok')] });
    const { transport, calls } = rejectingTransport(netErr);
    const onReauthRequired = jest.fn();

    const request = createAuthedRequest({ auth, transport, onReauthRequired });

    await expect(request('/api/v1/auth/me')).rejects.toBe(netErr); // the SAME error, unchanged
    expect(refresh.count).toBe(0); // offline must NOT be mistaken for an expired token
    expect(calls).toHaveLength(1); // no retry
    expect(onReauthRequired).not.toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// AC-7 — no live network: interceptor uses only the injected transport
// ---------------------------------------------------------------------------
describe('AC-7: no real network', () => {
  test('the interceptor never calls the real global fetch — only the injected transport', async () => {
    const realFetch = jest.fn();
    const prevFetch = (globalThis as { fetch?: unknown }).fetch;
    (globalThis as { fetch?: unknown }).fetch = realFetch;
    try {
      const { auth } = fakeAuth({
        sessions: [session('access-old')],
        refresh: { data: { session: session('access-new') }, error: null },
      });
      const { transport } = scriptedTransport([401, 200]); // exercise the refresh path too
      const request = createAuthedRequest({ auth, transport, onReauthRequired: jest.fn() });

      await request('/api/v1/auth/me');

      expect(realFetch).not.toHaveBeenCalled();
    } finally {
      (globalThis as { fetch?: unknown }).fetch = prevFetch;
    }
  });
});
