/**
 * ATDD red-phase scaffold — Story 1.6, Task 5: the re-auth-required SEAM (no UI).
 *
 * TDD RED PHASE: `test(...)` with real assertions. RED today because `../session`
 * does not exist yet. A future login story consumes this seam; the 401 interceptor's
 * `onReauthRequired` callback is wired to `signal.emit` in app code.
 *
 * SEAM CONTRACT this file pins:
 *   createReauthSignal(): { subscribe(listener): () => void; emit(): void }
 * subscribe() returns an unsubscribe function. This is a pub/sub seam — NOT a screen.
 */

import { createReauthSignal } from '../session';

describe('re-auth-required seam', () => {
  test('emit() notifies every current subscriber exactly once', () => {
    const signal = createReauthSignal();
    const l1 = jest.fn();
    const l2 = jest.fn();
    signal.subscribe(l1);
    signal.subscribe(l2);

    signal.emit();

    expect(l1).toHaveBeenCalledTimes(1);
    expect(l2).toHaveBeenCalledTimes(1);
  });

  test('unsubscribe() stops further delivery to that listener only', () => {
    const signal = createReauthSignal();
    const l1 = jest.fn();
    const l2 = jest.fn();
    const unsub1 = signal.subscribe(l1);
    signal.subscribe(l2);

    unsub1();
    signal.emit();

    expect(l1).not.toHaveBeenCalled(); // unsubscribed before the emit
    expect(l2).toHaveBeenCalledTimes(1); // still subscribed
  });
});
