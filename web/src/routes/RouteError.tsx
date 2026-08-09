import { useState } from 'react'
import { useRouteError } from 'react-router-dom'
import { motion } from 'motion/react'

/**
 * What a person sees when a screen fails to load.
 *
 * Without an `errorElement`, React Router renders its own fallback — the one
 * headed "Unexpected Application Error!" that goes on to say "Hey developer"
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
  const [showDetails, setShowDetails] = useState(false)

  const message = error instanceof Error ? error.message : ''
  const isLoadFailure = /dynamically imported module|Importing a module script failed|Failed to fetch/i.test(message)

  return (
    <main className="min-h-screen bg-canvas text-foreground flex items-center justify-center p-6">
      <motion.div
        initial={{ opacity: 0, y: 20, scale: 0.96 }}
        animate={{ opacity: 1, y: 0, scale: 1 }}
        transition={{ type: 'spring', damping: 25, stiffness: 300 }}
        className="w-full max-w-md bg-surface border border-border rounded-[10px] shadow-sm p-8 flex flex-col items-center text-center"
      >
        <img
          className="h-12 w-auto object-contain mb-8 opacity-80"
          src="/brand/amrita-logo.png"
          alt="Amrita Vishwa Vidyapeetham"
          width={600}
          height={155}
        />

        <div className="w-16 h-16 rounded-full bg-status-error-bg flex items-center justify-center mb-6">
          <svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" fill="currentColor" viewBox="0 0 256 256" className="text-status-error-fg" aria-hidden="true">
            <path d="M236.8,188.09,149.35,36.22a24.76,24.76,0,0,0-42.7,0L19.2,188.09a23.51,23.51,0,0,0,0,23.72A24.35,24.35,0,0,0,40.55,224h174.9a24.35,24.35,0,0,0,21.33-12.19A23.51,23.51,0,0,0,236.8,188.09ZM120,104a8,8,0,0,1,16,0v40a8,8,0,0,1-16,0Zm8,88a12,12,0,1,1,12-12A12,12,0,0,1,128,192Z"/>
          </svg>
        </div>

        <p className="text-xs uppercase tracking-widest text-foreground-muted font-bold mb-2">
          {isLoadFailure ? 'Connection problem' : 'Application error'}
        </p>
        <h1 className="text-2xl font-bold tracking-tight mb-3">
          {isLoadFailure ? "This screen didn't finish loading" : 'Something went wrong'}
        </h1>
        <p className="text-foreground-muted leading-relaxed mb-8" role="alert">
          {isLoadFailure
            ? 'Check your connection and try again — nothing you did has been lost.'
            : 'The screen could not be opened. Trying again usually fixes it.'}
        </p>

        <button
          className="primary w-full mb-4"
          type="button"
          onClick={() => window.location.reload()}
        >
          Try again
        </button>

        {message && (
          <div className="w-full">
            <button
              type="button"
              onClick={() => setShowDetails((v) => !v)}
              className="text-xs text-foreground-muted hover:text-foreground transition-colors flex items-center gap-1 mx-auto"
            >
              <span>{showDetails ? '▲' : '▼'}</span>
              {showDetails ? 'Hide' : 'Show'} technical details
            </button>
            {showDetails && (
              <pre className="mt-3 text-xs text-foreground-muted bg-canvas border border-border rounded-[10px] p-3 overflow-x-auto text-left whitespace-pre-wrap break-all">
                {message}
              </pre>
            )}
          </div>
        )}
      </motion.div>
    </main>
  )
}
