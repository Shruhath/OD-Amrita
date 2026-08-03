import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ThemeToggle } from './ThemeToggle'
import { THEME_STORAGE_KEY } from '../features/theme/theme'

/** jsdom has no matchMedia; every test needs one that reports a known OS value. */
function stubSystemDarkMode(prefersDark: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: prefersDark,
    media: query,
    onchange: null,
    addEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
  return listeners
}

const themeOf = () => document.documentElement.getAttribute('data-theme')

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('theme toggle', () => {
  test('[P1] starts on the operating system preference when nothing is stored', () => {
    stubSystemDarkMode(true)
    render(<ThemeToggle />)
    expect(themeOf()).toBe('dark')
  })

  test('[P1] a stored choice beats the operating system', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'light')
    stubSystemDarkMode(true)
    render(<ThemeToggle />)
    expect(themeOf()).toBe('light')
  })

  test('[P0] one press flips the theme and persists it', async () => {
    stubSystemDarkMode(false)
    render(<ThemeToggle />)
    expect(themeOf()).toBe('light')

    await userEvent.click(screen.getByRole('button', { name: /switch to dark theme/i }))

    expect(themeOf()).toBe('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
    // The label now offers the other direction, so the control is never a
    // one-way door.
    expect(screen.getByRole('button', { name: /switch to light theme/i })).toBeInTheDocument()
  })

  test('[P0] a single press escapes a system-resolved dark, rather than doing nothing', async () => {
    // Stepping the PREFERENCE (system -> light) would leave the screen dark
    // when the OS is dark. The press has to act on what is rendered.
    stubSystemDarkMode(true)
    render(<ThemeToggle />)
    expect(themeOf()).toBe('dark')

    await userEvent.click(screen.getByRole('button', { name: /switch to light theme/i }))

    expect(themeOf()).toBe('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
  })

  test('[P1] follows the OS live while no explicit choice has been made', () => {
    const listeners = stubSystemDarkMode(false)
    render(<ThemeToggle />)
    expect(themeOf()).toBe('light')

    act(() => {
      for (const listener of listeners) listener({ matches: true } as MediaQueryListEvent)
    })

    expect(themeOf()).toBe('dark')
  })

  test('[P1] stops following the OS once the user has chosen', async () => {
    const listeners = stubSystemDarkMode(false)
    render(<ThemeToggle />)

    await userEvent.click(screen.getByRole('button', { name: /switch to dark theme/i }))
    act(() => {
      for (const listener of listeners) listener({ matches: false } as MediaQueryListEvent)
    })

    expect(themeOf()).toBe('dark')
  })

  test('[P2] exposes an accessible name that states the destination', () => {
    stubSystemDarkMode(false)
    render(<ThemeToggle />)
    const button = screen.getByRole('button')
    expect(button).toHaveAccessibleName('Switch to dark theme')
    // The icon must not add a second, competing name.
    expect(button.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
