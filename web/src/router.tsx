import { createBrowserRouter, createMemoryRouter, Navigate } from 'react-router-dom'
import EventsRoute from './routes/portal/events/EventsRoute'
import EventDetailRoute from './routes/portal/events/EventDetailRoute'
import StudentsRoute from './routes/portal/students/StudentsRoute'
import TeachersRoute from './routes/portal/teachers/TeachersRoute'

const routes = [
  { path: '/portal/events', Component: EventsRoute },
  // The per-event surface. Story 2.4 (per-category coordinator assignment) and
  // 2.8 (per-event credit configuration) both need this route, which is why the
  // categories live here rather than in an expandable list row.
  { path: '/portal/events/:event_id', Component: EventDetailRoute },
  { path: '/portal/teachers', Component: TeachersRoute },
  { path: '/portal/students', Component: StudentsRoute },
  { path: '*', element: <Navigate to="/portal/events" replace /> },
]

export const createPortalBrowserRouter = () => createBrowserRouter(routes)

// initialEntries is parameterized for the browser-integration harness, which is
// served from a single static entry point and therefore cannot use a browser
// router: it needs to open (and, after a reload, reopen) a deep route.
export const createPortalMemoryRouter = (initialEntries: string[] = ['/portal/events']) =>
  createMemoryRouter(routes, { initialEntries })

export type PortalRouter = ReturnType<typeof createPortalBrowserRouter>
