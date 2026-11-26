/**
 * Re-auth-required SEAM — NOT a screen.
 *
 * The 401 interceptor cannot open a login screen: none exists yet (1.5a shipped backend-only
 * and deferred the mobile OAuth launch). So when the Supabase session is dead the interceptor
 * fires this pub/sub signal; the session gate (sessionGate.ts) subscribes and drives the re-auth UI.
 * Keeping it a seam (not a UI) is deliberate — building a login screen here is out of scope.
 */

export interface ReauthSignal {
  /** Register a listener; returns an unsubscribe function that removes only that listener. */
  subscribe(listener: () => void): () => void;
  /** Notify every current subscriber exactly once. */
  emit(): void;
}

export function createReauthSignal(): ReauthSignal {
  const listeners = new Set<() => void>();
  return {
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    emit() {
      // Iterate over a snapshot so a listener that (un)subscribes during emit cannot
      // change the set mid-iteration. Each call is isolated: one throwing subscriber must not
      // suppress delivery to the others, nor propagate back into the interceptor's
      // onReauthRequired() call site mid-401-handling.
      for (const listener of [...listeners]) {
        try {
          listener();
        } catch {
          // Swallow — a misbehaving subscriber cannot break the re-auth signal for the rest.
        }
      }
    },
  };
}

/**
 * App-wide singleton the interceptor's `onReauthRequired` is wired to (see api/client.ts).
 * The session gate consumes it via `reauthSignal.subscribe(...)`.
 */
export const reauthSignal = createReauthSignal();
