import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Application } from '../../src/Application'
import type { SessionPort } from '../../src/features/auth/session'
import { createPortalMemoryRouter } from '../../src/router'
import '../../src/index.css'

type TestRole = 'admin' | 'coordinator' | 'teacher' | 'student' | 'none'

// Must stay in step with the tokens api/test/portal/main.go's fixed verifier
// accepts — an unknown token there is an invalid-token 401, which surfaces here
// as a sign-in screen rather than as the role the spec asked for.
const HARNESS_TOKENS: Record<TestRole, string | null> = {
  admin: 'portal-admin-token',
  coordinator: 'portal-coordinator-token',
  teacher: 'portal-teacher-token',
  student: 'portal-student-token',
  none: null,
}

const query = new URLSearchParams(window.location.search)
const role = (query.get('role') ?? 'admin') as TestRole
const token = HARNESS_TOKENS[role] ?? null

// `role` and `path` are HARNESS controls. Every other query parameter is the
// app's own URL state and has to reach the memory router, or a surface that
// keeps state in the query string cannot be tested here at all — the public
// dashboard puts the selected student in `?student=<uuid>`, and without this
// split its deep-link and reload journeys would fail for a harness reason
// rather than a product one.
const HARNESS_PARAMS = ['role', 'path']

function appSearch(source: URLSearchParams): string {
  const params = new URLSearchParams(source)
  for (const name of HARNESS_PARAMS) params.delete(name)
  const encoded = params.toString()
  return encoded === '' ? '' : `?${encoded}`
}

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
const router = createPortalMemoryRouter([(query.get('path') ?? '/portal/events') + appSearch(query)])
router.subscribe((state) => {
  // Mirror BOTH halves of the app's location. Mirroring only the pathname drops
  // the query string, so a reload rebuilt the initial entry without it and any
  // query-string state silently vanished across the reload.
  const next = new URLSearchParams(window.location.search)
  for (const name of [...next.keys()]) {
    if (!HARNESS_PARAMS.includes(name)) next.delete(name)
  }
  next.set('path', state.location.pathname)
  for (const [name, value] of new URLSearchParams(state.location.search)) {
    next.set(name, value)
  }
  window.history.replaceState(null, '', `${window.location.pathname}?${next.toString()}`)
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Application session={session} router={router} />
  </StrictMode>,
)
