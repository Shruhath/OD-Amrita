import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
import App from './App'

describe('web test harness', () => {
  test('renders the authentication boundary', async () => {
    render(<App />)

    expect(await screen.findByRole('heading', { name: 'Sign in to OD-Amrita' })).toBeInTheDocument()
  })
})
