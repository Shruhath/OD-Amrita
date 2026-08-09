import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  // NO optimizeDeps OVERRIDE. The icon set was excluded from pre-bundling here
  // for a while, which meant the dev server handed the browser one module
  // request per icon — well over a thousand of them — and the browser journeys
  // that run against that server timed out in bulk, each one screenshotting a
  // page still showing its loading spinner. Vitest needs its own arrangement for
  // the same package and has one in vitest.config.ts; that is the right place
  // for it, because it does not slow the dev server down.
})
