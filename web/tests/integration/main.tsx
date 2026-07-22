import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Application } from '../../src/Application'
import type { SessionPort } from '../../src/features/auth/session'
import { createPortalMemoryRouter } from '../../src/router'
import '../../src/index.css'

type TestRole = 'admin' | 'student' | 'none'

const query = new URLSearchParams(window.location.search)
const role = (query.get('role') ?? 'admin') as TestRole
const token = role === 'admin' ? 'portal-admin-token' : role === 'student' ? 'portal-student-token' : null

const session: SessionPort = {
  async accessToken() { return token },
  async refresh() { return token },
  async signIn() {},
  async signOut() {},
  subscribe() { return () => undefined },
}

// This harness is served from one static entry point, so the app runs on a
// MEMORY router and the browser URL never changes on its own. Mirroring the
// in-memory location into a `path` query parameter is what makes a reload a real
// persistence oracle for a deep route: without it, reloading the event detail
// view would silently drop back to the events list and the assertion after the
// reload would be testing the wrong page.
const router = createPortalMemoryRouter([query.get('path') ?? '/portal/events'])
router.subscribe((state) => {
  const next = new URLSearchParams(window.location.search)
  next.set('path', state.location.pathname)
  window.history.replaceState(null, '', `${window.location.pathname}?${next.toString()}`)
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Application session={session} router={router} />
  </StrictMode>,
)
