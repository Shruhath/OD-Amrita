import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useAuth } from '../features/auth/context'
import { sectionsFor } from '../routes/access'
import { AccountMenu } from './AccountMenu'
import { AppFooter } from './AppFooter'
import { ThemeToggle } from './ThemeToggle'

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
  // Only the sections this role can actually open. The bar used to be the same
  // five links for everyone, so a teacher was offered four screens that answer
  // "Access denied" and was not offered the one they can use. The list and the
  // route guards now come from the same declaration, so a link into a refusal is
  // not a thing that can be written.
  const { identity } = useAuth()
  const sections = sectionsFor(identity?.role)

  return (
    <div className="portal-shell">
      <a className="skip-link" href="#workspace">Skip to content</a>

      <header className="topbar">
        {/*
          The brand is a LINK to `/`, which is the one affordance every visitor
          already expects to find and the only way off a screen like the live
          view that is not "sign out". `/` routes by role, so it lands a signed-in
          operator on their own start screen and a signed-out one on the front
          door — where signing in actually lives.
        */}
        <Link className="brand-home" to="/" aria-label="OD-Amrita home">
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">
            OD-Amrita
            <small>Amrita Vishwa Vidyapeetham</small>
          </span>
        </Link>

        <nav className="portal-nav" aria-label="Administration sections">
          {sections.map((section) => (
            <NavLink key={section.to} to={section.to}>{section.label}</NavLink>
          ))}
        </nav>

        <div className="account-actions">
          <ThemeToggle />
          <AccountMenu />
        </div>
      </header>

      <main className="workspace" id="workspace">{children}</main>

      <AppFooter />
    </div>
  )
}
