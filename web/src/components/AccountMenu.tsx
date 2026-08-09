import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useAuth } from '../features/auth/context'
import { initialsFor } from './initials'

/**
 * The account control in the top right: an avatar, and a menu behind it.
 *
 * It shows the AVATAR ONLY. The address it belongs to moves into the menu,
 * because a roll-number address is long, is the same on every screen, and was
 * taking horizontal room from the navigation on exactly the phone-width screens
 * where that room is scarcest. It is one tap away rather than gone.
 *
 * The menu is a real menu: it closes on Escape and on a click outside, and
 * Escape returns focus to the trigger rather than dropping it to the document,
 * which would send the next Tab back to the top of the page.
 */
export function AccountMenu() {
  const { identity, signOut } = useAuth()
  const [open, setOpen] = useState(false)
  const containerRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuID = useId()

  const close = useCallback((returnFocus = false) => {
    setOpen(false)
    if (returnFocus) triggerRef.current?.focus()
  }, [])

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) close()
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') close(true)
    }
    // `pointerdown` rather than `click`: a click listener fires after the button
    // that opened the menu has already toggled it, which reopens it immediately.
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open, close])

  if (!identity) return null

  const email = identity.email
  const label = identity.name?.trim() || email

  return (
    <div className="account-menu" ref={containerRef}>
      <button
        ref={triggerRef}
        className="account-menu__trigger"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuID : undefined}
        // The visible content is initials, which a screen reader would spell
        // out as letters. The accessible name says whose account it is.
        aria-label={`Account: ${label}`}
        onClick={() => setOpen((wasOpen) => !wasOpen)}
      >
        <span className="account-menu__avatar" aria-hidden="true">
          {initialsFor(identity.name, email)}
        </span>
      </button>

      {open ? (
        <div className="account-menu__panel" id={menuID} role="menu">
          {/* Not a menuitem: it is the identity the menu is about, not an
              action. Marking it as one would put a stop on the arrow-key walk
              through the menu that does nothing when activated. */}
          <p className="account-menu__email" title={email}>{email}</p>
          <button
            className="account-menu__item"
            type="button"
            role="menuitem"
            onClick={() => {
              close()
              void signOut()
            }}
          >
            Sign out
          </button>
        </div>
      ) : null}
    </div>
  )
}
