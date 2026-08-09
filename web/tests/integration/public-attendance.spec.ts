import { expect, test } from '@playwright/test'

/**
 * The real-stack PUBLIC attendance journey.
 *
 * Runs the real `web/` app against the real Go API and real Postgres through
 * `api/test/portal/main.go`, using the same `/integration.html?role=X&path=Y`
 * harness entry every other spec in this directory uses. `role=none` maps to a
 * NULL token in tests/integration/main.tsx, which is the whole point here: this
 * is the only journey in the suite that is supposed to work with no credential
 * at all, so it is also the only one that can prove the public group is mounted
 * as a sibling of /api/v1 rather than inside it (T1).
 *
 * ---------------------------------------------------------------------------
 * THREE HARNESS CHANGES THIS SPEC REQUIRES. All three are part of Story 0.10 and
 * none of them exist yet — a run before they land fails here rather than in the
 * feature, so read this before debugging a red.
 *
 *  1. `api/test/portal/main.go` must REGISTER the public group, exactly as
 *     cmd/api does. A harness that lacks the route turns a real composition bug
 *     into a browser 404 that reads as a frontend defect (the 0.9 lesson).
 *
 *  2. `api/test/portal/main.go` seed() must seed DEDICATED fixtures:
 *       - students CB.SC.U4CSE24008 / CB.SC.U4CSE24009. Disjoint from every
 *         pool already owned by another spec — 24001-003 (coordinator-marking),
 *         24004-005 (capture-credits), 24006-007 (teacher-admin-attendance).
 *         0.9 lost CI rounds to exactly this collision.
 *       - a dedicated PUBLIC event with pre-seeded present od_records on more
 *         than one slot AND a gap. This journey READS; it never marks, so the
 *         records have to already exist.
 *       - an ARCHIVED event carrying one of those records, to prove T6 end to
 *         end. Insert the record while the event is still active, THEN archive:
 *         `events` triggers block child mutations on an archived event.
 *     ORDER MATTERS: seed() deletes od_records for every CB.SC.U4CSE%/ECE%
 *     student near the top, and deletes od_records for the marking event near
 *     the bottom. These fixtures must be inserted after both, on their own
 *     event id, or they are silently wiped before the first test runs.
 *
 *  3. `tests/integration/main.tsx` currently mirrors only
 *     `state.location.pathname` into `?path=`, DROPPING the search string. The
 *     harness therefore cannot round-trip `?student=<uuid>` through a reload,
 *     and the persistence test below fails for a harness reason rather than a
 *     product one. Mirror `state.location.search` too, and seed the memory
 *     router's initial entry from it.
 * ---------------------------------------------------------------------------
 *
 * Deliberately NO rate-limit burst test here. The limiter is keyed by client IP
 * and every request in this job shares one, so a burst would poison the bucket
 * for every other assertion in the same run. 429 behaviour is proven
 * deterministically in api/internal/middleware/ratelimit (refill, isolation,
 * eviction, forged headers) and the UI copy is proven in the mocked spec. The
 * harness should also raise PUBLIC_RATE_LIMIT_PER_MINUTE so a debounced journey
 * cannot trip it at all.
 */

const portalAPI = 'http://127.0.0.1:8081'

// Seeded by api/test/portal/main.go — see harness change 2 above.
const PUBLIC_STUDENT_ROLL = 'CB.SC.U4CSE24008'
const PUBLIC_STUDENT_ID = '95000000-0000-4000-8000-000000000009'
const PUBLIC_ARCHIVED_EVENT_NAME = 'Browser Public Archived Event'

async function openPublicDashboard(page: import('@playwright/test').Page, query = '') {
  await page.goto(`/integration.html?role=none&path=%2Fattendance${query}`)
  await expect(page.getByRole('searchbox', { name: /roll number|name|student/i })).toBeVisible()
}

test.describe('real stack public attendance dashboard', () => {
  // 0.10-RS-001 — the load-bearing one, asserted at the transport level before
  // any UI is involved: both public endpoints answer with NO Authorization
  // header, and the authenticated surface still refuses one (AC 1).
  //
  // This is the test that catches T1's `api.Group("/public")` slip against the
  // REAL server rather than against a router assembled in a Go test.
  test('[P0] both public endpoints answer an anonymous request while /api/v1 still refuses one', async ({ request }) => {
    const search = await request.get(`${portalAPI}/api/v1/public/students/search?q=${PUBLIC_STUDENT_ROLL}`)
    expect(
      search.status(),
      'GET /api/v1/public/students/search with no token must be 200. A 401 means the public group was built ' +
        'from the authenticated group (api.Group("/public")) — same URL, inherited auth middleware (T1).',
    ).toBe(200)

    const records = await request.get(`${portalAPI}/api/v1/public/students/${PUBLIC_STUDENT_ID}/od-records`)
    expect(records.status()).toBe(200)

    // The other half: opening the public group opened nothing else.
    const authenticated = await request.get(`${portalAPI}/api/v1/students/search?q=${PUBLIC_STUDENT_ROLL}`)
    expect(
      authenticated.status(),
      'the authenticated roster search answered without a token — the public group has leaked its lack of auth',
    ).toBe(401)

    const admin = await request.get(`${portalAPI}/api/v1/admin/events`)
    expect([401, 403]).toContain(admin.status())
  })

  // 0.10-RS-002 — the public response carries no email and no actor user id,
  // asserted against the REAL serialiser rather than a fixture (AC 5).
  //
  // The contract test proves the SCHEMA has no such field. This proves the
  // running server does not put one on the wire anyway.
  test('[P0] no email, actor user id, card token or credit value appears on the wire', async ({ request }) => {
    const response = await request.get(`${portalAPI}/api/v1/public/students/${PUBLIC_STUDENT_ID}/od-records`)
    expect(response.status()).toBe(200)

    const body = await response.text()
    const parsed = JSON.parse(body)

    // ALLOWLISTS at every level, checked against the real serialiser. A
    // substring blocklist alone is the tautology 0.9's review caught: it can
    // only ever catch the names somebody already thought of.
    expect(Object.keys(parsed).sort()).toEqual(['days', 'generated_at', 'student'])
    expect(Object.keys(parsed.student).sort()).toEqual(
      ['batch', 'branch', 'display_name', 'roll_no', 'section', 'student_id'],
    )
    // The day group's key is `od_date` since the event became a range —
    // `event_date` was RENAMED here, not supplemented, because a thirty-day
    // festival grouped on the event's first day collapses to one heading.
    expect(Object.keys(parsed.days[0]).sort()).toEqual(['od_date', 'records'])

    // The RECORD carries BOTH: `event_date` is the event's first day, which the
    // dispute form's event options are built from, and `od_date` is the day this
    // particular OD belongs to. Still an exact set — a date is the one field
    // here that says when a named student was physically somewhere, and this
    // response is served to the open internet.
    const record = parsed.days[0].records[0]
    expect(Object.keys(record).sort()).toEqual(
      [
        'category_id', 'category_name', 'event_date', 'event_id', 'event_name',
        'marked_at', 'marked_by_name', 'marked_by_role', 'od_date', 'record_id', 'slot',
      ],
    )

    // The substring sweep is the belt to that braces: it also catches a value
    // (not just a key) that leaks, such as a display name populated from an
    // email address.
    for (const forbidden of ['@amrita.edu', 'user_id', 'email', 'token', 'fingerprint', 'fallback_credit', 'capture_method']) {
      expect(body, `the public records response contains ${forbidden}`).not.toContain(forbidden)
    }
  })

  // 0.10-RS-003 — the whole journey, anonymously, against real Postgres rows
  // (AC 4, 9, 10, 11).
  test('[P0] an anonymous visitor searches, opens a student and sees the real seeded slots', async ({ page }) => {
    await openPublicDashboard(page)

    await expect(page.getByRole('button', { name: /sign in|continue with microsoft/i })).toHaveCount(0)

    await page.getByRole('searchbox', { name: /roll number|name|student/i }).fill(PUBLIC_STUDENT_ROLL)
    await page.getByRole('option', { name: new RegExp(PUBLIC_STUDENT_ROLL.replace(/\./g, '\\.'), 'i') }).click()

    // Scoped to ONE slot row, never searched page-wide. 0.9 lost CI rounds to
    // table-wide locators that matched another spec's rows — and a page-wide
    // check could be satisfied by three DIFFERENT rows (the event in one, the
    // marker in another, the slot in a third) while the row under test is wrong.
    const slotOne = page.getByRole('listitem').filter({ hasText: /slot\s*1\b/i })
    await expect(slotOne).toHaveCount(1)
    await expect(slotOne).toContainText(/Browser Public Event/i)

    // The seeded gap. This is PRD Journey 3, and the gap is now conveyed by the
    // absence of a slot-3 row rather than by a row saying "No record": the
    // student reads the slots they were credited for and sees that 3 is not one
    // of them.
    await expect(page.getByRole('listitem').filter({ hasText: /slot\s*3\b/i })).toHaveCount(0)
    await expect(page.getByText(/no record/i)).toHaveCount(0)

    await expect(page.getByText(/pending|syncing|not yet synced|queued/i)).toHaveCount(0)
  })

  // 0.10-RS-004 — a record on an ARCHIVED event is still visible to the student
  // (AC 6, T6), proven through the real archived-event triggers.
  test('[P0] archived-event records are still shown to the student', async ({ page }) => {
    await openPublicDashboard(page, `&student=${PUBLIC_STUDENT_ID}`)

    await expect(page.getByText(new RegExp(PUBLIC_ARCHIVED_EVENT_NAME, 'i'))).toBeVisible()
  })

  // 0.10-RS-005 — the selected student survives a reload, which is what makes a
  // shared link work (AC 10).
  //
  // Depends on harness change 3 above: main.tsx must mirror location.search.
  test('[P0] the selected student survives a reload', async ({ page }) => {
    await openPublicDashboard(page, `&student=${PUBLIC_STUDENT_ID}`)
    await expect(page.getByText(new RegExp(PUBLIC_STUDENT_ROLL.replace(/\./g, '\\.'), 'i'))).toBeVisible()

    await page.reload()

    await expect(
      page.getByText(new RegExp(PUBLIC_STUDENT_ROLL.replace(/\./g, '\\.'), 'i')),
      'the student was lost across a reload — a shared link would open an empty search box',
    ).toBeVisible()
  })

  // 0.10-RS-006 — an unknown student is a named 404, never a silent empty view
  // that reads as "you were not marked" (AC 12).
  test('[P0] an unknown student id is an explicit not-found message', async ({ page }) => {
    await openPublicDashboard(page, '&student=00000000-0000-4000-8000-000000000000')

    const alert = page.getByRole('alert')
    await expect(alert).toContainText(/no such student|not found|check the roll/i)
    await expect(alert).not.toContainText(/no OD records/i)
  })

  // 0.10-RS-007 — the public journey writes NOTHING (AC 14).
  //
  // Counted through the public API's own effects: every read below is anonymous,
  // and the authenticated admin attendance total for the seeded event must be
  // unchanged afterwards. The database-level audit-row assertion is the
  // web-deploy SQL step; this is the browser-side half of the same invariant.
  test('[P0] reading the dashboard writes no attendance rows', async ({ page, request }) => {
    const eventID = await request
      .get(`${portalAPI}/api/v1/public/students/${PUBLIC_STUDENT_ID}/od-records`)
      .then(async (response) => JSON.parse(await response.text()).days[0].records[0].event_id as string)

    const before = await request.get(`${portalAPI}/api/v1/admin/events/${eventID}/attendance`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    expect(before.status()).toBe(200)
    const beforeTotal = (await before.json()).total

    await openPublicDashboard(page, `&student=${PUBLIC_STUDENT_ID}`)
    await expect(page.getByText(new RegExp(PUBLIC_STUDENT_ROLL.replace(/\./g, '\\.'), 'i'))).toBeVisible()
    await page.reload()
    await expect(page.getByText(new RegExp(PUBLIC_STUDENT_ROLL.replace(/\./g, '\\.'), 'i'))).toBeVisible()

    const after = await request.get(`${portalAPI}/api/v1/admin/events/${eventID}/attendance`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    expect(
      (await after.json()).total,
      'the attendance total changed across a read-only public journey',
    ).toBe(beforeTotal)
  })
})
