import type { ReactNode } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useAuth } from '../features/auth/context'
import { sectionsFor } from '../routes/access'
import { AccountMenu } from './AccountMenu'
import { AppFooter } from './AppFooter'
import { ThemeToggle } from './ThemeToggle'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { List } from '@phosphor-icons/react/dist/icons/List'
import { X } from '@phosphor-icons/react/dist/icons/X'
import { CaretRight } from '@phosphor-icons/react/dist/icons/CaretRight'
import { useEffect, useState } from 'react'
import { motion, AnimatePresence } from 'motion/react'

/**
 * The chrome every administration screen sits inside: university mark, the
 * section links, the signed-in account, and the footer.
 *
 * This markup was copy-pasted across all five portal screens before, which is
 * how the event detail page ended up as the only one with no way to navigate
 * anywhere except back to the list. NavLink derives `aria-current` from the
 * route, so the active section can no longer drift out of sync either.
 *
 * Below 768px the section rail collapses into a sheet behind one toggle. The
 * rail used to stay a horizontal row of five links on a 360px screen, which
 * either overflowed the header or squeezed the labels to unreadable — and the
 * account menu went with it. Everything the rail offered is still reachable
 * from the sheet, including the account, so the phone is not a reduced surface.
 */
export function PortalShell({ children }: { children: ReactNode }) {
  // Only the sections this role can actually open. The bar used to be the same
  // five links for everyone, so a teacher was offered four screens that answer
  // "Access denied" and was not offered the one they can use. The list and the
  // route guards now come from the same declaration, so a link into a refusal is
  // not a thing that can be written.
  const { identity } = useAuth()
  const sections = sectionsFor(identity?.role)
  const [navOpen, setNavOpen] = useState(false)

  useEffect(() => {
    if (!navOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setNavOpen(false)
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [navOpen])

  return (
    <div className="portal-shell">
      <a className="skip-link" href="#workspace">
        Skip to content
      </a>

      <header className="topbar portal-topbar">
        {/*
          The brand is a LINK to `/`, which is the one affordance every visitor
          already expects to find and the only way off a screen like the live
          view that is not "sign out". `/` routes by role, so it lands a signed-in
          operator on their own start screen and a signed-out one on the front
          door — where signing in actually lives.
        */}
        <Link className="brand-home portal-brand" to="/" aria-label="OD-Amrita home">
          <span className="brand-mark" aria-hidden="true">OD</span>
          <span className="brand-name">
            OD-Amrita
            <small>Amrita Vishwa Vidyapeetham</small>
          </span>
        </Link>

        <nav className="portal-nav portal-nav--desktop" aria-label="Administration sections">
          {sections.map((section) => (
            <NavLink key={section.to} to={section.to}>{section.label}</NavLink>
          ))}
        </nav>
        <div className="account-actions">
          <ThemeToggle />
          <button
            type="button"
            onClick={() => setNavOpen(!navOpen)}
            className="nav-toggle"
            aria-label={navOpen ? 'Close navigation' : 'Open navigation'}
            aria-expanded={navOpen}
          >
            {navOpen ? <X size={24} weight="bold" /> : <List size={24} weight="bold" />}
          </button>
          <div className="account-desktop"><AccountMenu /></div>
        </div>
      </header>

      {/* Mobile Navigation Sheet */}
      <AnimatePresence>
        {navOpen && (
          <motion.div
            initial={{ opacity: 0, y: -20 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -20 }}
            transition={{ type: "spring", damping: 20, stiffness: 300 }}
            className="mobile-nav"
            role="dialog"
            aria-modal="true"
            aria-label="Navigation menu"
          >
            <div className="mobile-nav__heading">
              <span>Workspace</span>
              <button type="button" className="text-button" onClick={() => setNavOpen(false)}>Close</button>
            </div>
            {sections.map((section) => (
              <NavLink 
                key={section.to} 
                to={section.to}
                onClick={() => setNavOpen(false)}
                className={({ isActive }) => isActive ? 'mobile-nav__link is-active' : 'mobile-nav__link'}
              >
                {section.label}
                <CaretRight size={18} aria-hidden="true" />
              </NavLink>
            ))}
            <div className="mobile-nav__account">
              <span className="text-sm font-medium text-foreground-muted">Account</span>
              <AccountMenu />
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      <main className="workspace" id="workspace">
        {children}
      </main>

      <AppFooter />
    </div>
  )
}
