/**
 * Session gate (Story 1.9, Task 2) — closing the 1.6 re-auth seam.
 *
 * Story 1.6 built the 401 interceptor and deliberately stopped short of a screen, leaving a
 * pub/sub seam behind (`session.ts`: "a FUTURE login story subscribes"). To this day
 * `reauthSignal` has zero subscribers: when the interceptor exhausts its silent refresh + retry
 * and gives up, it emits into the void, stranding the user on a screen that 401s forever with no
 * way back to sign-in. This module is the subscriber.
 *
 * WHY A MODULE AND NOT JUST App.tsx (D3: no router). `App.tsx` switches on session state between
 * two screens — that is right, and this does not change it. But the DECISION — "is there a
 * session, and what happens when re-auth is demanded" — has to be testable without mounting the
 * app or reaching for the Supabase singleton. So the decision lives here, over injected ports,
 * and `App.tsx` stays a thin shell that renders what the gate reports (same house pattern as the
 * 1.6 interceptor / api/client.ts split).
 */

export interface SessionLike {
  access_token: string;
  refresh_token: string;
}

/**
 * The slice of `supabase.auth` the gate depends on. Note `onAuthStateChange`'s callback here
 * takes ONLY the session — the real Supabase SDK callback is `(event, session) => void`; App.tsx
 * adapts that two-argument shape down to this one when it wires the real client, keeping this
 * module itself free of any Supabase-specific event vocabulary.
 */
export interface SessionGateAuthPort {
  getSession(): Promise<{ data: { session: SessionLike | null } }>;
  onAuthStateChange(callback: (session: SessionLike | null) => void): {
    data: { subscription: { unsubscribe(): void } };
  };
  signOut(): Promise<{ error: unknown }>;
}

export interface ReauthSignalLike {
  subscribe(listener: () => void): () => void;
}

export function createSessionGate({
  auth,
  reauthSignal,
}: {
  auth: SessionGateAuthPort;
  reauthSignal: ReauthSignalLike;
}) {
  return {
    /** Reports the current/incoming session to `onSession`. Returns an unsubscribe function. */
    start(onSession: (session: SessionLike | null) => void): () => void {
      let stopped = false;

      // The initial session, so a returning user skips sign-in.
      void auth.getSession().then(({ data }) => {
        if (stopped) return;
        onSession(data.session);
      });

      const {
        data: { subscription },
      } = auth.onAuthStateChange((session) => {
        if (stopped) return;
        onSession(session);
      });

      // The interceptor gave up on refresh + retry: the session is dead. Sign out locally
      // (best-effort revocation) and report null — the app shell falls back to sign-in.
      const unsubscribeReauth = reauthSignal.subscribe(() => {
        if (stopped) return;
        void auth.signOut().then(() => {
          if (stopped) return;
          onSession(null);
        });
      });

      return () => {
        stopped = true;
        subscription.unsubscribe();
        unsubscribeReauth();
      };
    },
  };
}
