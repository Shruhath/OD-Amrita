import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
import { LoadingScreen, Spinner } from './Spinner'

/**
 * The loading indicator.
 *
 * The visible change is that a sentence became a spinning ring. The change that
 * is easy to get wrong is the invisible one: replacing text with an animation
 * removes the only thing a screen reader had to announce, so these tests exist
 * to keep the words present while they are no longer seen.
 */

describe('spinner', () => {
  test('[P0] is a live status region, so a wait is announced rather than silent', () => {
    render(<Spinner label="Checking your session" />)

    expect(screen.getByRole('status')).toHaveTextContent('Checking your session')
  })

  test('[P0] the moving part is hidden from assistive technology', () => {
    // Announcing a decorative ring alongside the label would read the wait out
    // twice, once meaninglessly.
    const { container } = render(<Spinner label="Loading" />)

    expect(container.querySelector('.spinner__ring')).toHaveAttribute('aria-hidden', 'true')
  })

  test('[P0] the label is present in the DOM but not laid out visually', () => {
    const { container } = render(<Spinner label="Loading events workspace" />)

    const label = screen.getByText('Loading events workspace')
    expect(label).toBeInTheDocument()
    expect(label.className).toContain('sr-only')
    expect(container.textContent).toBe('Loading events workspace')
  })

  test('[P0] the full-screen version keeps the same announcement', () => {
    render(<LoadingScreen label="Checking your session" />)

    expect(screen.getByRole('status')).toHaveTextContent('Checking your session')
  })
})
