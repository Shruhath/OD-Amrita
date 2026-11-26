import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import { connectPolicy, connectSources } from './lib/connectPolicy'

/**
 * The Content-Security-Policy in vercel.json, checked against the files it has
 * to agree with.
 *
 * WHY THIS EXISTS. The policy allows the inline theme script in index.html by
 * its SHA-256 hash, not by 'unsafe-inline'. Editing that script by so much as a
 * space changes the hash, and the browser then refuses to run it — silently, on
 * production only, because the dev server sends no CSP. The theme would flash on
 * every load and nothing in the build would fail. This test is what fails.
 *
 * Session tokens live in localStorage, so the policy is also the main thing
 * standing between an injected script and those tokens. The checks below keep it
 * enforcing and keep the script and connection lists tight.
 */

// Resolved from the vitest root (web/), as theme.test.ts does: jsdom reports an
// http: module URL, which fileURLToPath rejects.
const read = (file: string) => readFileSync(resolve(process.cwd(), file), 'utf8')

type HeaderRule = { source: string; headers: { key: string; value: string }[] }

const vercel = JSON.parse(read('vercel.json')) as { headers: HeaderRule[] }
const siteWide = vercel.headers.find((rule) => rule.source === '/(.*)')
const header = (key: string) => siteWide?.headers.find((entry) => entry.key.toLowerCase() === key.toLowerCase())?.value

/** Directive name -> its source list. */
function directives(policy: string): Map<string, string[]> {
  const parsed = new Map<string, string[]>()
  for (const part of policy.split(';')) {
    const [name, ...sources] = part.trim().split(/\s+/)
    if (name) parsed.set(name.toLowerCase(), sources)
  }
  return parsed
}

/** Every inline <script> body in index.html, exactly as the browser hashes it. */
function inlineScripts(html: string): string[] {
  return [...html.matchAll(/<script\b([^>]*)>([\s\S]*?)<\/script>/gi)]
    .filter(([, attributes]) => !/\bsrc\s*=/i.test(attributes))
    .map(([, , body]) => body)
}

const sha256 = (body: string) => `'sha256-${createHash('sha256').update(body, 'utf8').digest('base64')}'`

const policy = header('Content-Security-Policy') ?? ''
const csp = directives(policy)

describe('content security policy', () => {
  test('[P0] is sent site-wide and enforced, not report-only', () => {
    expect(siteWide, 'vercel.json must carry a "/(.*)" header rule').toBeDefined()
    expect(policy).not.toBe('')
    expect(header('Content-Security-Policy-Report-Only')).toBeUndefined()
  })

  test('[P0] allows every inline script in index.html by its hash', () => {
    const scripts = inlineScripts(read('index.html'))
    expect(scripts.length, 'index.html is expected to carry the inline theme script').toBeGreaterThan(0)

    const allowed = csp.get('script-src') ?? []
    for (const body of scripts) {
      expect(
        allowed,
        `index.html changed an inline script; put ${sha256(body)} in script-src in web/vercel.json`,
      ).toContain(sha256(body))
    }
  })

  test('[P0] carries no hash for a script that no longer exists', () => {
    const current = new Set(inlineScripts(read('index.html')).map(sha256))
    const stale = (csp.get('script-src') ?? []).filter((source) => source.startsWith("'sha256-") && !current.has(source))
    expect(stale, 'remove hashes for inline scripts that were deleted or edited').toEqual([])
  })

  test('[P0] never allows arbitrary inline or evaluated script', () => {
    const scripts = csp.get('script-src') ?? []
    expect(scripts).not.toContain("'unsafe-inline'")
    expect(scripts).not.toContain("'unsafe-eval'")
    expect(scripts).not.toContain('*')
  })

  test('[P0] restricts every fetch directive that has no build-time half', () => {
    for (const directive of ['script-src', 'style-src', 'img-src', 'font-src', 'media-src', 'worker-src', 'manifest-src', 'frame-src']) {
      expect(csp.get(directive), `${directive} must be set in web/vercel.json`).toBeDefined()
      expect(csp.get(directive)).not.toContain('*')
    }
  })

  test('[P0] keeps the framing, base and plugin lockdown', () => {
    expect(csp.get('frame-ancestors')).toEqual(["'none'"])
    expect(csp.get('base-uri')).toEqual(["'self'"])
    expect(csp.get('object-src')).toEqual(["'none'"])
    expect(csp.get('form-action')).toEqual(["'self'"])
  })

  test('[P0] names no deployment origin: connect-src comes from the build', () => {
    // A default-src or connect-src here would also bind the build-time meta
    // policy (the browser enforces both), so either one would block the API.
    expect(csp.get('default-src')).toBeUndefined()
    expect(csp.get('connect-src')).toBeUndefined()
    expect(policy).not.toMatch(/https?:\/\//)
  })
})

describe('build-time connect policy', () => {
  test('[P0] allows the configured API and auth origins and nothing else', () => {
    expect(
      connectSources({
        VITE_API_BASE_URL: 'https://api.example.test/some/path',
        VITE_SUPABASE_URL: 'https://project-ref.supabase.co',
      }),
    ).toEqual(["'self'", 'https://api.example.test', 'https://project-ref.supabase.co', 'wss://project-ref.supabase.co'])
  })

  test('[P0] falls back to the page origin alone when nothing is configured', () => {
    expect(connectPolicy({})).toBe("connect-src 'self'")
  })

  test('[P0] refuses a value that is not an http(s) URL rather than emitting a broken policy', () => {
    expect(() => connectSources({ VITE_API_BASE_URL: 'api.example.test' })).toThrow()
    expect(() => connectSources({ VITE_SUPABASE_URL: 'javascript:alert(1)' })).toThrow()
  })

  test('[P0] covers the production API named in .env.production', () => {
    const apiBase = /^VITE_API_BASE_URL=(.+)$/m.exec(read('.env.production'))?.[1]?.trim()
    expect(apiBase, '.env.production must name the API').toBeTruthy()
    expect(connectSources({ VITE_API_BASE_URL: apiBase })).toContain(new URL(apiBase as string).origin)
  })
})

describe('permissions policy', () => {
  test('[P0] keeps the camera for barcode scanning on /mark', () => {
    expect(header('Permissions-Policy')).toMatch(/\bcamera=\(self\)/)
  })
})
