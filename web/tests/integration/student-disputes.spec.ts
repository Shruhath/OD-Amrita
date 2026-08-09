import { expect, test } from '@playwright/test'

/**
 * The real-stack dispute journey — the first UNAUTHENTICATED WRITE run end to
 * end against the real Go API and real Postgres.
 *
 * Uses the same `/integration.html?role=X&path=Y` harness entry every other spec
 * in this directory uses. `role=none` maps to a NULL token in
 * tests/integration/main.tsx, which is the whole point: this is the only WRITE in
 * the system that is supposed to work with no credential, so it is also the only
 * one that can prove the public POST is mounted as a sibling of /api/v1 rather
 * than inside it.
 *
 * ---------------------------------------------------------------------------
 * HARNESS CHANGES THIS SPEC REQUIRES. All are part of Story 0.11 and none exist
 * yet — a run before they land fails HERE rather than in the feature, so read
 * this before debugging a red.
 *
 *  1. `api/test/portal/main.go` must register the dispute routes exactly as
 *     cmd/api does: the public POST on the group built from the ROOT router with
 *     its own stricter limiter as route-level middleware, and the authenticated
 *     list/resolution on `markAPI`. A harness missing a route turns a real
 *     composition bug into a browser 404 that reads as a frontend defect — 0.9's
 *     flagship failure.
 *
 *  2. seed() must seed DEDICATED fixtures, disjoint from every pool another spec
 *     owns — 24001-003 (coordinator-marking), 24004-005 (capture-credits),
 *     24006-007 (teacher-admin-attendance), 24008-009 (0.10 public dashboard):
 *       - student CB.SC.U4CSE24010 with a roll number whose derived institutional
 *         address is cb.sc.u4cse24010@cb.students.amrita.edu (T1) and at least
 *         one ABSENT slot on the dispute event;
 *       - a DEDICATED dispute event, so the counts in the web-deploy gate are
 *         fixed rather than shared with the 0.10 read fixtures;
 *       - `DELETE FROM disputes` for that event on every harness start, or a
 *         rerun hits the partial unique index and every submission 409s.
 *     ORDER MATTERS, exactly as it did for 0.10: seed() deletes od_records for
 *     every CB.SC.U4CSE%/ECE% student near the top and for the marking event near
 *     the bottom. These fixtures go in AFTER both.
 *
 *  3. The harness must raise PUBLIC_DISPUTE_RATE_LIMIT_PER_MINUTE. The production
 *     default is FIVE a minute and the portal-integration job runs every spec
 *     from one client IP, so the real default would refuse this suite's own
 *     submissions. The limiter's real behaviour is proven deterministically in
 *     api/internal/middleware/ratelimit and in the route-composition tests.
 * ---------------------------------------------------------------------------
 *
 * Deliberately NO 429 burst test here, for the same reason 0.10 has none: the
 * limiter is keyed by client IP and every request in this job shares one, so a
 * burst would poison the bucket for every other assertion in the same run.
 */

const portalAPI = 'http://127.0.0.1:8081'

// Seeded by api/test/portal/main.go — see harness change 2 above.
const DISPUTE_STUDENT_ROLL = 'CB.SC.U4CSE24010'
const DISPUTE_STUDENT_ID = '95000000-0000-4000-8000-000000000011'
const DISPUTE_EMAIL = 'cb.sc.u4cse24010@cb.students.amrita.edu'
const DISPUTE_EVENT_NAME = 'Browser Dispute Event'
const DISPUTE_EVENT_ID = '93000000-0000-4000-8000-000000000004'
const DISPUTE_CATEGORY_ID = '94000000-0000-4000-8000-000000000004'
/** The slot the fixture leaves empty — the one a student would dispute. */
const DISPUTE_SLOT = 3
/**
 * The day the disputed OD belongs to — the fixture event's date.
 *
 * Required from table creation by the 2026-08-06 multi-day amendment
 * (docs/multi-day-events-plan.md §7): an event becomes a real date range, and a dispute
 * without a day is then ambiguous across thirty of them.
 */
const DISPUTE_OD_DATE = '2026-07-28'

function submission(overrides: Record<string, unknown> = {}) {
  return {
    student_id: DISPUTE_STUDENT_ID,
    submitted_email: DISPUTE_EMAIL,
    event_id: DISPUTE_EVENT_ID,
    category_id: DISPUTE_CATEGORY_ID,
    od_date: DISPUTE_OD_DATE,
    slot: DISPUTE_SLOT,
    ...overrides,
  }
}

async function openPublicDashboard(page: import('@playwright/test').Page, query = '') {
  await page.goto(`/integration.html?role=none&path=%2Fattendance${query}`)
  await expect(page.getByRole('searchbox', { name: /roll number|name|student/i })).toBeVisible()
}

test.describe('real stack student disputes', () => {
  // 0.11-RS-001 — the load-bearing one, asserted at the transport level before
  // any UI is involved: the public POST answers with NO Authorization header,
  // while the authenticated dispute routes still refuse one (AC 20).
  //
  // This is what catches a POST registered on markAPI, or a public group built
  // with api.Group("/public") — identical URLs, inherited auth middleware, and a
  // dead form for every student on campus.
  test('[P0] the public dispute POST answers anonymously while the authenticated routes refuse', async ({ request }) => {
    const events = await request.get(`${portalAPI}/api/v1/public/events`)
    expect(
      events.status(),
      'GET /api/v1/public/events with no token must be 200. A 401 means the route was mounted inside the ' +
        'authenticated group — same URL, inherited middleware',
    ).toBe(200)

    const created = await request.post(`${portalAPI}/api/v1/public/disputes`, { data: submission({ slot: 10 }) })
    expect(
      created.status(),
      'POST /api/v1/public/disputes with no token must be 201. A 401 means the only unauthenticated WRITE in ' +
        'the system was registered on a group that carries auth middleware',
    ).toBe(201)

    // The authenticated side is unchanged by opening the public one.
    const list = await request.get(`${portalAPI}/api/v1/disputes`)
    expect(list.status(), 'GET /api/v1/disputes answered without a token — it returns other students\' emails').toBe(401)

    const resolve = await request.post(
      `${portalAPI}/api/v1/disputes/00000000-0000-4000-8000-000000000000/resolution`,
      { data: { resolution_note: 'no token' } },
    )
    expect([401, 403]).toContain(resolve.status())

    // And 0.10's surface still behaves.
    const authenticatedSearch = await request.get(`${portalAPI}/api/v1/students/search?q=${DISPUTE_STUDENT_ROLL}`)
    expect(authenticatedSearch.status()).toBe(401)
  })

  // 0.11-RS-002 — the receipt discloses nothing, asserted against the REAL
  // serialiser rather than a fixture (AC 10).
  //
  // The contract test proves the SCHEMA has no such field. This proves the
  // running server does not put one on the wire anyway — which is a different
  // claim, and the one that matters when a handler maps a struct by hand.
  test('[P0] the receipt carries no email, no note and no actor id', async ({ request }) => {
    const response = await request.post(`${portalAPI}/api/v1/public/disputes`, {
      data: submission({ slot: 6 }),
    })
    expect(response.status()).toBe(201)

    const body = await response.text()
    const parsed = JSON.parse(body)

    // ALLOWLISTS at every level. A substring blocklist alone can only ever catch
    // the names somebody already thought of.
    expect(Object.keys(parsed).sort()).toEqual(['dispute_id', 'reference', 'status', 'student', 'submitted_at'])
    expect(Object.keys(parsed.student).sort()).toEqual(
      ['batch', 'branch', 'display_name', 'roll_no', 'section', 'student_id'],
    )
    expect(parsed.status).toBe('open')

    // FR42: the display fields came from the DATABASE, not from the request. The
    // request never carried a name, so a name coming back proves the lookup ran.
    expect(parsed.student.roll_no).toBe(DISPUTE_STUDENT_ROLL)

    // The submitter's own address must not be echoed (AC 10) — a receipt that
    // repeats it back is a confirmation that it resolved.
    for (const forbidden of [DISPUTE_EMAIL, '@amrita.edu', 'submitted_email', 'resolution_note', 'user_id', 'token', 'fingerprint']) {
      expect(body, `the public dispute receipt contains ${forbidden}`).not.toContain(forbidden)
    }
  })

  // 0.11-RS-003 — the three identity refusals are BYTE-IDENTICAL on the wire
  // (AC 3).
  //
  // The service unit test compares the error VALUES. This compares the serialised
  // RESPONSES, which is what an attacker actually sees — including the status
  // line, which a handler could still branch on after the service did not.
  test('[P0] a bad domain, an unknown address and a mismatch return identical responses', async ({ request }) => {
    const cases = [
      { name: 'domain outside the amrita family', data: submission({ submitted_email: 'someone@gmail.com' }) },
      { name: 'well-formed address matching no roll', data: submission({ submitted_email: 'cb.sc.u4cse24999@cb.students.amrita.edu' }) },
      { name: 'email resolves to a different student', data: submission({ student_id: '95000000-0000-4000-8000-000000000009' }) },
    ]

    const observed: { status: number; body: string }[] = []
    for (const testCase of cases) {
      const response = await request.post(`${portalAPI}/api/v1/public/disputes`, { data: testCase.data })
      observed.push({ status: response.status(), body: await response.text() })
    }

    for (const [index, result] of observed.entries()) {
      expect(result.status, `${cases[index].name} was not refused with 400`).toBe(400)
      expect(JSON.parse(result.body).code).toBe('DISPUTE_IDENTITY_UNVERIFIED')
    }

    expect(
      observed[1].body,
      'the "unknown address" refusal differs from the "bad domain" refusal. Two different answers is a ' +
        'directory oracle: anyone on the internet can then ask whether an address exists and read it off the ' +
        'response (T3)',
    ).toBe(observed[0].body)
    expect(
      observed[2].body,
      'the "email resolves to a different student" refusal differs from the others (T5)',
    ).toBe(observed[0].body)
    expect(observed[1].status).toBe(observed[0].status)
    expect(observed[2].status).toBe(observed[0].status)
  })

  // 0.11-RS-004 — the whole browser journey, anonymously, writing a real row
  // (AC 1, 2, 15).
  test('[P0] an anonymous visitor files a dispute for a missed slot and sees the confirmation', async ({ page }) => {
    await openPublicDashboard(page, `&student=${DISPUTE_STUDENT_ID}`)

    await expect(page.getByRole('button', { name: /sign in|continue with microsoft/i })).toHaveCount(0)

    // The slot being disputed has no row on the dashboard — that is the whole
    // point of it — so it is named INSIDE the form. Which is also the only place
    // it can be named for a day the student has no rows on at all.
    await expect(
      page.getByRole('listitem').filter({ hasText: new RegExp(`slot\\s*${DISPUTE_SLOT}\\b`, 'i') }),
    ).toHaveCount(0)

    await page.getByRole('button', { name: /report a missed slot/i }).click()

    const form = page.getByRole('form', { name: /report a missed slot|dispute/i })
    await expect(form).toBeVisible()

    await form.getByRole('combobox', { name: /slot/i }).click()
    await page.getByRole('listbox').locator(`[data-value="${DISPUTE_SLOT}"]`).click()

    // Read-only and server-sourced (AC 1).
    await expect(form).toContainText(new RegExp(DISPUTE_STUDENT_ROLL.replace(/\./g, '\\.'), 'i'))

    await form.getByRole('textbox', { name: /email/i }).fill(DISPUTE_EMAIL)
    await form.getByRole('button', { name: /submit|send|report/i }).click()

    // Scoped to the receipt panel. A bare getByRole('status') also matches
    // 0.10's "As of" live region, and that collision was previously resolved by
    // deleting 0.10's role rather than by narrowing the locator.
    await expect(
      page.getByRole('region', { name: /dispute receipt/i }).getByRole('status'),
    ).toContainText(/Your dispute has been submitted\. A teacher will review it\./i)
  })

  // 0.11-RS-005 — a second submission for the same slot is refused by the
  // DATABASE and names the existing reference (AC 6).
  //
  // Run through the API rather than the browser so the assertion is on the status
  // and the body rather than on copy, and so it cannot be satisfied by a UI that
  // merely disables the button.
  test('[P0] a duplicate submission is a 409 that names the existing reference', async ({ request }) => {
    const first = await request.post(`${portalAPI}/api/v1/public/disputes`, { data: submission({ slot: 8 }) })
    expect(first.status()).toBe(201)
    const reference = (await first.json()).reference as string

    const second = await request.post(`${portalAPI}/api/v1/public/disputes`, { data: submission({ slot: 8 }) })
    expect(
      second.status(),
      'the duplicate was accepted. The guard is the partial unique index, not an application-level check ' +
        '(T11) — and this endpoint is reachable by anyone on the internet, so the race is not theoretical',
    ).toBe(409)

    const body = await second.json()
    expect(body.code).toBe('DISPUTE_ALREADY_OPEN')
    expect(
      body.message,
      'the 409 does not quote the existing reference; without it the student has nothing to hold on to and ' +
        'simply submits again',
    ).toContain(reference)
  })

  // 0.11-RS-006 — a teacher APPROVES, and the slot is credited against the real
  // stack (AC 11, 12, 14).
  //
  // This test used to assert the opposite: that resolving never moved the
  // attendance total. That WAS the defect — a teacher agreed a student had been
  // missed, and the student stayed missing, because crediting the slot was a
  // separate trip to another surface that nothing prompted. The attendance total
  // is still read before and after through the admin endpoint; it now has to go
  // UP by exactly one.
  test('[P0] a teacher approves a dispute and the slot is credited', async ({ page, request }) => {
    const created = await request.post(`${portalAPI}/api/v1/public/disputes`, { data: submission({ slot: 9 }) })
    expect(created.status()).toBe(201)
    const reference = (await created.json()).reference as string

    const before = await request.get(`${portalAPI}/api/v1/admin/events/${DISPUTE_EVENT_ID}/attendance`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    expect(before.status()).toBe(200)
    const beforeTotal = (await before.json()).total

    await page.goto('/integration.html?role=teacher&path=%2Fportal%2Fdisputes')

    const row = page.getByRole('button', { name: new RegExp(reference, 'i') })
    await expect(row).toHaveCount(1)
    await row.click()

    const detail = page.getByRole('region', { name: new RegExp(reference, 'i') })
    await expect(detail).toContainText(new RegExp(DISPUTE_STUDENT_ROLL.replace(/\./g, '\\.'), 'i'))
    await expect(detail).toContainText(new RegExp(DISPUTE_EVENT_NAME, 'i'))
    // The copy must still say what the button does to the student's attendance —
    // only the true statement has changed.
    await expect(detail).toContainText(/marks the student present|mark(ed)? present/i)

    await detail.getByRole('textbox', { name: /note|resolution/i }).fill('Verified against the register.')
    await detail.getByRole('button', { name: /approve/i }).click()

    await expect(detail).toContainText(/approved/i)

    const after = await request.get(`${portalAPI}/api/v1/admin/events/${DISPUTE_EVENT_ID}/attendance`, {
      headers: { Authorization: 'Bearer portal-admin-token' },
    })
    const afterBody = await after.json()
    expect(
      afterBody.total,
      'approving a dispute did not create the attendance record. The student checks the public dashboard, and ' +
        'an approval they cannot see is indistinguishable from one that never happened',
    ).toBe(beforeTotal + 1)
    // And the record says HOW it came to exist, rather than posing as a search.
    const credited = afterBody.data.find((record: { slot: number }) => record.slot === 9)
    expect(credited, 'no attendance record for the disputed slot').toBeTruthy()
    expect(credited.capture_method).toBe('dispute')
  })

  // 0.11-RS-007 — a coordinator cannot read the dispute inbox (AC 11).
  //
  // At the transport level, so it cannot be satisfied by a UI that merely hides
  // the link.
  test('[P0] a coordinator is refused the dispute list', async ({ request }) => {
    const response = await request.get(`${portalAPI}/api/v1/disputes`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    expect(response.status(), 'a coordinator read the dispute inbox, which carries every submitted email').toBe(403)
    expect(await response.text()).not.toContain(DISPUTE_EMAIL)
  })

  // 0.11-RS-008 — the public event read discloses only the four allowed fields
  // (AC 8).
  test('[P0] the public event list carries no branch, coordinator, count or archive metadata', async ({ request }) => {
    const response = await request.get(`${portalAPI}/api/v1/public/events`)
    expect(response.status()).toBe(200)

    const body = await response.text()
    const parsed = JSON.parse(body)
    expect(Object.keys(parsed)).toEqual(['data'])
    expect(parsed.data.length).toBeGreaterThan(0)

    for (const event of parsed.data) {
      // `end_date` joined the shape when an event became a real date range: the
      // dispute form enumerates event_date..end_date as its bounded day options,
      // and without the range on the wire a student missed on day 17 of a
      // festival has no way to report it. Still an exact set.
      expect(Object.keys(event).sort()).toEqual(['categories', 'end_date', 'event_date', 'event_id', 'name'])
      for (const category of event.categories) {
        expect(Object.keys(category).sort()).toEqual(['id', 'name'])
      }
    }

    for (const forbidden of ['branch', 'coordinator', 'archived_at', 'archived_by', 'status', 'count', 'credit', '@amrita.edu']) {
      expect(body, `the public event list contains ${forbidden}`).not.toContain(forbidden)
    }
  })

  // 0.11-RS-009 — the 0.10 read surface is unchanged and still green (AC 17).
  //
  // This story puts a POST behind the door 0.10 opened. The regression to catch
  // is the reads breaking as a side effect — a shared limiter, a changed group,
  // or a schema edit that widened a Public* type.
  test('[P0] the 0.10 public reads still answer anonymously with their original shape', async ({ request }) => {
    const search = await request.get(`${portalAPI}/api/v1/public/students/search?q=${DISPUTE_STUDENT_ROLL}`)
    expect(search.status()).toBe(200)

    const records = await request.get(`${portalAPI}/api/v1/public/students/${DISPUTE_STUDENT_ID}/od-records`)
    expect(records.status()).toBe(200)

    const parsed = JSON.parse(await records.text())
    expect(
      Object.keys(parsed).sort(),
      'the 0.10 records response shape changed; its contract and conservation gates must stay green (AC 17)',
    ).toEqual(['days', 'generated_at', 'student'])
  })
})
