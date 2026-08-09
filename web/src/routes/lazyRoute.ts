import { lazy, type ComponentType } from 'react'

/**
 * `React.lazy`, but it survives the network not being ready.
 *
 * THE FAILURE THIS FIXES, observed in production: open the app on a phone that
 * has just woken, navigate, and get "Failed to fetch dynamically imported
 * module" — then a manual refresh works fine. Every screen here is code-split,
 * so opening one is a network request, and the radio is frequently not up yet in
 * the second after the phone is unlocked. React.lazy caches the REJECTED promise,
 * so that single bad moment permanently breaks the route until the page is
 * reloaded by hand: it never tries again on its own.
 *
 * Two things are therefore needed, and they answer different causes:
 *
 *  1. RETRY. A transient failure is by far the common case, and a second attempt
 *     a moment later almost always succeeds. Retrying costs nothing when the
 *     first attempt worked.
 *
 *  2. RELOAD ONCE, as the last resort. If every attempt fails, the other real
 *     cause is a STALE chunk: the tab has been open across a deployment and is
 *     asking for a filename from the previous build. No amount of retrying fixes
 *     that, because the document itself is out of date — only fetching a fresh
 *     index.html does, which is exactly what a manual refresh was doing.
 *
 * The reload is guarded so it can happen at most once in a short window. A
 * reload loop on a screen someone is trying to use during an event would be far
 * worse than the error page it replaces, and a genuinely offline device would
 * otherwise reload forever.
 */

const RELOAD_GUARD_KEY = 'od-amrita.chunk-reload-at'
/** Long enough that a persistent failure cannot loop, short enough that a real
 *  deployment an hour later still self-heals. */
const RELOAD_GUARD_MS = 30_000

function alreadyReloadedRecently(): boolean {
  try {
    const previous = Number(window.sessionStorage.getItem(RELOAD_GUARD_KEY) ?? '0')
    return Number.isFinite(previous) && Date.now() - previous < RELOAD_GUARD_MS
  } catch {
    // No storage means no guard, and an unguarded reload can loop — so treat an
    // unreadable guard as "already reloaded" and show the error instead.
    return true
  }
}

function markReloaded() {
  try {
    window.sessionStorage.setItem(RELOAD_GUARD_KEY, String(Date.now()))
  } catch {
    // Handled by alreadyReloadedRecently returning true on the next read.
  }
}

/** Exported for tests; production passes the real timer. */
export const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

export type LazyRouteOptions = {
  attempts?: number
  /** Delay before the Nth retry. Grows, so a slow reconnect gets more room. */
  delayMs?: (attempt: number) => number
  reload?: () => void
}

/**
 * Loads a route module, retrying before giving up, and reloading the page once
 * if the module genuinely cannot be fetched.
 */
export async function loadRouteModule<T>(
  load: () => Promise<T>,
  {
    attempts = 3,
    delayMs = (attempt) => 300 * attempt,
    reload = () => window.location.reload(),
  }: LazyRouteOptions = {},
): Promise<T> {
  let lastError: unknown
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      return await load()
    } catch (error) {
      lastError = error
      if (attempt < attempts) await wait(delayMs(attempt))
    }
  }

  if (!alreadyReloadedRecently()) {
    markReloaded()
    reload()
    // The reload is asynchronous. Hand back a promise that never settles so
    // React does not render an error state during the moment before the page
    // goes away — a flash of "something went wrong" on a screen that is about
    // to recover on its own reads as a fault that was never there.
    return new Promise<T>(() => {})
  }

  throw lastError
}

/** `lazy` for a route component, with the recovery above applied. */
export function lazyRoute<T extends ComponentType<any>>(
  load: () => Promise<{ default: T }>,
  options?: LazyRouteOptions,
) {
  return lazy(() => loadRouteModule(load, options))
}
