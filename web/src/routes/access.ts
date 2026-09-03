/**
 * Who may open which screen — declared ONCE.
 *
 * THE DEFECT THIS REPLACES: the portal's navigation was a fixed list of five
 * links shown to everyone, while each route enforced its own separate rule. A
 * teacher signing in therefore saw Events, Teachers, Students and Card populator
 * — four links that answer "Access denied" — and did NOT see a link to the one
 * screen they can actually use, because the marking route was never in the list.
 * The bar advertised four refusals and hid the single thing that worked.
 *
 * Two independent lists cannot be kept in agreement by attention, so there is
 * now one: the routes read their guard from here, and the navigation is built
 * from here. A link a role cannot follow is not a bug to notice — it is
 * unrepresentable.
 */

export type Role = 'admin' | 'teacher' | 'coordinator' | 'student'

/** Every guarded path and the roles admitted to it. */
export const ROUTE_ROLES = {
  '/portal/events': ['admin'],
  '/portal/events/:event_id': ['admin'],
  '/portal/live': ['teacher', 'admin'],
  '/portal/mark': ['teacher', 'admin'],
  '/portal/od-records': ['teacher', 'admin'],
  '/portal/disputes': ['teacher', 'admin'],
  '/portal/teachers': ['admin'],
  '/portal/students': ['admin'],
  '/portal/card-populator': ['admin'],
  // Live NFC/camera/search marking. Teachers use `/portal/mark` because their
  // correction flow needs an explicit date, slot and reason note.
  '/mark': ['coordinator', 'admin'],
} as const satisfies Record<string, readonly Role[]>

export type GuardedPath = keyof typeof ROUTE_ROLES

/**
 * The portal sections, in the order they appear in the bar.
 *
 * `/mark` is included for admins because they run the event desk too: it opens
 * the compact live NFC/camera marking surface rather than the manual portal
 * mark screen.
 */
const SECTIONS = [
  { to: '/portal/events', label: 'Events' },
  { to: '/mark', label: 'NFC marking' },
  { to: '/portal/live', label: 'Live' },
  { to: '/portal/mark', label: 'Mark' },
  { to: '/portal/od-records', label: 'Manage records' },
  { to: '/portal/disputes', label: 'Disputes' },
  { to: '/portal/teachers', label: 'Teachers' },
  { to: '/portal/students', label: 'Students' },
  { to: '/portal/card-populator', label: 'Card populator' },
] as const satisfies readonly { to: GuardedPath; label: string }[]

/** The sections this role may actually open. */
export function sectionsFor(role: string | undefined): { to: string; label: string }[] {
  if (!role) return []
  return SECTIONS.filter((section) => {
    if (section.to === '/mark' && role !== 'admin') return false
    return (ROUTE_ROLES[section.to] as readonly string[]).includes(role)
  })
}

/** The roles admitted to a path, for a route to hand to its boundary. */
export function rolesFor(path: GuardedPath): readonly string[] {
  return ROUTE_ROLES[path]
}
