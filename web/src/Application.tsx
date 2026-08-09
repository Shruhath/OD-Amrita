import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'
import { AppBackdrop } from './components/AppBackdrop'
import { InstallPrompt } from './components/InstallPrompt'
import { AuthProvider } from './features/auth/AuthGate'
import type { SessionPort } from './features/auth/session'
import type { PortalRouter } from './router'
import './App.css'

export function Application({ session, router }: { session: SessionPort; router: PortalRouter }) {
  const [queryClient] = useState(() => new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 15_000 }, mutations: { retry: false } },
  }))
  return (
    <QueryClientProvider client={queryClient}>
      {/* No theme provider: the theme is an external store (features/theme/store),
          so every toggle shares it without one. */}
      {/* Painted once, behind everything. Every shell above it is transparent,
          so it is the ground for all seven surfaces, not just sign-in. */}
      <AppBackdrop />
      <AuthProvider session={session}>
        <RouterProvider router={router} />
        {/* At the root, not on one screen: the browser fires its install offer
            once, early, on whichever page the visitor happened to land on —
            which for a student is the public dashboard and for a coordinator is
            the sign-in card. Mounting it here means whichever of those it was,
            the offer is caught. It renders nothing unless there is one. */}
        <InstallPrompt />
      </AuthProvider>
    </QueryClientProvider>
  )
}
