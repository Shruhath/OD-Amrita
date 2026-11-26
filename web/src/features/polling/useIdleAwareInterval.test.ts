import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { IDLE_STOP_MS, useIdleAwareInterval } from './useIdleAwareInterval'

/**
 * The hosting bill, as a unit test.
 *
 * The property worth breaking the build over is the FIRST one: a screen nobody
 * is touching must eventually stop asking, because that silence is the only
 * thing that lets the database suspend. The rest guard the other half of the
 * trade — that a screen somebody IS using never stops, and that waking one shows
 * fresh data rather than whatever it had half an hour ago.
 */

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('idle-aware polling', () => {
  test('[P0] an untouched screen stops polling', () => {
    const { result } = renderHook(() => useIdleAwareInterval({ activeMs: 10_000 }))

    expect(result.current).toBe(10_000)

    act(() => {
      vi.advanceTimersByTime(IDLE_STOP_MS + 1)
    })

    expect(
      result.current,
      'a forgotten tab kept polling — this is the whole cost of the feature',
    ).toBe(false)
  })

  test('[P0] a screen in use never stops', () => {
    const { result } = renderHook(() => useIdleAwareInterval({ activeMs: 10_000 }))

    // Somebody marking students: a tap every few minutes, for two hours.
    for (let elapsed = 0; elapsed < 2 * 60 * 60_000; elapsed += 5 * 60_000) {
      act(() => {
        vi.advanceTimersByTime(5 * 60_000)
        document.dispatchEvent(new Event('pointerdown'))
      })
      expect(result.current, `stopped polling after ${elapsed / 60_000} minutes of active use`).toBe(10_000)
    }
  })

  test('[P0] waking refetches, so nobody reads stale data', () => {
    const onResume = vi.fn()
    const { result } = renderHook(() => useIdleAwareInterval({ activeMs: 10_000, onResume }))

    act(() => {
      vi.advanceTimersByTime(IDLE_STOP_MS + 1)
    })
    expect(result.current).toBe(false)
    expect(onResume).not.toHaveBeenCalled()

    act(() => {
      document.dispatchEvent(new Event('pointerdown'))
    })

    expect(result.current).toBe(10_000)
    expect(onResume, 'polling resumed without refetching — the screen shows half-hour-old data').toHaveBeenCalledTimes(1)
  })

  test('[P0] returning to the tab wakes it', () => {
    const onResume = vi.fn()
    const { result } = renderHook(() => useIdleAwareInterval({ activeMs: 20_000, onResume }))

    act(() => {
      vi.advanceTimersByTime(IDLE_STOP_MS + 1)
    })
    expect(result.current).toBe(false)

    act(() => {
      document.dispatchEvent(new Event('visibilitychange'))
    })

    // jsdom reports the document as visible, so this is the return-to-tab case.
    expect(result.current).toBe(20_000)
    expect(onResume).toHaveBeenCalledTimes(1)
  })

  test("[P1] a caller's own reason not to poll is preserved", () => {
    // LivePortal and EventAttendanceSection already return false for a past date
    // or a finished event. Wrapping must not turn that back into polling.
    const { result } = renderHook(() => useIdleAwareInterval({ activeMs: false }))

    expect(result.current).toBe(false)
  })

  test('[P1] the interval follows the caller while attended', () => {
    // MarkScreen swaps 10s for 60s when no slot is open. That decision stays the
    // caller's; this hook only ever overrides it with `false`.
    const { rerender, result } = renderHook(({ ms }) => useIdleAwareInterval({ activeMs: ms }), {
      initialProps: { ms: 10_000 },
    })

    expect(result.current).toBe(10_000)
    rerender({ ms: 60_000 })
    expect(result.current).toBe(60_000)
  })

  test('[P1] no interaction listeners survive unmount', () => {
    const remove = vi.spyOn(document, 'removeEventListener')
    const { unmount } = renderHook(() => useIdleAwareInterval({ activeMs: 10_000 }))

    unmount()

    const removed = remove.mock.calls.map(([type]) => type)
    expect(removed).toEqual(expect.arrayContaining(['pointerdown', 'keydown', 'visibilitychange']))
  })
})
