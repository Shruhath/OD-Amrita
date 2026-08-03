import { createBrowserRouter, createMemoryRouter } from 'react-router-dom'
import MarkRoute from './routes/mark/MarkRoute'
import RoleLanding from './routes/RoleLanding'
import CardPopulatorRoute from './routes/portal/card-populator/CardPopulatorRoute'
import EventsRoute from './routes/portal/events/EventsRoute'
import EventDetailRoute from './routes/portal/events/EventDetailRoute'
import StudentsRoute from './routes/portal/students/StudentsRoute'
import TeachersRoute from './routes/portal/teachers/TeachersRoute'

const routes = [
  { path: '/portal/events', Component: EventsRoute },
  // The per-event surface. Per-category coordinator assignment and per-event
  // credit configuration both need this route, which is why the categories live
  // here rather than in an expandable list row.
  { path: '/portal/events/:event_id', Component: EventDetailRoute },
  { path: '/portal/teachers', Component: TeachersRoute },
  { path: '/portal/students', Component: StudentsRoute },
  // The pre-event card-writing surface. Admin-only through SessionBoundary,
  // and Chrome-on-Android only at runtime because it needs Web NFC.
  { path: '/portal/card-populator', Component: CardPopulatorRoute },
  // The coordinator marking surface. Deliberately at the ROOT rather than under
  // /portal: /portal/* is the admin administration surface, and this screen has
  // its own compact phone chrome rather than the admin nav.
  { path: '/mark', Component: MarkRoute },
  { path: '*', Component: RoleLanding },
]

export const createPortalBrowserRouter = () => createBrowserRouter(routes)

// initialEntries is parameterized for the browser-integration harness, which is
// served from a single static entry point and therefore cannot use a browser
// router: it needs to open (and, after a reload, reopen) a deep route.
export const createPortalMemoryRouter = (initialEntries: string[] = ['/portal/events']) =>
  createMemoryRouter(routes, { initialEntries })

export type PortalRouter = ReturnType<typeof createPortalBrowserRouter>
