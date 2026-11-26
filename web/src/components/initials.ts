/**
 * Avatar initials.
 *
 * Its own module rather than an export from AccountMenu.tsx so a test can import
 * it without that file exporting a non-component — the same reason
 * routes/public/days.ts exists.
 */

/**
 * Two characters to stand for an account.
 *
 * A student account's `name` is frequently the ROLL NUMBER rather than a
 * person's name, which is why the split treats dots, underscores and hyphens as
 * word breaks: without that, `cb.sc.u4cse99712` is a single "word" and every
 * student in the university gets the same single-letter avatar.
 *
 * Falling back to the first two characters covers the one-word case, so the
 * result is never a single character and never empty.
 */
export function initialsFor(name: string | null | undefined, email: string): string {
  const source = (name ?? '').trim() || email.split('@')[0] || '?'
  const words = source.split(/[\s._-]+/).filter(Boolean)
  if (words.length >= 2) {
    return (words[0]![0]! + words[1]![0]!).toUpperCase()
  }
  return source.slice(0, 2).toUpperCase()
}
