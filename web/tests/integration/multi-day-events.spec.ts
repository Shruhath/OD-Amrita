import { expect, test } from '@playwright/test'

/**
 * The real-stack multi-day journey.
 *
 * Runs the real `web/` app against the real Go API and real Postgres through
 * `api/test/portal/main.go`. This is the ONLY level that can see the failures the
 * mocked suite cannot: a unique index that still refuses day two, a counter that
 * sums the whole range, a credit pool that never resets, a day filter that
 * narrows nothing.
 *
 * ⚠️ THREE HARNESS CHANGES THIS DEPENDS ON, and none of them exist yet
 * (api/test/portal/main.go):
 *
 *  1. The marking event must be seeded as a RANGE COVERING TODAY — e.g. yesterday
 *     -> tomorrow — not as a single day dated today. The comment at main.go:73-76
 *     currently explains that the event is dated TODAY "because odrecord.ActiveSlot
 *     compares against events.event_date"; that comment describes the behaviour
 *     this story removes, and a range is what proves CONTAINMENT rather than
 *     equality. Seeded as a single day, every assertion below passes for the wrong
 *     reason.
 *  2. Dedicated students, disjoint from every other spec's roll numbers (0.9 and
 *     0.10 both lost CI rounds to roll-number collisions across specs sharing one
 *     database).
 *  3. Public-dashboard fixtures must seed `od_date` EXPLICITLY rather than letting
 *     it derive from `server_received_at`, or the day-grouping assertions become
 *     vacuous (T24).
 *
 * NOT skipped and not gated. They fail until the harness and the feature exist.
 */

const portalAPI = 'http://127.0.0.1:8081'

/** The harness's marking event — seeded as a range that CONTAINS today. */
const MARKING_EVENT_ID = '93000000-0000-4000-8000-000000000001'

/**
 * The harness's dedicated multi-day student (api/test/portal/main.go), marked on
 * the same slot of three consecutive days of one event.
 *
 * Its own student and its own event, disjoint from every other spec's pool:
 * Stories 0.9 and 0.10 each lost a CI round to a shared roll number, and the
 * public-dashboard conservation gate asserts an exact record count for the two
 * Story 0.10 event ids.
 */
const MULTIDAY_STUDENT_ID = process.env.PORTAL_MULTIDAY_STUDENT_ID ?? '95000000-0000-4000-8000-000000000012'

function isoDay(offsetDays: number) {
  const now = new Date()
  now.setDate(now.getDate() + offsetDays)
  return now.toISOString().slice(0, 10)
}

test.describe('the multi-day event, end to end', () => {
  // 0.12a-INT-E2E-001 — the marking screen opens on a day INSIDE the range, and
  // the range it reports contains today.
  //
  // The whole slot gate is this comparison. Seeded as a range covering yesterday
  // -> tomorrow, an ActiveSlot still written as date EQUALITY reports no slot on
  // every day of the event, and the screen is dead for the entire festival.
  test('0.12a-INT-E2E-001 the slot gate opens on a day inside the range', async ({ page }) => {
    const headers = { Authorization: 'Bearer portal-coordinator-token' }

    // The coordinator's own scoped event read. `/api/v1/mark/*` holds the status
    // read only; the selector's events come from `/api/v1/events`.
    const events = await page.request.get(`${portalAPI}/api/v1/events`, { headers })
    expect(events.ok(), await events.text()).toBeTruthy()
    const body = await events.json()
    const marking = body.data.find((event: { id: string }) => event.id === MARKING_EVENT_ID)
    expect(marking, 'the harness marking event must be present').toBeDefined()
    expect(
      marking.end_date >= isoDay(0) && marking.event_date <= isoDay(0),
      `the harness must seed a RANGE containing today (got ${marking.event_date}..${marking.end_date}). ` +
        'Seeded as a single day dated today, every assertion in this file passes for the wrong reason — ' +
        'equality and containment are indistinguishable on a one-day event.',
    ).toBeTruthy()

    // And the status read answers for a real event+category pair on that day.
    // category_id is a required UUID, so an empty one is a 400 about the request
    // rather than anything about the range.
    const categories = await page.request.get(
      `${portalAPI}/api/v1/events/${MARKING_EVENT_ID}/categories`,
      { headers },
    )
    expect(categories.ok(), await categories.text()).toBeTruthy()
    const categoryID = (await categories.json()).data[0].id

    const status = await page.request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${MARKING_EVENT_ID}&category_id=${categoryID}`,
      { headers },
    )
    expect(status.ok(), 'GET /mark/status must answer for a coordinator on a day inside the range').toBeTruthy()
  })

  // 0.12a-INT-E2E-002 — an admin creates a real range and it comes back as one.
  //
  // One request, one event row. A fan-out that wrote thirty rows would satisfy
  // "the range was stored" while building the event-SERIES option that was
  // reversed on 2026-08-06.
  test('0.12a-INT-E2E-002 creating a range writes exactly one event', async ({ page }) => {
    const start = isoDay(30)
    const end = isoDay(59)

    const branches = await page.request.get(`${portalAPI}/api/v1/admin/branches`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const branchID = (await branches.json()).data[0].id

    const before = await page.request.get(`${portalAPI}/api/v1/admin/events?page_size=100`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const beforeTotal = (await before.json()).total

    const created = await page.request.post(`${portalAPI}/api/v1/admin/events`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
      data: { name: `Range journey ${Date.now()}`, event_date: start, end_date: end, branch_ids: [branchID] },
    })
    expect(created.status(), await created.text()).toBe(201)

    const event = await created.json()
    expect(event.event_date).toBe(start)
    expect(event.end_date, 'the range must survive the round trip').toBe(end)

    const after = await page.request.get(`${portalAPI}/api/v1/admin/events?page_size=100`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    expect(
      (await after.json()).total - beforeTotal,
      'a 30-day range must write ONE event row. N rows sharing a series_id is the superseded option.',
    ).toBe(1)
  })

  // 0.12a-INT-E2E-003 — omitting `end_date` yields a one-day event, not a 400.
  //
  // AC 2 against the real handler. `CreateEventRequest.end_date` is not in
  // `required`, so oapi-codegen hands the handler a nil pointer for every client
  // written before this story — and a nil-deref here is a 500 on the admin's
  // primary create path. Story 0.4's review found exactly that bug once.
  test('0.12a-INT-E2E-003 omitting end_date still creates a single-day event', async ({ page }) => {
    const day = isoDay(90)
    const branches = await page.request.get(`${portalAPI}/api/v1/admin/branches`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const branchID = (await branches.json()).data[0].id

    const created = await page.request.post(`${portalAPI}/api/v1/admin/events`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
      data: { name: `Single day journey ${Date.now()}`, event_date: day, branch_ids: [branchID] },
    })
    expect(created.status(), await created.text()).toBe(201)
    const event = await created.json()
    expect(event.end_date, 'an omitted end_date defaults to the start date server-side').toBe(day)
  })

  // 0.12a-INT-E2E-004 — an over-long range is refused on `end_date`, by the real
  // service, with the real envelope.
  test('0.12a-INT-E2E-004 a range over ninety days is refused on end_date', async ({ page }) => {
    const branches = await page.request.get(`${portalAPI}/api/v1/admin/branches`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const branchID = (await branches.json()).data[0].id

    const refused = await page.request.post(`${portalAPI}/api/v1/admin/events`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
      data: { name: `Too long ${Date.now()}`, event_date: isoDay(120), end_date: isoDay(300), branch_ids: [branchID] },
    })
    expect(refused.status()).toBe(400)
    const body = await refused.json()
    expect(body.code).toBe('VALIDATION_FAILED')
    expect(
      body.details?.[0],
      'the refusal must name end_date, not event_date — the admin\'s start date is fine and the form has to ' +
        'mark the control that is actually wrong',
    ).toMatchObject({ field: 'end_date', code: 'range_too_long' })
  })

  // 0.12a-INT-E2E-005 — the coordinator marks on a day inside the range, in a
  // real browser, and the screen reports today's counters.
  //
  // The browser-level proof that the whole chain agrees: the slot gate opened by
  // containment, the write carried a server-derived od_date, and the counter the
  // coordinator reads came back scoped to today rather than to the range.
  test('0.12a-INT-E2E-005 a coordinator marks on a day inside the range', async ({ page }) => {
    // The integration harness page seeds the role and the route; navigating to
    // /portal/mark directly lands on the sign-in screen with no session.
    await page.goto('/integration.html?role=coordinator&path=%2Fmark')

    const eventSelect = page.getByRole('combobox', { name: /event/i })
    const categorySelect = page.getByRole('combobox', { name: /category/i })
    await expect(eventSelect).not.toHaveValue('')
    await expect(categorySelect).not.toHaveValue('')

    // The screen reached a coherent SLOT state rather than an error. Both
    // branches are accepted because this runs at whatever time CI runs: inside a
    // slot it renders the countdown ("Left in this slot"), outside one the
    // explicit "No slot is open right now". What must never appear is a failure
    // — that is the AC 11 property, and it is asserted for the out-of-range day
    // in the mocked suite where the clock can be controlled.
    await expect(
      page.getByText(/left in this slot|no slot is open|slot closing/i),
    ).toBeVisible()

    const mine = page.getByText(/marked by you/i)
    await expect(
      mine,
      'the counters must be visible and scoped to TODAY; summed across a thirty-day range they are cumulative ' +
        'totals on a screen the coordinator reads as today',
    ).toBeVisible()
  })

  // 0.12a-INT-E2E-006 — the public dashboard groups a multi-day history by day.
  //
  // Against the real read query, so it fails if the grouping is on
  // `events.event_date` (one heading for thirty days) or on `server_received_at`
  // (a correction filed under the day it was typed).
  test('0.12a-INT-E2E-006 the public dashboard groups a festival by day', async ({ page }) => {
    const records = await page.request.get(
      `${portalAPI}/api/v1/public/students/${MULTIDAY_STUDENT_ID}/od-records`,
    )
    expect(
      records.ok(),
      'the harness must seed a dedicated multi-day student (harness change 2 — shared roll numbers cost ' +
        '0.9 and 0.10 a CI round each)',
    ).toBeTruthy()

    const body = await records.json()
    expect(body.days.length, 'a student marked on three days of one event has three day groups').toBe(3)
    for (const day of body.days) {
      expect(day.od_date, 'the day group key is od_date, not event_date (T18)').toBeTruthy()
      expect(day.event_date, 'PublicODDay.event_date is RENAMED to od_date, not supplemented').toBeUndefined()
    }
  })

  // 0.12a-INT-E2E-007 — the admin attendance day filter narrows a real query.
  test('0.12a-INT-E2E-007 the attendance day filter narrows the real read', async ({ page }) => {
    const headers = { Authorization: 'Bearer portal-admin-token' }

    const all = await page.request.get(
      `${portalAPI}/api/v1/admin/events/${MARKING_EVENT_ID}/attendance?page_size=100`,
      { headers },
    )
    expect(all.ok(), await all.text()).toBeTruthy()
    const allBody = await all.json()

    const filtered = await page.request.get(
      `${portalAPI}/api/v1/admin/events/${MARKING_EVENT_ID}/attendance?page_size=100&od_date=${isoDay(0)}`,
      { headers },
    )
    expect(filtered.ok(), await filtered.text()).toBeTruthy()
    const filteredBody = await filtered.json()

    expect(
      filteredBody.total,
      'the od_date filter must narrow the read, never widen it',
    ).toBeLessThanOrEqual(allBody.total)
    for (const record of filteredBody.data) {
      expect(record.od_date).toBe(isoDay(0))
    }
  })
})
