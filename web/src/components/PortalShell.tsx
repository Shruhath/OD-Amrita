import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { useAuth } from '../features/auth/context'
import { AppFooter } from './AppFooter'
import { ThemeToggle } from './ThemeToggle'

const sections = [
  { to: '/portal/events', label: 'Events' },
  { to: '/portal/disputes', label: 'Disputes' },
  { to: '/portal/teachers', label: 'Teachers' },
  { to: '/portal/students', label: 'Students' },
  { to: '/portal/card-populator', label: 'Card populator' },
]

/**
 * The chrome every administration screen sits inside: university mark, the
 * section links, the signed-in account, and the footer.
 *
 * This markup was copy-pasted across all five portal screens before, which is
 * how the event detail page ended up as the only one with no way to navigate
 * anywhere except back to the list. NavLink derives `aria-current` from the
 * route, so the active section can no longer drift out of sync either.
 */
export function PortalShell({ children }: { children: ReactNode }) {
  const { identity, signOut } = useAuth()

  return (
    <div className="portal-shell">
      <a className="skip-link" href="#workspace">Skip to content</a>

      <header className="topbar">
        <div>
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">
            OD-Amrita
            <small>Amrita Vishwa Vidyapeetham</small>
          </span>
        </div>

        <nav className="portal-nav" aria-label="Administration sections">
          {sections.map((section) => (
            <NavLink key={section.to} to={section.to}>{section.label}</NavLink>
          ))}
        </nav>

        <div className="account-actions">
          <span>{identity?.name ?? identity?.email}</span>
          <ThemeToggle />
          <button className="text-button" type="button" onClick={() => void signOut()}>Sign out</button>
        </div>
      </header>

      <main className="workspace" id="workspace">{children}</main>

      <AppFooter />
    </div>
  )
}
