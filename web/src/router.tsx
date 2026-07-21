import { createBrowserRouter, createMemoryRouter, Navigate } from 'react-router-dom'
import EventsRoute from './routes/portal/events/EventsRoute'

const routes = [
  { path: '/portal/events', Component: EventsRoute },
  { path: '*', element: <Navigate to="/portal/events" replace /> },
]

export const createPortalBrowserRouter = () => createBrowserRouter(routes)
export const createPortalMemoryRouter = () => createMemoryRouter(routes, { initialEntries: ['/portal/events'] })

export type PortalRouter = ReturnType<typeof createPortalBrowserRouter>
