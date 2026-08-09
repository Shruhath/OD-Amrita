import type { ReactNode } from 'react'
import { NavLink } from 'react-router-dom'
import { useAuth } from '../features/auth/context'
import { sectionsFor } from '../routes/access'
import { AccountMenu } from './AccountMenu'
import { AppFooter } from './AppFooter'
import { ThemeToggle } from './ThemeToggle'
import { List, X, CaretRight } from '@phosphor-icons/react'
import { useEffect, useState } from 'react'
import { motion, AnimatePresence } from 'motion/react'

export function PortalShell({ children }: { children: ReactNode }) {
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

      {/* Mobile Header */}
      <header className="topbar portal-topbar">
        <div className="portal-brand">
          <img className="brand-mark" src="/brand/amrita-seal.png" alt="" width={40} height={40} />
          <span className="brand-name">OD-Amrita<small>On-duty records</small></span>
        </div>
        <nav className="portal-nav portal-nav--desktop" aria-label="Administration sections">
          {sections.map((section) => (
            <NavLink key={section.to} to={section.to}>{section.label}</NavLink>
          ))}
        </nav>
        <div className="account-actions">
          <ThemeToggle />
          <button 
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
