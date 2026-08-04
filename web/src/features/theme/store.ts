import { useSyncExternalStore } from 'react'
import {
  DARK_MEDIA_QUERY,
  applyResolvedTheme,
  nextTheme,
  readStoredPreference,
  resolveTheme,
  storePreference,
  type ResolvedTheme,
  type ThemePreference,
} from './theme'

/**
 * The theme as an external store rather than a React context.
 *
 * It is genuinely global — one document, one `data-theme` attribute — and it is
 * read by controls in three unrelated trees (the portal chrome, the marking
 * header, the auth cards). A provider would have to sit above all of them and
 * then be repeated in the setup of every test that renders any of them, which
 * couples a decorative control into the wiring of the authentication tests.
 *
 * `useSyncExternalStore` gives every mounted toggle the same value with no
 * provider at all.
 */

type Snapshot = { preference: ThemePreference; resolved: ResolvedTheme }

const listeners = new Set<() => void>()

let preference: ThemePreference = 'system'
let prefersDark = false
// Cached so that getSnapshot returns a referentially stable value between
// changes — returning a fresh object each call makes React re-render forever.
let snapshot: Snapshot = { preference, resolved: 'light' }
let mediaQuery: MediaQueryList | null = null

function systemQuery(): MediaQueryList | null {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return null
  return window.matchMedia(DARK_MEDIA_QUERY)
}

function publish() {
  const resolved = resolveTheme(preference, prefersDark)
  if (snapshot.preference === preference && snapshot.resolved === resolved) return
  snapshot = { preference, resolved }
  applyResolvedTheme(typeof document === 'undefined' ? null : document, resolved)
  for (const listener of listeners) listener()
}

/**
 * Re-reads the environment. Run whenever the first listener attaches, so the
 * store picks up a preference written by another tab — and so each test starts
 * from the storage it just seeded rather than from whatever the previous test
 * left in module scope.
 */
function syncFromEnvironment() {
  preference = readStoredPreference(typeof window === 'undefined' ? null : window.localStorage)
  prefersDark = systemQuery()?.matches ?? false
  publish()
}

function onSystemChange(event: MediaQueryListEvent) {
  prefersDark = event.matches
  publish()
}

function subscribe(listener: () => void): () => void {
  if (listeners.size === 0) {
    syncFromEnvironment()
    mediaQuery = systemQuery()
    mediaQuery?.addEventListener('change', onSystemChange)
  }
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) {
      mediaQuery?.removeEventListener('change', onSystemChange)
      mediaQuery = null
    }
  }
}

function getSnapshot(): Snapshot {
  return snapshot
}

export function setThemePreference(next: ThemePreference): void {
  preference = next
  storePreference(typeof window === 'undefined' ? null : window.localStorage, next)
  publish()
}

/**
 * Flips to the opposite of what is RENDERED, then pins it. Stepping the
 * preference instead (system -> light) would change nothing on screen for
 * someone whose OS is already light.
 */
export function toggleTheme(): void {
  setThemePreference(nextTheme(resolveTheme(preference, prefersDark)))
}

export function useTheme() {
  const current = useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  return {
    preference: current.preference,
    resolved: current.resolved,
    setPreference: setThemePreference,
    toggle: toggleTheme,
  }
}
