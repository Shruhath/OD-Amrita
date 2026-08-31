import { useEffect, useRef, useState } from 'react'

export interface SmartPollOptions {
  activeInterval: number // e.g. 5_000ms
  idleInterval?: number // e.g. 30_000ms
  idleTimeoutMs?: number // e.g. 2 mins
  maxIdleTimeoutMs?: number // e.g. 10 mins (pauses polling)
  enabled?: boolean
  isSlotOpen?: boolean // if false, back off
  hasError?: boolean // if true, slow-poll to recover
}

/**
 * useSmartPollInterval dynamically adjusts or pauses TanStack query refetchInterval:
 * 1. Pauses immediately when the browser tab is hidden/backgrounded (document.visibilityState !== 'visible').
 * 2. Slows polling down after 2 minutes of user inactivity or on network error.
 * 3. Stops polling completely after 10 minutes of user inactivity (resumes instantly on interaction).
 * 4. Backs off when no active slot is open.
 */
export function useSmartPollInterval({
  activeInterval,
  idleInterval = activeInterval * 6,
  idleTimeoutMs = 120_000,
  maxIdleTimeoutMs = 600_000,
  enabled = true,
  isSlotOpen = true,
  hasError = false,
}: SmartPollOptions): number | false {
  const [isVisible, setIsVisible] = useState(() =>
    typeof document !== 'undefined' ? document.visibilityState === 'visible' : true,
  )
  const lastActivityRef = useRef<number>(Date.now())
  const [, setTick] = useState(0)

  // 1. Page Visibility API listener
  useEffect(() => {
    const handleVisibility = () => {
      const visible = document.visibilityState === 'visible'
      setIsVisible(visible)
      if (visible) {
        lastActivityRef.current = Date.now()
        setTick((t) => t + 1)
      }
    }
    document.addEventListener('visibilitychange', handleVisibility)
    return () => document.removeEventListener('visibilitychange', handleVisibility)
  }, [])

  // 2. User activity tracking (uses ref to avoid re-render churn on scroll/touch)
  useEffect(() => {
    if (!isVisible) return
    const onActivity = () => {
      lastActivityRef.current = Date.now()
    }
    const events = ['mousedown', 'keydown', 'touchstart', 'scroll']
    events.forEach((evt) => window.addEventListener(evt, onActivity, { passive: true }))
    return () => {
      events.forEach((evt) => window.removeEventListener(evt, onActivity))
    }
  }, [isVisible])

  // 3. Coarse timer to trigger idle threshold state transitions
  useEffect(() => {
    if (!isVisible || !enabled) return
    const interval = setInterval(() => {
      setTick((t) => t + 1)
    }, 15_000)
    return () => clearInterval(interval)
  }, [isVisible, enabled])

  if (!enabled || !isVisible) {
    return false
  }

  if (hasError) {
    return idleInterval
  }

  const idleDuration = Date.now() - lastActivityRef.current

  // Pause completely if user is inactive for > maxIdleTimeout
  if (idleDuration > maxIdleTimeoutMs) {
    return false
  }

  // Slow poll if slot is closed or user is idle > idleTimeout
  if (!isSlotOpen || idleDuration > idleTimeoutMs) {
    return idleInterval
  }

  return activeInterval
}
