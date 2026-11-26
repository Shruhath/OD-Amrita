/**
 * The build-time half of the Content-Security-Policy: which origins the page may
 * open connections to.
 *
 * WHY IT IS SPLIT FROM vercel.json. The header policy in vercel.json is generic
 * and committed; it must not name a deployment's API or auth origin, because
 * every fork and environment has different ones. Those origins are already build
 * configuration (VITE_API_BASE_URL, VITE_SUPABASE_URL), so the build turns them
 * into a `<meta http-equiv="Content-Security-Policy">` carrying `connect-src`.
 * A browser enforces every policy it receives, so the header and the meta tag
 * together allow only what both allow.
 *
 * Kept free of Vite and DOM imports so vite.config.ts and the unit test can both
 * load it.
 */

export type ConnectPolicyEnv = {
  VITE_API_BASE_URL?: string
  VITE_SUPABASE_URL?: string
}

function originOf(value: string | undefined): URL | undefined {
  const trimmed = value?.trim()
  if (!trimmed) return undefined
  let url: URL
  try {
    url = new URL(trimmed)
  } catch {
    throw new Error(`Content-Security-Policy: "${trimmed}" is not an absolute URL`)
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new Error(`Content-Security-Policy: "${trimmed}" must be an http(s) URL`)
  }
  return url
}

/** The connect-src source list for a build, deduplicated, 'self' first. */
export function connectSources(env: ConnectPolicyEnv): string[] {
  const sources = ["'self'"]
  const api = originOf(env.VITE_API_BASE_URL)
  if (api) sources.push(api.origin)
  const auth = originOf(env.VITE_SUPABASE_URL)
  if (auth) {
    sources.push(auth.origin)
    // supabase-js speaks to the same host over a WebSocket for realtime.
    sources.push(`${auth.protocol === 'https:' ? 'wss:' : 'ws:'}//${auth.host}`)
  }
  return [...new Set(sources)]
}

/** The full meta-tag policy for a build. */
export function connectPolicy(env: ConnectPolicyEnv): string {
  return `connect-src ${connectSources(env).join(' ')}`
}
