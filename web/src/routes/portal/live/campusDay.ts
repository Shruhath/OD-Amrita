/**
 * Today, on campus.
 *
 * Its own module rather than an export from LivePortal.tsx so a test can import
 * it without that file exporting a non-component — the same reason
 * routes/public/days.ts exists.
 *
 * Asia/Kolkata, NOT the device's timezone. `od_date` is a campus-local day
 * decided by the server, so a phone left on another timezone — or simply the
 * half hour either side of midnight IST, which is when Asia/Kolkata's +05:30
 * offset bites — would ask for a different day than the one the marks were
 * filed under, and the live screen would read as empty at exactly the time it
 * matters most.
 */
export function campusToday(now: Date = new Date()): string {
  // en-CA renders as YYYY-MM-DD, which is the format the API expects.
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Kolkata' }).format(now)
}
