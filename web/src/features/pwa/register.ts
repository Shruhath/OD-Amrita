/**
 * Registers the service worker.
 *
 * Its only jobs are making the app installable and turning a dropped connection
 * on a navigation into a page rather than the browser's error. It deliberately
 * does not touch API traffic — see public/sw.js.
 *
 * Registered after `load` so it never competes with the first paint for
 * bandwidth on a phone on campus wifi. Failure is logged and ignored: an app
 * that will not start because a worker did not register is strictly worse than
 * one that simply is not installable.
 */
export function registerServiceWorker() {
  if (typeof window === 'undefined') return
  if (!('serviceWorker' in navigator)) return
  // Vite serves public/ in dev too, but a worker caching the dev shell fights
  // hot reload for no benefit — the install offer is a production concern.
  if (import.meta.env.DEV) return

  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch((error) => {
      console.warn('service worker registration failed; the app is not installable', error)
    })
  })
}
