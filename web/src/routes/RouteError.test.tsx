import { render, screen } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router-dom'
import { describe, expect, test, vi } from 'vitest'
import RouteError from './RouteError'
import { createPortalMemoryRouter } from '../router'

/**
 * What a person sees when a screen fails to open.
 *
 * React Router's own fallback — "Unexpected Application Error!", followed by
 * "Hey developer 👋" and advice about adding an ErrorBoundary — was reaching
 * STUDENTS on the public dashboard. It names a JavaScript filename and offers
 * nothing to do about it.
 */

function renderFailingRoute(error: Error) {
  const router = createMemoryRouter(
    [
      {
        path: '/',
        Component: () => {
          throw error
        },
        errorElement: <RouteError />,
      },
    ],
    { initialEntries: ['/'] },
  )
  // React logs the thrown error; silenced so the run stays readable.
  const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
  const view = render(<RouterProvider router={router} />)
  consoleError.mockRestore()
  return view
}

describe('route error screen', () => {
  test('[P0] a failed screen load reads as a connection problem, not a crash', async () => {
    renderFailingRoute(new TypeError('Failed to fetch dynamically imported module: /assets/MarkScreen-abc.js'))

    expect(await screen.findByRole('heading', { name: /did not finish loading/i })).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(/check your connection/i)
    // The reassurance matters: an operator mid-event needs to know a mark was
    // not silently lost.
    expect(screen.getByRole('alert')).toHaveTextContent(/nothing you did has been lost/i)
  })

  test('[P0] it never shows React Router\'s developer fallback', async () => {
    renderFailingRoute(new TypeError('Failed to fetch dynamically imported module'))

    expect(screen.queryByText(/hey developer/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/ErrorBoundary/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Unexpected Application Error/i)).not.toBeInTheDocument()
  })

  test('[P0] there is always a way out', async () => {
    renderFailingRoute(new Error('something else entirely'))

    expect(await screen.findByRole('button', { name: /try again/i })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /something went wrong/i })).toBeInTheDocument()
  })

  test('[P0] the detail is kept for reporting, but is not the headline', async () => {
    renderFailingRoute(new TypeError('Failed to fetch dynamically imported module: /assets/MarkScreen-abc.js'))

    const headings = await screen.findAllByRole('heading')
    expect(headings[0].textContent).not.toMatch(/MarkScreen-abc/)
    expect(screen.getByText(/MarkScreen-abc\.js/)).toBeInTheDocument()
  })

  test('[P0] every route in the application carries the guard', () => {
    // Applied by mapping over the route table rather than written into each
    // entry, so a route added later cannot be the one that forgets — this holds
    // that property.
    const router = createPortalMemoryRouter(['/attendance'])
    const withoutGuard = router.routes.filter((route) => !route.errorElement)

    expect(
      withoutGuard.map((route) => route.path ?? '(index)'),
      'a route with no error element falls back to the developer error page',
    ).toEqual([])
    expect(router.routes.length).toBeGreaterThan(5)
  })
})
