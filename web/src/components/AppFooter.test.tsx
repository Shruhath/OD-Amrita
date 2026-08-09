import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
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
