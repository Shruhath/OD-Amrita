import { ApiFailure } from '../../../api/client'

/**
 * Renders an API date-only string (YYYY-MM-DD) without a UTC round trip.
 * `new Date('2026-08-15')` parses as midnight UTC and can render as the 14th in
 * a negative-offset timezone; constructing from the parts keeps the calendar
 * date the server sent.
 */
export function formatDate(value: string) {
  const [year, month, day] = value.split('-').map(Number)
  return new Intl.DateTimeFormat('en-GB', { day: '2-digit', month: 'short', year: 'numeric' })
    .format(new Date(year ?? 0, (month ?? 1) - 1, day ?? 1))
}

export function titleCase(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

/** Prefers the server's message, which is the authoritative explanation. */
export function messageOf(cause: unknown, fallback: string) {
  if (cause instanceof ApiFailure) return cause.body.message
  return cause instanceof Error && cause.message ? cause.message : fallback
}
