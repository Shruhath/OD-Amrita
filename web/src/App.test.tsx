import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
import App from './App'

describe('web test harness', () => {
  test('renders the current application shell', () => {
    render(<App />)

    expect(screen.getByRole('heading', { name: 'OD-Amrita' })).toBeInTheDocument()
  })
})
