import { useCallback, useEffect, useState } from 'react'

/**
 * The event Chromium fires when it is willing to install the app.
 *
 * Typed here rather than imported: `BeforeInstallPromptEvent` is not in the DOM
 * lib because it is not a standard — it exists in Chromium and nowhere else,
 * which is the same reason the iOS branch below has to exist at all.
 */
export type InstallPromptEvent = Event & {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

/** Remembers a decline, so the banner asks once rather than on every visit. */
export const INSTALL_DISMISSED_KEY = 'od-amrita-install-dismissed'

export type InstallState = {
  /** True when the browser has offered a prompt we are holding. */
  canInstall: boolean
  /** True when the app is already running as an installed app. */
  isInstalled: boolean
  /** True on a browser that can install but gives us no prompt to fire (iOS). */
  needsManualInstructions: boolean
  /** True when the banner should be on screen: installable and not yet declined. */
  shouldOfferInstall: boolean
  /** Fires the held prompt. Resolves to what the visitor chose. */
  promptInstall: () => Promise<'accepted' | 'dismissed' | 'unavailable'>
  /** Records a decline so the banner stops asking. */
  dismissInstall: () => void
}

function readDismissed(): boolean {
  try {
    return window.localStorage.getItem(INSTALL_DISMISSED_KEY) === 'true'
  } catch {
    // Safari in private mode throws on localStorage. A visitor who cannot be
    // remembered should still be asked, not silently excluded.
    return false
  }
}

/** Already installed? Standalone display is the reliable signal on both families. */
export function isRunningInstalled(): boolean {
  if (typeof window === 'undefined') return false
  const standalone = window.matchMedia?.('(display-mode: standalone)')?.matches
  // iOS Safari does not implement the display-mode query for home-screen apps
  // and sets this non-standard flag instead.
  const iosStandalone = (window.navigator as Navigator & { standalone?: boolean }).standalone
  return Boolean(standalone || iosStandalone)
}

/** iOS offers no install prompt at all — only Share → Add to Home Screen. */
export function isIOS(): boolean {
  if (typeof window === 'undefined') return false
  const ua = window.navigator.userAgent
  // iPadOS reports itself as a Mac, so the touch-point check is what catches it.
  const iPadOS = /Macintosh/.test(ua) && window.navigator.maxTouchPoints > 1
  return /iPad|iPhone|iPod/.test(ua) || iPadOS
}

/**
 * Holds the browser's install offer and exposes it to the UI.
 *
 * The offer has to be CAUGHT AND KEPT: `beforeinstallprompt` fires once, early,
 * and the prompt can only be shown from a user gesture later — so a component
 * that is not listening at load time can never install the app afterwards. That
 * is why this listens from mount and stores the event rather than asking for it
 * when a button is clicked.
 */
export function useInstallPrompt(): InstallState {
  const [offer, setOffer] = useState<InstallPromptEvent | null>(null)
  const [installed, setInstalled] = useState(() => isRunningInstalled())
  const [dismissed, setDismissed] = useState(() => readDismissed())

  useEffect(() => {
    const onBeforeInstallPrompt = (event: Event) => {
      // Without this Chromium shows its own mini-infobar and our banner would be
      // the second thing asking the same question.
      event.preventDefault()
      setOffer(event as InstallPromptEvent)
    }
    const onInstalled = () => {
      setInstalled(true)
      setOffer(null)
    }
    window.addEventListener('beforeinstallprompt', onBeforeInstallPrompt)
    window.addEventListener('appinstalled', onInstalled)
    return () => {
      window.removeEventListener('beforeinstallprompt', onBeforeInstallPrompt)
      window.removeEventListener('appinstalled', onInstalled)
    }
  }, [])

  const promptInstall = useCallback(async () => {
    if (!offer) return 'unavailable' as const
    await offer.prompt()
    const { outcome } = await offer.userChoice
    // The event is single-use: Chromium refuses a second prompt() on the same
    // one, so holding it would leave a button that silently does nothing.
    setOffer(null)
    if (outcome === 'dismissed') {
      // A decline at the browser's own dialog is still a decline.
      try {
        window.localStorage.setItem(INSTALL_DISMISSED_KEY, 'true')
      } catch {
        // Not being able to remember is not a reason to fail the install flow.
      }
      setDismissed(true)
    }
    return outcome
  }, [offer])

  const dismissInstall = useCallback(() => {
    try {
      window.localStorage.setItem(INSTALL_DISMISSED_KEY, 'true')
    } catch {
      // As above.
    }
    setDismissed(true)
  }, [])

  const needsManualInstructions = !installed && !offer && isIOS()

  return {
    canInstall: Boolean(offer),
    isInstalled: installed,
    needsManualInstructions,
    shouldOfferInstall: !installed && !dismissed && (Boolean(offer) || needsManualInstructions),
    promptInstall,
    dismissInstall,
  }
}
