import { beforeEach, describe, expect, test, vi } from 'vitest'
import { loadRouteModule } from './lazyRoute'

/**
 * Opening a screen on a phone that has just woken.
 *
 * Every screen here is code-split, so opening one is a network request, and the
 * radio is routinely not up in the second after the phone is unlocked. React's
 * `lazy` caches the REJECTED promise, so a single bad moment used to break the
 * route permanently — "Failed to fetch dynamically imported module", with a
 * manual refresh as the only way out. That was reaching real users.
 */

const options = { delayMs: () => 0 }

beforeEach(() => {
  window.sessionStorage.clear()
})

describe('loading a route module', () => {
  test('[P0] a module that loads first time is returned unchanged', async () => {
    const load = vi.fn(async () => ({ default: 'screen' }))

    await expect(loadRouteModule(load, options)).resolves.toEqual({ default: 'screen' })
    expect(load).toHaveBeenCalledOnce()
  })

  test('[P0] a connection that was not ready yet is retried, not surfaced', async () => {
    // The observed failure. One attempt fails, the next succeeds a moment later.
    const load = vi
      .fn<() => Promise<{ default: string }>>()
      .mockRejectedValueOnce(new TypeError('Failed to fetch dynamically imported module'))
      .mockResolvedValueOnce({ default: 'screen' })

    await expect(loadRouteModule(load, options)).resolves.toEqual({ default: 'screen' })
    expect(load).toHaveBeenCalledTimes(2)
  })

  test('[P0] it keeps trying for a few attempts before giving up', async () => {
    const load = vi
      .fn<() => Promise<{ default: string }>>()
      .mockRejectedValueOnce(new TypeError('boom'))
      .mockRejectedValueOnce(new TypeError('boom'))
      .mockResolvedValueOnce({ default: 'screen' })

    await expect(loadRouteModule(load, options)).resolves.toEqual({ default: 'screen' })
    expect(load).toHaveBeenCalledTimes(3)
  })

  test('[P0] a chunk that never loads reloads the page once', async () => {
    // The other real cause: a tab left open across a deployment is asking for a
    // filename from the previous build. No retry can fix that — only a fresh
    // document can, which is exactly what a manual refresh was doing by hand.
    const reload = vi.fn()
    const load = vi.fn(async () => {
      throw new TypeError('Failed to fetch dynamically imported module')
    })

    const pending = loadRouteModule(load, { ...options, reload })
    await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce())

    // It must not settle: rendering an error for the instant before the page
    // goes away is a flash of failure on a screen that is about to recover.
    let settled = false
    void pending.then(() => { settled = true }, () => { settled = true })
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(settled).toBe(false)
  })

  test('[P0] it never reloads twice, so a broken screen cannot loop', async () => {
    // A device that is genuinely offline fails every attempt. Reloading each
    // time would spin the phone in a loop during an event, which is far worse
    // than an error page.
    const reload = vi.fn()
    const load = vi.fn(async () => {
      throw new TypeError('offline')
    })

    void loadRouteModule(load, { ...options, reload })
    await vi.waitFor(() => expect(reload).toHaveBeenCalledOnce())

    await expect(loadRouteModule(load, { ...options, reload })).rejects.toThrow('offline')
    expect(reload).toHaveBeenCalledOnce()
  })

  test('[P0] the original error survives to the error screen', async () => {
    // Once the reload has been spent, the failure has to be reportable — an
    // operator during an event needs something to quote.
    window.sessionStorage.setItem('od-amrita.chunk-reload-at', String(Date.now()))
    const load = vi.fn(async () => {
      throw new TypeError('Failed to fetch dynamically imported module: /assets/MarkScreen-abc.js')
    })

    await expect(loadRouteModule(load, options)).rejects.toThrow(/MarkScreen-abc\.js/)
  })
})
