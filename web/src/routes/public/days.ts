/**
 * Date helpers for the PUBLIC bundle.
 *
 * Its own module rather than a function inside PublicAttendanceScreen.tsx so a
 * test can import it without exporting a non-component from a component file —
 * and so the public bundle still pulls in nothing from the authenticated portal,
 * which is the reason this logic is duplicated at all.
 */

/**
 * Every day from `start` to `end` inclusive, as YYYY-MM-DD strings.
 *
 * Bounds the dispute form's day picker to the event's real range.
 * routes/portal/events/format.ts carries the portal's copy — change both
 * together. That instruction is backed by a test rather than left as prose:
 * PublicAttendanceDays.test.tsx asserts the two implementations agree across
 * single days, ranges, month and year boundaries, a leap day and degenerate
 * input. A comment saying "change both together" is not a mechanism.
 *
 * Walks the calendar from the date PARTS rather than `new Date('2026-08-15')`,
 * which is midnight UTC and rolls back a day west of Greenwich — that would
 * offer a student a day the event never had.
 */
export function enumerateDays(start: string, end: string): string[] {
  if (!start) return []
  const [year, month, day] = start.split('-').map(Number)
  const cursor = new Date(year ?? 0, (month ?? 1) - 1, day ?? 1)
  if (Number.isNaN(cursor.getTime())) return [start]
  const last = end && end > start ? end : start
  const days: string[] = []
  // Bounded well above the service's own 90-day range cap. A malformed or
  // hostile end date must not spin here: this runs in a browser, on a public
  // page, against a value that arrived over the wire.
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
