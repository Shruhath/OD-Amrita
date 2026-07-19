/**
 * ATDD RED PHASE — Story 1.9, Task 2: closing the 1.6 re-auth seam.
 *
 * These tests FAIL until `../sessionGate` exists.
 *
 * WHY THIS MODULE EXISTS. Story 1.6 built the 401 interceptor and deliberately stopped short of a
 * screen, leaving a pub/sub seam behind (`session.ts`: "a FUTURE login story subscribes"). To this
 * day `reauthSignal` has ZERO subscribers. That means: when the interceptor exhausts its silent
 * refresh + retry and gives up, it emits into the void — and the user is stranded on a screen that
 * 401s forever, with no way back to sign-in. This story is the one that subscribes.
 *
 * WHY IT IS A MODULE AND NOT JUST App.tsx. D3 says no router: `App.tsx` switches on session state
 * between two screens. That is right, and this does not change it. But the DECISION — "is there a
 * session, and what happens when re-auth is demanded" — has to be testable without mounting the
 * app or reaching for the Supabase singleton. So the decision lives here, over injected ports, and
 * `App.tsx` stays a thin shell that renders what the gate reports. Same house pattern as 1.6.
 *
 * SEAM CONTRACT these tests pin (the dev implements exactly this shape):
 *
 *   createSessionGate({ auth, reauthSignal }) -> {
 *     start(onSession: (session: SessionLike | null) => void): () => void   // returns unsubscribe
 *   }
 */

import { createReauthSignal } from '../session';
import { createSessionGate, type SessionGateAuthPort, type SessionLike } from '../sessionGate';

// ---------------------------------------------------------------------------
// Test doubles (deterministic, no live Supabase)
// ---------------------------------------------------------------------------

const aSession = (): SessionLike => ({ access_token: 'at', refresh_token: 'rt' });

/**
 * A Supabase-auth-like fake exposing the two things the gate needs: the current session, and a
 * change stream. `emitAuthChange` lets a test drive Supabase's `onAuthStateChange` callback.
 */
function fakeAuth(initial: SessionLike | null) {
  let listener: ((session: SessionLike | null) => void) | null = null;
  const calls = { signOut: 0, unsubscribed: 0 };

  const auth: SessionGateAuthPort = {
    async getSession() {
      return { data: { session: initial } };
    },
    onAuthStateChange(cb) {
      listener = cb;
      return {
        data: {
          subscription: {
            unsubscribe() {
              calls.unsubscribed += 1;
              listener = null;
            },
          },
        },
      };
    },
    async signOut() {
      calls.signOut += 1;
      return { error: null };
    },
  };

  return {
    auth,
    calls,
    emitAuthChange(session: SessionLike | null) {
      listener?.(session);
    },
  };
}

// ---------------------------------------------------------------------------
// 1.9-UNIT-008 [P1] — THE seam closure: a dead session returns the user to sign-in
// ---------------------------------------------------------------------------

test('[P1] when re-auth is signalled the session is dropped and the user is sent back to sign-in', async () => {
  const reauthSignal = createReauthSignal();
  const { auth, calls } = fakeAuth(aSession());
  const gate = createSessionGate({ auth, reauthSignal });

  const seen: Array<SessionLike | null> = [];
  gate.start((session) => seen.push(session));

  // The interceptor has exhausted its refresh + retry: the Supabase session is dead.
  reauthSignal.emit();

  // Flush the gate's async signOut before asserting.
  await Promise.resolve();

  expect(calls.signOut).toBe(1);
  // null === "no session" === App.tsx renders SignInScreen. Without this, the user is stranded on
  // a screen that will 401 forever.
  expect(seen[seen.length - 1]).toBeNull();
});

test('[P1] the gate reports the initial session so a returning user skips sign-in', async () => {
  const reauthSignal = createReauthSignal();
  const { auth } = fakeAuth(aSession());
  const gate = createSessionGate({ auth, reauthSignal });

  const seen: Array<SessionLike | null> = [];
  gate.start((session) => seen.push(session));

  // getSession() is async — let it settle.
  await Promise.resolve();
  await Promise.resolve();

  expect(seen[seen.length - 1]).not.toBeNull();
});

test('[P1] auth-state changes from Supabase are forwarded to the app shell', async () => {
  const reauthSignal = createReauthSignal();
  const { auth, emitAuthChange } = fakeAuth(null);
  const gate = createSessionGate({ auth, reauthSignal });

  const seen: Array<SessionLike | null> = [];
  gate.start((session) => seen.push(session));

  // A sign-in completed: Supabase pushes the new session.
  emitAuthChange(aSession());

  expect(seen[seen.length - 1]).not.toBeNull();
});

// ---------------------------------------------------------------------------
// [P1] — stopping the gate must not leave zombies behind
// ---------------------------------------------------------------------------

test('[P1] stopping the gate unsubscribes from BOTH Supabase and the re-auth signal', async () => {
  const reauthSignal = createReauthSignal();
  const { auth, calls } = fakeAuth(aSession());
  const gate = createSessionGate({ auth, reauthSignal });

  const seen: Array<SessionLike | null> = [];
  const stop = gate.start((session) => seen.push(session));

  stop();
  const countAfterStop = seen.length;

  // A signal arriving after the gate stopped must be inert — a stopped gate that still calls
  // signOut would log the user out from under a screen that is no longer mounted.
  reauthSignal.emit();
  await Promise.resolve();

  expect(calls.unsubscribed).toBe(1);
  expect(calls.signOut).toBe(0);
  expect(seen).toHaveLength(countAfterStop);
});
