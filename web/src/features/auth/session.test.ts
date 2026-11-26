import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

/**
 * The browser session's sign-out, against a stubbed Supabase client.
 *
 * The defect these pin: Supabase keeps the local session whenever its sign-out
 * request fails, so a dropped connection used to leave a shared phone signed in
 * to the previous coordinator. The device must end up signed out regardless of
 * what the network did, and the caller must still be told it did not go cleanly.
 */

const signOut = vi.hoisted(() => vi.fn())

vi.mock('../../lib/supabase/client', () => ({
  SUPABASE_STORAGE_KEY: 'sb-test-auth-token',
  supabase: { auth: { signOut } },
}))

const { browserSession, SignOutError, REMOTE_SIGN_OUT_TIMEOUT_MS, LOCAL_SIGN_OUT_TIMEOUT_MS } = await import('./session')

const STORED_KEYS = ['sb-test-auth-token', 'sb-test-auth-token-code-verifier', 'sb-test-auth-token-user']

function storeSession() {
  STORED_KEYS.forEach((key) => window.localStorage.setItem(key, '{"access_token":"t"}'))
}

function storedKeys() {
  return STORED_KEYS.filter((key) => window.localStorage.getItem(key) !== null)
}

beforeEach(() => {
  signOut.mockReset()
  window.localStorage.clear()
  storeSession()
})

afterEach(() => {
  vi.useRealTimers()
  window.localStorage.clear()
})

describe('browser sign-out', () => {
  test('[P0] a successful sign-out makes one global call and reports nothing', async () => {
    signOut.mockResolvedValue({ error: null })

    await expect(browserSession.signOut()).resolves.toBeUndefined()

    expect(signOut).toHaveBeenCalledTimes(1)
    expect(signOut).toHaveBeenCalledWith()
  })

  test('[P0] a refused sign-out falls back to a local one and still clears this device', async () => {
    signOut
      .mockResolvedValueOnce({ error: new Error('Failed to fetch') })
      .mockResolvedValueOnce({ error: new Error('Failed to fetch') })

    const outcome = await browserSession.signOut().catch((cause: unknown) => cause)

    expect(signOut).toHaveBeenNthCalledWith(2, { scope: 'local' })
    expect(outcome).toBeInstanceOf(SignOutError)
    expect((outcome as InstanceType<typeof SignOutError>).localCleared).toBe(true)
    expect(storedKeys(), 'the stored session must not survive a failed sign-out').toEqual([])
  })

  test('[P0] a sign-out that throws is handled the same way', async () => {
    signOut.mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValueOnce({ error: null })

    const outcome = await browserSession.signOut().catch((cause: unknown) => cause)

    expect(outcome).toBeInstanceOf(SignOutError)
    expect((outcome as InstanceType<typeof SignOutError>).localCleared).toBe(true)
    expect(storedKeys()).toEqual([])
  })

  test('[P0] a sign-out request that hangs is abandoned rather than waited on', async () => {
    vi.useFakeTimers()
    signOut.mockImplementation(() => new Promise(() => undefined))

    const outcome = browserSession.signOut().catch((cause: unknown) => cause)
    await vi.advanceTimersByTimeAsync(REMOTE_SIGN_OUT_TIMEOUT_MS + LOCAL_SIGN_OUT_TIMEOUT_MS)

    const result = await outcome
    expect(result).toBeInstanceOf(SignOutError)
    expect((result as InstanceType<typeof SignOutError>).localCleared).toBe(true)
    expect(storedKeys()).toEqual([])
  })

  test('[P1] says so when even the local session could not be removed', async () => {
    signOut.mockResolvedValue({ error: new Error('Failed to fetch') })
    vi.spyOn(window.localStorage, 'removeItem').mockImplementation(() => {
      throw new Error('storage unavailable')
    })

    const outcome = await browserSession.signOut().catch((cause: unknown) => cause)

    expect(outcome).toBeInstanceOf(SignOutError)
    expect((outcome as InstanceType<typeof SignOutError>).localCleared).toBe(false)
  })
})
