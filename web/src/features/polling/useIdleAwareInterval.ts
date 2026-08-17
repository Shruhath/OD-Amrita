import { useCallback, useEffect, useRef, useState } from 'react'

/**
 * Polling that gives up when nobody is watching.
 *
 * WHY THIS EXISTS — it is a hosting bill, not a nicety.
 *
 * Neon suspends its compute when nothing talks to the database, and that
 * suspension is where the idle spend goes to zero. A single tab left open on the
 * marking screen defeats it completely: at a ten-second cadence it asks the
 * server 8,640 times a night, and the compute never gets a quiet minute in which
 * to suspend. The measured trade is roughly $18-37/month at a 0.25 CU minimum,
 * spent so that one person does not wait ~2.6s for a cold start in the morning.
 *
 * TanStack already declines to poll a HIDDEN tab — `refetchIntervalInBackground`
 * defaults to false — so what is left, and what this closes, is the tab that is
 * visible but unattended: a laptop open on a desk overnight.
 *
 * WHAT IT DOES NOT COST: the event itself. A coordinator marking students is
 * touching the screen constantly, so `stopAfterMs` is never reached while anyone
 * is working. The moment they do touch it — or switch back to the tab — polling
 * resumes AND `onResume` fires, so the screen is never showing stale data at the
 * moment somebody looks at it.
 */

/**
 * How long a screen may sit untouched before it stops asking.
 *
 * Half an hour is deliberately far longer than any gap in real use: a slot is
 * twenty-five minutes at most and a coordinator between students is still
 * tapping. It is short enough that a screen forgotten at 18:15 lets the database
 * suspend well before midnight.
 */
export const IDLE_STOP_MS = 30 * 60_000

/** Interactions that count as "somebody is still here". */
const INTERACTION_EVENTS = ['pointerdown', 'keydown'] as const

export type IdleAwareIntervalOptions = {
  /**
   * The interval to use while the screen is attended. `false` passes straight
   * through, so a screen that already has its own reason not to poll (a past
   * date, no live event) keeps that reason and simply gains the idle stop.
   */
  activeMs: number | false
  /** Defaults to {@link IDLE_STOP_MS}. */
  stopAfterMs?: number
  /**
   * Called when an idle screen wakes. Wire this to `refetch()`: resuming the
   * timer alone would leave the reader looking at up to half an hour of stale
   * data until the next tick.
   */
  onResume?: () => void
}

/**
 * Returns a value for TanStack Query's `refetchInterval`: the active interval
 * while somebody is here, and `false` once they have gone.
 */
export function useIdleAwareInterval({
  activeMs,
  stopAfterMs = IDLE_STOP_MS,
  onResume,
}: IdleAwareIntervalOptions): number | false {
  const [idle, setIdle] = useState(false)

  // The ref is the source of truth for the handlers and is mutated ONLY inside
  // them; `idle` state exists to trigger the re-render that hands the new
  // interval back to the caller. Mirroring state into the ref during render
  // instead would let a render clobber a wake that had not yet flushed.
  const idleRef = useRef(false)
  const onResumeRef = useRef(onResume)
  onResumeRef.current = onResume

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined

    const sleep = () => {
      idleRef.current = true
      setIdle(true)
    }

    const wake = () => {
      if (idleRef.current) {
        idleRef.current = false
        setIdle(false)
        onResumeRef.current?.()
      }
      if (timer !== undefined) clearTimeout(timer)
      timer = setTimeout(sleep, stopAfterMs)
    }

    // Returning to the tab is itself a sign of attention, and is the common way
    // back: the screen was hidden (and therefore not polling) for hours.
    const onVisibilityChange = () => {
      if (!document.hidden) wake()
    }

    wake()
    for (const type of INTERACTION_EVENTS) {
      document.addEventListener(type, wake, { passive: true })
    }
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => {
      if (timer !== undefined) clearTimeout(timer)
      for (const type of INTERACTION_EVENTS) {
        document.removeEventListener(type, wake)
      }
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [stopAfterMs])

  return idle ? false : activeMs
}

/**
 * Reports whether the screen has gone idle, for the rare caller that wants to
 * say so in the interface rather than only stop asking.
 */
export function useIsIdle(stopAfterMs: number = IDLE_STOP_MS): boolean {
  return useIdleAwareInterval({ activeMs: 1, stopAfterMs }) === false
}

/**
 * A stable `onResume` for the common case of "refetch this query".
 *
 * Exists so the screens do not each re-create the callback every render, which
 * would be harmless here (the hook reads it through a ref) but reads as though
 * it matters.
 */
export function useRefetchOnResume(refetch: () => unknown): () => void {
  return useCallback(() => {
    refetch()
  }, [refetch])
}
