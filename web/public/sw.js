/*
 * OD-Amrita service worker.
 *
 * DELIBERATELY MINIMAL. This app has no offline mode — that is a scope decision,
 * not an omission: a mark must never be queued, because the 201 is the only
 * evidence a mark exists, and a service worker that answered an API call from a
 * cache would be inventing attendance. So nothing here touches /api at all.
 *
 * What it does do is make the app installable and survive a dropped connection
 * on NAVIGATION only, which is what turns "no internet" into a page that
 * explains itself instead of the browser's dinosaur.
 *
 * Hashed build assets are not cached here on purpose. Their filenames already
 * change on every build and the host serves them immutable, so the browser's own
 * HTTP cache does this correctly — a second copy in here would only add a way
 * for the two to disagree.
 */

// Bump this to retire every previously cached shell. It is the whole update
// mechanism: on activate, any cache whose name is not this one is deleted.
const VERSION = 'v1'
const SHELL_CACHE = `od-amrita-shell-${VERSION}`
const SHELL_URL = '/index.html'

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL_CACHE)
      .then((cache) => cache.add(new Request(SHELL_URL, { cache: 'reload' })))
      // Take over straight away rather than waiting for every tab to close. A
      // worker that activates on the NEXT visit means a fix ships a day late.
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((name) => name !== SHELL_CACHE).map((name) => caches.delete(name))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const request = event.request

  // Navigations only. Everything else — API calls, assets, anything
  // cross-origin — falls through to the network untouched, which is the whole
  // safety property of this worker.
  if (request.mode !== 'navigate') return

  event.respondWith(
    // NETWORK FIRST, never cache first. A cache-first shell serves the previous
    // deployment's index.html, which points at asset filenames that no longer
    // exist, and the app breaks for everyone until the cache happens to expire.
    fetch(request)
      .then((response) => {
        // Refresh the stored shell from a good response so the offline fallback
        // does not drift a deployment behind.
        if (response && response.ok) {
          const copy = response.clone()
          caches.open(SHELL_CACHE).then((cache) => cache.put(SHELL_URL, copy))
        }
        return response
      })
      .catch(async () => {
        const cached = await caches.match(SHELL_URL)
        if (cached) return cached
        return new Response('You are offline, and this page has not been opened before.', {
          status: 503,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        })
      }),
  )
})
