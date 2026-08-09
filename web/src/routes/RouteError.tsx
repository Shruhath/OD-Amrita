import { useRouteError } from 'react-router-dom'

/**
 * What a person sees when a screen fails to load.
 *
 * Without an `errorElement`, React Router renders its own fallback — the one
 * headed "Unexpected Application Error!" that goes on to say "Hey developer 👋"
 * and suggests adding an ErrorBoundary. That page was reaching STUDENTS on a
 * public dashboard, which is both alarming and useless to them: it names a
 * JavaScript filename and offers nothing to do about it.
 *
 * This says what happened in one line and gives the one action that fixes the
 * overwhelmingly common cause — a screen whose code did not download because the
 * phone's connection was not up yet.
 */
export default function RouteError() {
  const error = useRouteError()

  // A failed dynamic import is the expected case, and it is worth naming
  // separately: the app is fine and the connection is the problem, which is a
  // different thing to tell someone than "this broke".
  const message = error instanceof Error ? error.message : ''
  const isLoadFailure = /dynamically imported module|Importing a module script failed|Failed to fetch/i.test(message)

  return (
    <main className="center-state">
      <img
        className="auth-brand"
        src="/brand/amrita-logo.png"
        alt="Amrita Vishwa Vidyapeetham"
        width={600}
        height={155}
      />
      <h1>{isLoadFailure ? 'This screen did not finish loading' : 'Something went wrong'}</h1>
      <p role="alert">
        {isLoadFailure
          ? 'Check your connection and try again — nothing you did has been lost.'
          : 'The screen could not be opened. Trying again usually fixes it.'}
      </p>
      <button className="primary" type="button" onClick={() => window.location.reload()}>
        Try again
      </button>
      {/* The technical detail is kept, because an operator reporting a problem
          during an event needs something to quote — but it is secondary, small,
          and never the headline. */}
      {message ? <p className="auth-hint"><code>{message}</code></p> : null}
    </main>
  )
}
