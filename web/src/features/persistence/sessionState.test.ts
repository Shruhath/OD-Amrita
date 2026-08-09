import { renderHook, act } from '@testing-library/react'
import { beforeEach, describe, expect, test } from 'vitest'
import {
  PERSISTENCE_PREFIX,
  asString,
  asStringRecord,
  clearPersistedState,
  usePersistentState,
} from './sessionState'

/**
 * The store behind "my screen is still there when I come back".
 *
 * The security-relevant half is what this must REFUSE to be used for: a stored
 * value is editable by anyone with the developer tools open, so it is untrusted
 * input on the way back in, and no roster or card material may go into it at all
 * (AR20). The revivers are what make the first true; the second is a rule these
 * tests cannot enforce alone, so it is asserted at the call sites too.
 */

beforeEach(() => {
  window.sessionStorage.clear()
})

describe('persistent state', () => {
  test('[P0] a value written now is the value read back after a reload', () => {
    const first = renderHook(() => usePersistentState('scope.v1', '', asString))
    act(() => first.result.current[1]('event-42'))
    first.unmount()

    // A fresh mount stands in for the page having been discarded and rebuilt.
    const second = renderHook(() => usePersistentState('scope.v1', '', asString))
    expect(second.result.current[0]).toBe('event-42')
  })

  test('[P0] the stored value is available on the FIRST render, not after it', () => {
    window.sessionStorage.setItem(`${PERSISTENCE_PREFIX}scope.v1`, JSON.stringify('event-42'))

    const renders: string[] = []
    renderHook(() => {
      const [value] = usePersistentState('scope.v1', '', asString)
      renders.push(value)
      return value
    })

    // Reading in an effect instead would put the fallback on screen first and
    // replace it a frame later — a flicker on exactly the return-to-app path
    // this exists to smooth over.
    expect(renders[0]).toBe('event-42')
  })

  test('[P0] a tampered or malformed entry falls back instead of being trusted', () => {
    window.sessionStorage.setItem(`${PERSISTENCE_PREFIX}scope.v1`, '{ not json')
    const broken = renderHook(() => usePersistentState('scope.v1', 'fallback', asString))
    expect(broken.result.current[0]).toBe('fallback')

    window.sessionStorage.setItem(`${PERSISTENCE_PREFIX}scope.v1`, JSON.stringify({ evil: true }))
    const wrongType = renderHook(() => usePersistentState('scope.v1', 'fallback', asString))
    expect(wrongType.result.current[0]).toBe('fallback')
  })

  test('[P0] a partially written record is rejected whole', () => {
    // Restoring `undefined` into a controlled input silently turns it into an
    // uncontrolled one, which fails a long way from this line.
    window.sessionStorage.setItem(
      `${PERSISTENCE_PREFIX}filters.v1`,
      JSON.stringify({ branchID: 'b1', batch: '2024' }),
    )

    const { result } = renderHook(() =>
      usePersistentState(
        'filters.v1',
        { branchID: '', batch: '', section: '' },
        asStringRecord(['branchID', 'batch', 'section']),
      ),
    )

    expect(result.current[0]).toEqual({ branchID: '', batch: '', section: '' })
  })

  test('[P0] keys are namespaced, so clearing cannot reach another app on the origin', () => {
    window.sessionStorage.setItem('someone-elses-key', 'keep me')
    const { result } = renderHook(() => usePersistentState('scope.v1', '', asString))
    act(() => result.current[1]('event-42'))

    expect(window.sessionStorage.getItem(`${PERSISTENCE_PREFIX}scope.v1`)).toBe('"event-42"')

    clearPersistedState()

    expect(window.sessionStorage.getItem(`${PERSISTENCE_PREFIX}scope.v1`)).toBeNull()
    expect(window.sessionStorage.getItem('someone-elses-key')).toBe('keep me')
  })

  test('[P0] signing out clears every stored key, not every other one', () => {
    // The walk used to delete by index while iterating, which shifts the store
    // and skips entries — leaving one coordinator's screen for the next.
    for (const key of ['a.v1', 'b.v1', 'c.v1', 'd.v1', 'e.v1']) {
      window.sessionStorage.setItem(PERSISTENCE_PREFIX + key, '"x"')
    }
    expect(window.sessionStorage.length).toBe(5)

    clearPersistedState()

    expect(window.sessionStorage.length).toBe(0)
  })
})
