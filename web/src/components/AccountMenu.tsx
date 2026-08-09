import { useAuth } from '../features/auth/context'
import { initialsFor } from './initials'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from './ui/dropdown-menu'

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
  if (!identity) return null

  const email = identity.email
  const label = identity.name?.trim() || email

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <button className="account-menu__trigger" type="button"
        // The visible content is initials, which a screen reader would spell
        // out as letters. The accessible name says whose account it is.
        aria-label={`Account: ${label}`}
      >
        <span className="account-menu__avatar" aria-hidden="true">
          {initialsFor(identity.name, email)}
        </span>
        </button>
      </DropdownMenuTrigger>

      <DropdownMenuContent side="left" align="start" sideOffset={10} collisionPadding={12} avoidCollisions={false} className="account-menu__panel">
        <DropdownMenuLabel className="account-menu__email" title={email}>{email}</DropdownMenuLabel>
        <DropdownMenuItem className="account-menu__item" onSelect={() => void signOut()}>Sign out</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
