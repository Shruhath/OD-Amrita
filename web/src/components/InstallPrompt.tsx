import { useInstallPrompt } from '../features/pwa/install'

/**
 * The install offer, shown once per visitor until they decline.
 *
 * It renders NOTHING at all unless the browser has actually offered an install —
 * so it is silent in an already-installed app, on a desktop browser that will
 * not install, and on a second visit after a decline. That is why it can sit at
 * the application root instead of being wired into one screen: it costs an empty
 * render everywhere else.
 *
 * iOS gets words instead of a button, because Safari exposes no install API at
 * all. Pretending otherwise would mean a button that does nothing on a large
 * share of student phones.
 */
export function InstallPrompt() {
  const install = useInstallPrompt()

  if (!install.shouldOfferInstall) return null

  return (
    <aside className="install-prompt" role="complementary" aria-label="Install OD-Amrita">
      <img className="install-prompt__icon" src="/icons/icon-192.png" alt="" width={48} height={48} />
      <div className="install-prompt__body">
        <p className="install-prompt__title">Install OD-Amrita</p>
        {install.needsManualInstructions ? (
          <p className="install-prompt__text">
            Tap <strong>Share</strong>, then <strong>Add to Home Screen</strong>.
          </p>
        ) : (
          <p className="install-prompt__text">Add it to your home screen for quicker access.</p>
        )}
      </div>
      <div className="install-prompt__actions">
        {install.canInstall ? (
          <button className="primary" type="button" onClick={() => void install.promptInstall()}>
            Install
          </button>
        ) : null}
        <button type="button" onClick={install.dismissInstall}>
          Not now
        </button>
      </div>
    </aside>
  )
}

/**
 * The MANUAL entry point, for someone who declined and changed their mind.
 *
 * Separate from the banner because it answers a different question: the banner
 * asks, this one waits to be asked. It hides once the app is installed — an
 * install button inside an installed app is a dead control.
 */
export function InstallButton({ className }: { className?: string }) {
  const install = useInstallPrompt()

  if (install.isInstalled) return null
  if (!install.canInstall && !install.needsManualInstructions) return null

  if (install.needsManualInstructions) {
    return (
      <span className={className}>
        To install: <strong>Share</strong> → <strong>Add to Home Screen</strong>
      </span>
    )
  }

  return (
    <button className={className} type="button" onClick={() => void install.promptInstall()}>
      Install app
    </button>
  )
}
