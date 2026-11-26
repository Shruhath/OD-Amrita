import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
import { DEFAULT_SOURCE_URL, sourceCodeURL } from '../lib/sourceCode'
import { AppFooter } from './AppFooter'

describe('footer contact', () => {
  test('[P0] is labelled Contact, not an internal abbreviation', () => {
    render(<AppFooter />)

    expect(screen.getByText('Contact:')).toBeInTheDocument()
    expect(
      screen.queryByText(/POC/i),
      'POC is project shorthand; the people reading this footer are students',
    ).not.toBeInTheDocument()
  })

  test('[P0] the name is a link that dials', () => {
    render(<AppFooter />)

    const link = screen.getByRole('link', { name: /call shruhath/i })
    expect(link).toHaveTextContent('Shruhath')
    // E.164 with the country code, so it dials from any handset including one
    // roaming — a bare local number does not.
    expect(link).toHaveAttribute('href', 'tel:+917995944735')
  })
})

describe('footer source offer', () => {
  test('[P0] links to the source code of the running app', () => {
    render(<AppFooter />)

    const link = screen.getByRole('link', { name: 'Source code' })
    // The test environment sets no VITE_SOURCE_URL, so this is the default.
    expect(link).toHaveAttribute('href', DEFAULT_SOURCE_URL)
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'))
  })

  test('[P1] defaults to the project repository', () => {
    expect(DEFAULT_SOURCE_URL).toBe('https://github.com/Shruhath/OD-Amrita')
    expect(sourceCodeURL(undefined)).toBe(DEFAULT_SOURCE_URL)
    expect(sourceCodeURL('')).toBe(DEFAULT_SOURCE_URL)
    expect(sourceCodeURL('   ')).toBe(DEFAULT_SOURCE_URL)
  })

  test('[P1] a configured VITE_SOURCE_URL wins', () => {
    expect(sourceCodeURL('https://git.example.edu/od-amrita')).toBe('https://git.example.edu/od-amrita')
  })
})
