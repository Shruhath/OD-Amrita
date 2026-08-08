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

/**
 * Renders an event's date range, collapsing a one-day event to a single date.
 *
 * The collapse is the point: most events are one day, and rendering
 * "15 Aug 2026 – 15 Aug 2026" on every row of the list admins use most would be
 * a visible regression in exchange for a range almost nobody uses.
 *
 * Built on `formatDate` rather than re-parsing, so the UTC-midnight-renders-as-
 * yesterday bug is fixed in exactly one place.
 */
export function formatDateRange(start: string, end: string) {
  if (!end || start === end) return formatDate(start)
  return `${formatDate(start)} – ${formatDate(end)}`
}

/**
 * Every day from `start` to `end` inclusive, as YYYY-MM-DD strings.
 *
 * Bounds the attendance day filter to the event's own days: a free date input
 * would let an admin ask for a day the event never covered and get an empty
 * table that reads as "nobody was marked".
 *
 * Walks the calendar from the date PARTS rather than `new Date('2026-08-15')`,
 * which is midnight UTC and rolls back a day west of Greenwich — for the same
 * reason `formatDate` above does.
 *
 * PublicAttendanceScreen.tsx carries its own copy on purpose: the public bundle
 * is served to the open internet and deliberately imports nothing from the
 * authenticated portal, exactly as it keeps its own `formatDate`. Change both
 * together.
 */
export function enumerateDays(start: string, end: string): string[] {
  if (!start) return []
  const [year, month, day] = start.split('-').map(Number)
  const cursor = new Date(year ?? 0, (month ?? 1) - 1, day ?? 1)
  if (Number.isNaN(cursor.getTime())) return [start]
  const last = end && end > start ? end : start
  const days: string[] = []
  // Bounded well above the service's own 90-day range cap, so a malformed or
  // hostile end date cannot spin the browser.
  for (let guard = 0; guard < 200; guard += 1) {
    const value = [
      String(cursor.getFullYear()).padStart(4, '0'),
      String(cursor.getMonth() + 1).padStart(2, '0'),
      String(cursor.getDate()).padStart(2, '0'),
    ].join('-')
    days.push(value)
    if (value >= last) break
    cursor.setDate(cursor.getDate() + 1)
  }
  return days
}

export function titleCase(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1)
}

/** Prefers the server's message, which is the authoritative explanation. */
export function messageOf(cause: unknown, fallback: string) {
  if (cause instanceof ApiFailure) return cause.body.message
  return cause instanceof Error && cause.message ? cause.message : fallback
}
