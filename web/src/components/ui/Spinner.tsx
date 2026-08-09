/**
 * The loading indicator.
 *
 * A spinning ring rather than a line of prose. "Checking your session…" told the
 * visitor what the code was doing rather than that anything was happening at
 * all, and a static sentence is indistinguishable from a page that has frozen —
 * which is precisely the question someone is asking while they wait.
 *
 * THE WORDS DO NOT DISAPPEAR, they stop being visual. The label is rendered
 * screen-reader-only and the whole thing is a live `status` region, so a screen
 * reader still announces what is loading; a bare animated circle would announce
 * nothing at all. That is why `label` is required rather than optional.
 */
export function Spinner({
  label,
  size = 'medium',
  className,
}: {
  label: string
  size?: 'small' | 'medium'
  className?: string
}) {
  return (
    <span
      className={['spinner', `spinner--${size}`, className].filter(Boolean).join(' ')}
      role="status"
    >
      <span className="spinner__ring" aria-hidden="true" />
      <span className="sr-only">{label}</span>
    </span>
  )
}

/** The full-height version, for a screen that has nothing else on it yet. */
export function LoadingScreen({ label }: { label: string }) {
  return (
    <main className="center-state">
      <Spinner label={label} />
    </main>
  )
}
