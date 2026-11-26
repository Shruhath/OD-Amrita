import { expect, test, type Page } from '@playwright/test'
import { fulfilPreflight, installPortalSession, json, type PortalRole } from './support/portal-session'
import { unreadableControls } from './support/contrast'

/**
 * EVERY CONTROL ON EVERY SCREEN, IN BOTH THEMES, HAS TO BE READABLE.
 *
 * WHY THIS EXISTS AS A SWEEP RATHER THAN AS ONE MORE ASSERTION PER SCREEN. A
 * button that has lost its fill is still present, still correctly named, still
 * focusable and still clickable — every property the rest of the suite checks
 * stays true while nobody can see it. The defect that prompted this was a single
 * CSS selector whose specificity was one notch too high, and it took out the
 * primary action on several screens at once; the suite had 400 tests and not one
 * of them noticed. Per-screen assertions would have to be remembered per screen,
 * which is the thing that failed.
 *
 * WHAT IT MEASURES. For each visible, enabled control: the contrast between the
 * text colour and whatever is ACTUALLY painted behind it — walking up to the
 * nearest ancestor with a real background, because a transparent button shows
 * the panel through it. WCAG AA for ordinary text is 4.5:1, and that is the
 * property a person means by "I cannot see the button".
 *
 * WHAT IT DOES NOT MEASURE. Whether a control looks RIGHT — a legible button in
 * the wrong colour passes here and belongs in a visual review. Disabled controls
 * are skipped: they are deliberately faded, and holding them to the same bar
 * would only teach the next person to raise the threshold.
 */

const branch = { id: '11111111-1111-4111-8111-111111111111', code: 'CSE', name: 'Computer Science and Engineering' }
const ece = { id: '22222222-2222-4222-8222-222222222222', code: 'ECE', name: 'Electronics and Communication' }
const eventID = 'aaaaaaaa-aaaa-4aaa-8aaa-000000000001'

const events = [
  { id: eventID, name: 'Amritotsavam 2026', event_date: '2026-07-28', status: 'active', branches: [branch], created_at: '2026-07-01T04:00:00Z' },
  { id: 'aaaaaaaa-aaaa-4aaa-8aaa-000000000002', name: 'Convocation Rehearsal', event_date: '2026-06-14', status: 'archived', branches: [branch, ece], created_at: '2026-06-01T04:00:00Z' },
]
const categories = [
  { id: 'cccccccc-cccc-4ccc-8ccc-000000000001', event_id: eventID, name: 'Cultural Programme', created_at: '2026-07-01T04:00:00Z' },
  { id: 'cccccccc-cccc-4ccc-8ccc-000000000002', event_id: eventID, name: 'Sports Meet', created_at: '2026-07-01T04:05:00Z' },
]
const teachers = [
  { user_id: 'e1', email: 'anitha.r@amrita.edu', name: 'Anitha Ramesh', branches: [branch], created_at: '2026-07-01T04:00:00Z' },
]
const coordinators = [
  { id: 'd1', user_id: 'c1', email: 'rahul.k@amrita.edu', name: 'Rahul Krishnan', category_id: categories[0].id, category_name: 'Cultural Programme', created_at: '2026-07-10T09:30:00Z' },
]
const students = [
  { student_id: 's1', roll_no: 'CB.SC.U4CSE99124', display_name: 'Aparna Menon', branch_code: 'CSE', batch: '2024', section: 'A', card_state: 'pending' },
]

const page1 = (data: unknown[]) => ({ data, page: 1, page_size: 20, total: data.length })

async function mockApi(page: Page, role: PortalRole) {
  await page.route('**/api/v1/**', async (route) => {
    if (route.request().method() === 'OPTIONS') return fulfilPreflight(route)
    const path = new URL(route.request().url()).pathname

    if (path.endsWith('/auth/me')) return json(route, { subject: `sweep-${role}`, email: `${role}@amrita.edu`, name: 'Arjun Menon', role })
    if (path.endsWith('/branches')) return json(route, page1([branch, ece]))
    if (path.endsWith('/coordinators')) return json(route, page1(coordinators))
    if (path.endsWith('/categories')) return json(route, page1(categories))
    if (path.endsWith('/students/search')) return json(route, page1(students))
    if (path.endsWith('/admin/students')) return json(route, page1(students))
    if (path.endsWith('/teachers')) return json(route, page1(teachers))
    if (path.endsWith('/extended-slots')) return json(route, { enabled: false, max_slot: 12 })
    if (/\/events\/[^/]+$/.test(path)) return json(route, events[0])
    if (path.endsWith('/events')) return json(route, page1(events))
    return json(route, page1([]))
  })
}

type Surface = { name: string; role: PortalRole | null; path: string; prepare?: (page: Page) => Promise<void> }

const surfaces: Surface[] = [
  { name: 'the front door', role: null, path: '/' },
  { name: 'access denied', role: 'student', path: '/portal/events' },
  { name: 'the public board', role: null, path: '/attendance' },
  { name: 'events', role: 'admin', path: '/portal/events' },
  {
    name: 'the new-event dialog',
    role: 'admin',
    path: '/portal/events',
    prepare: async (page) => {
      await page.getByRole('button', { name: 'New event' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  { name: 'event detail', role: 'admin', path: `/portal/events/${eventID}` },
  { name: 'teachers', role: 'admin', path: '/portal/teachers' },
  { name: 'students', role: 'admin', path: '/portal/students' },
  { name: 'manage records', role: 'admin', path: '/portal/od-records' },
  { name: 'disputes', role: 'admin', path: '/portal/disputes' },
  { name: 'the live view', role: 'teacher', path: '/portal/live' },
  { name: 'teacher marking', role: 'teacher', path: '/portal/mark' },
  { name: 'the card populator', role: 'admin', path: '/portal/card-populator' },
  { name: 'coordinator marking', role: 'coordinator', path: '/mark' },
]

for (const theme of ['light', 'dark'] as const) {
  for (const surface of surfaces) {
    test(`[P0] every control on ${surface.name} is readable (${theme})`, async ({ page }) => {
      await installPortalSession(page, surface.role)
      await mockApi(page, (surface.role ?? 'student') as PortalRole)
      // Registered AFTER installPortalSession, whose init script clears storage.
      await page.addInitScript((value) => localStorage.setItem('od-amrita-theme', value), theme)

      await page.goto(surface.path)
      await page.waitForLoadState('networkidle')
      if (surface.prepare) await surface.prepare(page)
      // The theme transition is 200ms; measuring mid-fade reads a blended colour
      // that belongs to neither theme.
      await page.waitForTimeout(500)

      const findings = await unreadableControls(page)

      expect(
        findings,
        `these controls cannot be read on ${surface.name} in the ${theme} theme:\n` +
          findings.map((finding) => `  "${finding.label}" — ${finding.contrast}:1 (${finding.color} on ${finding.background})`).join('\n'),
      ).toEqual([])
    })
  }
}
