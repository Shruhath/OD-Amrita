import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { InstallButton, InstallPrompt } from './InstallPrompt'
import { INSTALL_DISMISSED_KEY, type InstallPromptEvent } from '../features/pwa/install'

/**
 * The install offer.
 *
 * The behaviour that matters is what happens when the browser has NOT offered an
 * install — which is most of the time, on most browsers. A banner that renders
 * anyway is a permanent piece of furniture asking for something impossible, so
 * the silence is asserted as hard as the offer.
 */

/** Fires a `beforeinstallprompt` the way Chromium does, with a working prompt(). */
function offerInstall(outcome: 'accepted' | 'dismissed' = 'accepted') {
  const prompt = vi.fn(async () => undefined)
  const event = Object.assign(new Event('beforeinstallprompt', { cancelable: true }), {
    prompt,
    userChoice: Promise.resolve({ outcome }),
  }) as InstallPromptEvent
  window.dispatchEvent(event)
  return { event, prompt }
}

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('install prompt', () => {
  test('[P0] renders nothing until the browser actually offers an install', () => {
    render(<InstallPrompt />)

    expect(
      screen.queryByRole('complementary', { name: /install/i }),
      'a banner shown without an offer asks for something the browser will not do',
    ).not.toBeInTheDocument()
  })

  test('[P0] appears once the browser offers, and installs on request', async () => {
    const user = userEvent.setup()
    render(<InstallPrompt />)

    const { prompt } = offerInstall('accepted')

    const install = await screen.findByRole('button', { name: /^install$/i })
    await user.click(install)

    expect(prompt).toHaveBeenCalledOnce()
  })

  test('[P0] a decline is remembered, so it asks once rather than every visit', async () => {
    const user = userEvent.setup()
    const first = render(<InstallPrompt />)
    offerInstall()

    await user.click(await screen.findByRole('button', { name: /not now/i }))
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /^install$/i })).not.toBeInTheDocument()
    })
    expect(window.localStorage.getItem(INSTALL_DISMISSED_KEY)).toBe('true')
    first.unmount()

    // A fresh visit: the offer arrives again and must stay silent.
    render(<InstallPrompt />)
    offerInstall()
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /^install$/i })).not.toBeInTheDocument()
    })
  })

  test('[P0] the browser prompt is fired at most once per offer', async () => {
    // Chromium refuses a second prompt() on the same event, so a button that
    // stayed live after the first click would silently do nothing.
    const user = userEvent.setup()
    render(<InstallPrompt />)
    const { prompt } = offerInstall('accepted')

    await user.click(await screen.findByRole('button', { name: /^install$/i }))
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /^install$/i })).not.toBeInTheDocument()
    })

    expect(prompt).toHaveBeenCalledOnce()
  })

  test('[P0] the banner disappears when the app reports itself installed', async () => {
    render(<InstallPrompt />)
    offerInstall()
    expect(await screen.findByRole('button', { name: /^install$/i })).toBeInTheDocument()

    window.dispatchEvent(new Event('appinstalled'))

    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /^install$/i })).not.toBeInTheDocument()
    })
  })
})

describe('manual install button', () => {
  test('[P0] stays hidden while there is nothing to install', () => {
    render(<InstallButton />)

    expect(
      screen.queryByRole('button', { name: /install app/i }),
      'a dead install control is worse than none',
    ).not.toBeInTheDocument()
  })

  test('[P0] is the way back for someone who dismissed the banner', async () => {
    const user = userEvent.setup()
    window.localStorage.setItem(INSTALL_DISMISSED_KEY, 'true')
    render(
      <>
        <InstallPrompt />
        <InstallButton />
      </>,
    )

    const { prompt } = offerInstall('accepted')

    // The banner respects the earlier decline...
    expect(screen.queryByRole('button', { name: /not now/i })).not.toBeInTheDocument()
    // ...while the manual control still works.
    await user.click(await screen.findByRole('button', { name: /install app/i }))
    expect(prompt).toHaveBeenCalledOnce()
  })
})
