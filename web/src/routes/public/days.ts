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
export function campusToday(now: Date = new Date()): string {
  // en-CA renders as YYYY-MM-DD, the format `od_date` uses on the wire.
  //
  // Asia/Kolkata, NOT the device's timezone. `od_date` is a campus-local day
  // the server decides, so a phone left on another timezone — or simply the
  // half hour either side of midnight IST, which is when the +05:30 offset
  // bites — would open on a day the marks were never filed under, and the
  // dashboard would read as empty at exactly the hour a student checks it.
  //
  // routes/portal/live/campusDay.ts carries the portal's copy, duplicated for
  // the same reason enumerateDays is: the public bundle imports nothing from
  // the authenticated portal. PublicAttendanceDays.test.tsx asserts the two
  // agree, because "change both together" written as prose is not a mechanism.
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' }).format(now)
}

/**
 * The day the dashboard opens on, given the days a student actually has marks.
 *
 * TODAY WHEN TODAY HAS MARKS. That is the case the page is for — PRD Journey 3
 * is a student checking at 10pm on the day of the event — and the day filter
 * then walks backwards from there.
 *
 * OTHERWISE THE MOST RECENT DAY THAT HAS MARKS. Opening on an empty today for a
 * student whose last OD was a week ago would render "nothing marked" as the
 * first thing they see, and this page's one unbreakable rule is that it must
 * never imply an absence it was not asked about. The day is on screen and
 * selectable, so nothing is hidden — the default just starts somewhere useful.
 */
export function defaultDay(recordDays: readonly string[], today: string): string {
  if (recordDays.includes(today)) return today
  const sorted = [...recordDays].sort()
  const past = sorted.filter((day) => day < today)
  return past.at(-1) ?? sorted[0] ?? today
}

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
