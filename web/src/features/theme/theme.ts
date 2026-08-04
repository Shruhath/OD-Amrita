/**
 * Theme resolution, kept as free functions with everything injected.
 *
 * The rules this encodes:
 *
 *   - There are THREE preferences but only TWO themes. `system` is a
 *     preference, never a rendered value, and it is the default: a first-time
 *     visitor gets whatever their OS is set to rather than an arbitrary choice.
 *   - The resolved theme is written to `<html data-theme>`, which is the single
 *     selector the stylesheet keys off. Nothing else reads the preference.
 *   - Storage is treated as untrusted: it is shared with anything else on the
 *     origin, and Safari throws on `localStorage` access in private mode rather
 *     than returning null. Every access is guarded.
 */

export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

/** Must stay in step with the boot script in index.html — asserted by a test. */
export const THEME_STORAGE_KEY = 'od-amrita-theme'

export const DARK_MEDIA_QUERY = '(prefers-color-scheme: dark)'

/** The colour the browser paints its own chrome with, per theme. */
const THEME_COLOR: Record<ResolvedTheme, string> = {
  light: '#b50246',
  dark: '#0d1017',
}

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

/**
 * Anything unrecognised — absent, corrupt, or written by an older build — falls
 * back to `system` rather than to a hard-coded theme.
 */
export function readStoredPreference(storage: Pick<Storage, 'getItem'> | null | undefined): ThemePreference {
  if (!storage) return 'system'
  try {
    const stored = storage.getItem(THEME_STORAGE_KEY)
    return isThemePreference(stored) ? stored : 'system'
  } catch {
    return 'system'
  }
}

/**
 * A failed write must not break the toggle: the theme still applies for this
 * session, it just will not survive a reload.
 */
export function storePreference(
  storage: Pick<Storage, 'setItem'> | null | undefined,
  preference: ThemePreference,
): void {
  if (!storage) return
  try {
    storage.setItem(THEME_STORAGE_KEY, preference)
  } catch {
    /* Private mode, or a full quota. Not worth surfacing to the user. */
  }
}

export function resolveTheme(preference: ThemePreference, systemPrefersDark: boolean): ResolvedTheme {
  if (preference === 'system') return systemPrefersDark ? 'dark' : 'light'
  return preference
}

/** The theme a toggle press should move to, given what is on screen now. */
export function nextTheme(resolved: ResolvedTheme): ResolvedTheme {
  return resolved === 'dark' ? 'light' : 'dark'
}

/**
 * Writes the resolved theme to the document. This is the ONLY place the
 * attribute is set from React; the boot script sets the same attribute before
 * first paint so the two never disagree on screen.
 */
export function applyResolvedTheme(documentRef: Document | null | undefined, resolved: ResolvedTheme): void {
  if (!documentRef) return
  documentRef.documentElement.setAttribute('data-theme', resolved)
  const meta = documentRef.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', THEME_COLOR[resolved])
}
