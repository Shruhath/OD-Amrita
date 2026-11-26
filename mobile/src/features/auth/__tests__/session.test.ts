/**
 * Unit suite — the re-auth-required SEAM (no UI).
 *
 * `test(...)` with real assertions against the shipped `../session`. The session gate
 * consumes this seam; the 401 interceptor's `onReauthRequired` callback is wired to
 * `signal.emit` in app code.
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

  test('a throwing subscriber does not suppress delivery to the others, and emit() does not throw', () => {
    const signal = createReauthSignal();
    const l1 = jest.fn(() => {
      throw new Error('subscriber blew up');
    });
    const l2 = jest.fn();
    signal.subscribe(l1);
    signal.subscribe(l2);

    expect(() => signal.emit()).not.toThrow(); // isolation: the throw is swallowed
    expect(l1).toHaveBeenCalledTimes(1);
    expect(l2).toHaveBeenCalledTimes(1); // still delivered despite l1 throwing
  });
});
