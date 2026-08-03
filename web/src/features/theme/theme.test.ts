import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import {
  DARK_MEDIA_QUERY,
  THEME_STORAGE_KEY,
  applyResolvedTheme,
  isThemePreference,
  nextTheme,
  readStoredPreference,
  resolveTheme,
  storePreference,
} from './theme'

describe('theme resolution', () => {
  test('system follows the OS in both directions', () => {
    expect(resolveTheme('system', true)).toBe('dark')
    expect(resolveTheme('system', false)).toBe('light')
  })

  test('an explicit preference overrides the OS', () => {
    expect(resolveTheme('light', true)).toBe('light')
    expect(resolveTheme('dark', false)).toBe('dark')
  })

  test('a toggle press moves away from what is on screen', () => {
    expect(nextTheme('dark')).toBe('light')
    expect(nextTheme('light')).toBe('dark')
  })
})

describe('stored preference', () => {
  const storageOf = (value: string | null) => ({ getItem: () => value })

  test('reads a valid preference back', () => {
    expect(readStoredPreference(storageOf('dark'))).toBe('dark')
    expect(readStoredPreference(storageOf('light'))).toBe('light')
    expect(readStoredPreference(storageOf('system'))).toBe('system')
  })

  test.each([null, '', 'blue', '{"theme":"dark"}'])('falls back to system for %j', (stored) => {
    expect(readStoredPreference(storageOf(stored))).toBe('system')
  })

  test('survives storage that throws, as Safari private mode does', () => {
    const hostile = {
      getItem() { throw new DOMException('denied') },
      setItem() { throw new DOMException('denied') },
    }
    expect(readStoredPreference(hostile)).toBe('system')
    // The write must not propagate: the theme still applies for this session.
    expect(() => storePreference(hostile, 'dark')).not.toThrow()
  })

  test('treats a missing storage object as system', () => {
    expect(readStoredPreference(null)).toBe('system')
    expect(() => storePreference(null, 'dark')).not.toThrow()
  })

  test('only the three known preferences are accepted', () => {
    expect(isThemePreference('light')).toBe(true)
    expect(isThemePreference('dark')).toBe(true)
    expect(isThemePreference('system')).toBe(true)
    expect(isThemePreference('sepia')).toBe(false)
    expect(isThemePreference(undefined)).toBe(false)
  })
})

describe('applying a theme to the document', () => {
  test('writes data-theme and repaints the browser chrome colour', () => {
    document.head.innerHTML = '<meta name="theme-color" content="#000000" />'

    applyResolvedTheme(document, 'dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    const dark = document.querySelector('meta[name="theme-color"]')?.getAttribute('content')

    applyResolvedTheme(document, 'light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
    const light = document.querySelector('meta[name="theme-color"]')?.getAttribute('content')

    expect(dark).not.toBe(light)
  })

  test('is a no-op without a document rather than throwing', () => {
    expect(() => applyResolvedTheme(null, 'dark')).not.toThrow()
  })
})

/**
 * The inline boot script in index.html cannot import from this module, so the
 * storage key and the media query are written out twice. If they ever drift,
 * the theme resolved before paint stops matching the one React resolves after
 * it, and every load flashes. This is the guard against that.
 */
describe('the boot script agrees with this module', () => {
  // Resolved from the vitest root (web/) rather than from import.meta.url: the
  // jsdom environment reports an http: module URL, which fileURLToPath rejects.
  const html = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8')

  test('uses the same storage key', () => {
    expect(html).toContain(`'${THEME_STORAGE_KEY}'`)
  })

  test('uses the same media query', () => {
    expect(html).toContain(`'${DARK_MEDIA_QUERY}'`)
  })

  test('runs before the module bundle, or it cannot beat first paint', () => {
    expect(html.indexOf('data-theme')).toBeLessThan(html.indexOf('src="/src/main.tsx"'))
  })
})
