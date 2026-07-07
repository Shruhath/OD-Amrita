/**
 * Re-auth-required SEAM (Story 1.6, Task 5) — NOT a screen.
 *
 * The 401 interceptor cannot open a login screen: none exists yet (1.5a shipped backend-only
 * and deferred the mobile OAuth launch). So when the Supabase session is dead the interceptor
 * fires this pub/sub signal; a FUTURE login story subscribes and drives the actual re-auth UI.
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
      // change the set mid-iteration.
      for (const listener of [...listeners]) listener();
    },
  };
}

/**
 * App-wide singleton the interceptor's `onReauthRequired` is wired to (see api/client.ts).
 * A future login story consumes it via `reauthSignal.subscribe(...)`.
 */
export const reauthSignal = createReauthSignal();
