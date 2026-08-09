import { render, screen } from '@testing-library/react'
import { describe, expect, test } from 'vitest'
import App from './App'

describe('web test harness', () => {
  /**
   * The smoke test: the real App, at the real root, boots to the front door.
   *
   * It used to assert the sign-in heading, because `/` WAS a bare sign-in card.
   * The root now offers a choice instead, so the thing worth smoke-testing is
   * that both options are there — an app that boots to one of them is the
   * friction this page replaced.
   */
  test('renders the front door with both ways in', async () => {
    render(<App />)

    expect(await screen.findByRole('button', { name: /sign in with microsoft/i })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /public dashboard/i })).toBeInTheDocument()
  })
})
