import { defineConfig, loadEnv, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { connectPolicy } from './src/lib/connectPolicy.ts'

/**
 * Adds the connect-src half of the Content-Security-Policy to the built
 * index.html as a meta tag, derived from the build's own API and auth origins.
 * See src/lib/connectPolicy.ts for why it is not in vercel.json. Build only: the
 * dev server needs its HMR socket and sends no CSP header anyway.
 */
function connectPolicyMeta(env: Record<string, string>): Plugin {
  return {
    name: 'od-connect-policy-meta',
    apply: 'build',
    transformIndexHtml: {
      order: 'pre',
      handler: () => [
        {
          tag: 'meta',
          attrs: { 'http-equiv': 'Content-Security-Policy', content: connectPolicy(env) },
          injectTo: 'head-prepend',
        },
      ],
    },
  }
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => ({
  plugins: [react(), tailwindcss(), connectPolicyMeta(loadEnv(mode, process.cwd(), 'VITE_'))],
  // NO optimizeDeps OVERRIDE. The icon set was excluded from pre-bundling here
  // for a while, which meant the dev server handed the browser one module
  // request per icon — well over a thousand of them — and the browser journeys
  // that run against that server timed out in bulk, each one screenshotting a
  // page still showing its loading spinner. Vitest needs its own arrangement for
  // the same package and has one in vitest.config.ts; that is the right place
  // for it, because it does not slow the dev server down.
}))
