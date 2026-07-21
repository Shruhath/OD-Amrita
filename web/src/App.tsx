import { lazy, Suspense, useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createBrowserRouter, Navigate, RouterProvider } from 'react-router-dom'
import { AuthProvider, SessionBoundary } from './features/auth/AuthGate'
import './App.css'

const EventsPortal = lazy(() => import('./routes/portal/events/EventsPortal'))

function EventsRoute() {
  return (
    <SessionBoundary>
      <Suspense fallback={<main className="center-state"><p role="status">Loading events workspace…</p></main>}>
        <EventsPortal />
      </Suspense>
    </SessionBoundary>
  )
}

const router = createBrowserRouter([
  { path: '/portal/events', Component: EventsRoute },
  { path: '*', element: <Navigate to="/portal/events" replace /> },
])

export default function App() {
  const [queryClient] = useState(() => new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 15_000 }, mutations: { retry: false } },
  }))
  return (
    <QueryClientProvider client={queryClient}>
      <AuthProvider>
        <RouterProvider router={router} />
      </AuthProvider>
    </QueryClientProvider>
  )
}
