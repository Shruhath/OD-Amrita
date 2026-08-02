import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from 'react-router-dom'
import { AppBackdrop } from './components/AppBackdrop'
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
      {/* Painted once, behind everything. The portal shell is opaque and covers
          it; only the auth surfaces let it through. */}
      <AppBackdrop />
      <AuthProvider session={session}>
        <RouterProvider router={router} />
      </AuthProvider>
    </QueryClientProvider>
  )
}
