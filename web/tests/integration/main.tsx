import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Application } from '../../src/Application'
import type { SessionPort } from '../../src/features/auth/session'
import { createPortalMemoryRouter } from '../../src/router'
import '../../src/index.css'

type TestRole = 'admin' | 'student' | 'none'

const role = (new URLSearchParams(window.location.search).get('role') ?? 'admin') as TestRole
const token = role === 'admin' ? 'portal-admin-token' : role === 'student' ? 'portal-student-token' : null

const session: SessionPort = {
  async accessToken() { return token },
  async refresh() { return token },
  async signIn() {},
  async signOut() {},
  subscribe() { return () => undefined },
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Application session={session} router={createPortalMemoryRouter()} />
  </StrictMode>,
)
