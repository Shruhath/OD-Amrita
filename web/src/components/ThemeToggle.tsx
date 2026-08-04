import { useTheme } from '../features/theme/store'

/**
 * The light/dark control. One component, placed in every chrome the app has —
 * the portal topbar, the marking header, and the auth cards — so no surface is
 * a dead end for someone who needs the other theme.
 *
 * It is a plain button rather than a `switch`: "on" has no meaning here, and an
 * accessible name that states the destination ("Switch to dark theme") is
 * clearer than a name plus a state a screen reader has to combine. The icon is
 * aria-hidden; the label carries everything.
 */
export function ThemeToggle() {
  const { resolved, toggle } = useTheme()
  const target = resolved === 'dark' ? 'light' : 'dark'
  const label = `Switch to ${target} theme`

  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggle}
      aria-label={label}
      title={label}
      data-theme-state={resolved}
    >
      {resolved === 'dark' ? <SunIcon /> : <MoonIcon />}
    </button>
  )
}

function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false">
      <circle cx="12" cy="12" r="4.2" fill="currentColor" />
      <g stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
        <line x1="12" y1="2.5" x2="12" y2="5" />
        <line x1="12" y1="19" x2="12" y2="21.5" />
        <line x1="2.5" y1="12" x2="5" y2="12" />
        <line x1="19" y1="12" x2="21.5" y2="12" />
        <line x1="5.3" y1="5.3" x2="7.1" y2="7.1" />
        <line x1="16.9" y1="16.9" x2="18.7" y2="18.7" />
        <line x1="5.3" y1="18.7" x2="7.1" y2="16.9" />
        <line x1="16.9" y1="7.1" x2="18.7" y2="5.3" />
      </g>
    </svg>
  )
}

function MoonIcon() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false">
      <path
        d="M20 14.2A8.2 8.2 0 0 1 9.8 4a8.4 8.4 0 1 0 10.2 10.2Z"
        fill="currentColor"
      />
    </svg>
  )
}
