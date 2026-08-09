import { expectSomethingSelected, selectedValue } from '../support/select'
import { expect, test } from '@playwright/test'
import { installFakeBarcodeScanner } from '../e2e/support/barcode'
import { installFakeWebNfc } from '../e2e/support/web-nfc'

/**
 * The real-stack coordinator browser journey.
 *
 * Runs the real `web/` app against the real Go API and real Postgres through
 * `api/test/portal/main.go`. This is the level that catches the failures the
 * mocked suite cannot see: a route mounted on the wrong gin group, a scoped read
 * that 403s a coordinator, a counter query that counts the wrong rows, and a
 * slot window that disagrees with the server's own FR12 decision.
 *
 * TWO HARNESS FACTS these depend on, both seeded by api/test/portal/main.go:
 *
 *  1. A user row with `role = 'coordinator'` behind `portal-coordinator-token`.
 *     Production provisions everyone as `student`, so the harness seeds this
 *     principal explicitly.
 *  2. An active event dated TODAY. `odrecord.ActiveSlot` compares against
 *     `events.event_date` in Asia/Kolkata, so an event dated any other day makes
 *     every slot assertion vacuously "no slot open". The harness recomputes the
 *     date on every start rather than hardcoding one that would go stale.
 *
 * The radio and the camera are faked — no hardware in CI, ever. The real Android
 * tap and the real barcode decode are recorded by hand, not here.
 */

const portalAPI = 'http://127.0.0.1:8081'

/** The harness's marking event — the one seeded dated TODAY in Asia/Kolkata. */
const MARKING_EVENT_ID = '93000000-0000-4000-8000-000000000001'

/**
 * Reads the scope the screen actually settled on.
 *
 * Waits for both selects to hold a real id first: reading them the instant the
 * page loads returns empty strings, and a status request scoped to `''` answers
 * 400 — which would then be mistaken for "a slot is open" and send the test down
 * the wrong branch.
 */
async function selectedScope(page: import('@playwright/test').Page) {
  const eventSelect = page.getByRole('combobox', { name: /event/i })
  const categorySelect = page.getByRole('combobox', { name: /category/i })
  await expectSomethingSelected(eventSelect)
  await expectSomethingSelected(categorySelect)
  return {
    eventID: await selectedValue(eventSelect),
    categoryID: await selectedValue(categorySelect),
  }
}

/** Opens the marking screen as a coordinator through the memory-router harness. */
async function openMarkingScreen(page: import('@playwright/test').Page) {
  await page.goto('/integration.html?role=coordinator&path=%2Fmark')
  await expect(page.getByRole('heading', { name: /mark/i })).toBeVisible()
}

test.describe('real stack coordinator marking', () => {
  test('[P0] the scoped reads answer a coordinator and exclude archived events', async ({ request }) => {
    // The API answers the coordinator directly: this is the assertion that fails
    // loudly if the routes were mounted under /admin.
    const events = await request.get(`${portalAPI}/api/v1/events`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    expect(events.status(), 'a coordinator must not be 403 on their own scoped read').toBe(200)

    const body = await events.json()
    expect(Array.isArray(body.data)).toBe(true)
    for (const event of body.data) {
      expect(event.status, 'the selector must never offer an unmarkable event').toBe('active')
    }
  })

  test('[P0] an admin principal is refused the coordinator marking reads', async ({ request }) => {
    // C2: teacher/admin marking is Story 0.9. An admin token must not open this
    // surface just because admins can do everything else.
    const status = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=00000000-0000-4000-8000-000000000000&category_id=00000000-0000-4000-8000-000000000000`,
      { headers: { Authorization: 'Bearer portal-admin-token' } },
    )
    expect([403, 404]).toContain(status.status())
  })

  test('[P0] search hits real Postgres and returns no email or token', async ({ request }) => {
    const response = await request.get(`${portalAPI}/api/v1/students/search?q=CB.SC.U4CSE24`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    expect(response.status()).toBe(200)

    const body = await response.json()
    expect(body.data.length).toBeGreaterThan(0)

    const serialized = JSON.stringify(body)
    expect(serialized).not.toContain('email')
    expect(serialized).not.toContain('token')
    expect(serialized).not.toContain('fingerprint')

    // A query below the minimum is a typed 400, not an unbounded roster dump.
    const short = await request.get(`${portalAPI}/api/v1/students/search?q=ab`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    expect(short.status()).toBe(400)
  })

  test('[P0] a real mark moves the real counters, and a closed slot moves neither', async ({ page, request }, testInfo) => {
    await installFakeWebNfc(page)
    await openMarkingScreen(page)

    // A DIFFERENT student per attempt. The harness clears this event's
    // od_records once at boot, not per test, and CI runs with `retries: 2` — so
    // re-marking the same student in the same slot returns SCAN_DUPLICATE and
    // the retry could only ever fail, turning any flake into a hard red. Three
    // roster students are seeded, which covers the two retries.
    const rolls = ['CB.SC.U4CSE24001', 'CB.SC.U4CSE24002', 'CB.SC.U4CSE24003']
    const roll = rolls[testInfo.retry % rolls.length]

    const { eventID, categoryID } = await selectedScope(page)

    const before = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${eventID}&category_id=${categoryID}`,
      { headers: { Authorization: 'Bearer portal-coordinator-token' } },
    )
    expect(before.status(), 'the counter baseline must be a real read, not an error envelope').toBe(200)
    const beforeBody = await before.json()

    // Mark one student through the search path — it needs no card to have been
    // written, so it does not depend on a physically written card.
    await page.getByRole('button', { name: /search/i }).click()
    await page.getByRole('searchbox').fill(roll)
    await page.getByRole('button', { name: new RegExp(roll.replace(/\./g, '\\.')) }).click()

    const after = async () => {
      const response = await request.get(
        `${portalAPI}/api/v1/mark/status?event_id=${eventID}&category_id=${categoryID}`,
        { headers: { Authorization: 'Bearer portal-coordinator-token' } },
      )
      return response.json()
    }

    // BOTH branches are real assertions, and which one runs is decided by the
    // SERVER's own slot answer rather than by the wall clock here. Slots run
    // 08:00-18:15 IST, so a suite that only asserted the green path would fail
    // every evening — and one that skipped outside those hours would quietly
    // prove nothing for most of the day.
    if (beforeBody.slot === null) {
      // FR12: a mark outside every slot is REFUSED, and a refusal must not move
      // a counter. This is the stronger of the two assertions.
      await expect(page.getByRole('alert')).toBeVisible()
      await expect(page.getByRole('alert')).toContainText(/slot/i)
      await expect(page.getByRole('status')).toHaveCount(0)

      const afterBody = await after()
      expect(afterBody.counters.mine).toBe(beforeBody.counters.mine)
      expect(afterBody.counters.category_total).toBe(beforeBody.counters.category_total)
      return
    }

    await expect(page.getByRole('status')).toBeVisible()

    const afterBody = await after()
    expect(afterBody.counters.mine).toBe(beforeBody.counters.mine + 1)
    expect(afterBody.counters.category_total).toBe(beforeBody.counters.category_total + 1)
  })

  test('[P0] the slot window comes from the server and matches its own clock', async ({ request }) => {
    const events = await request.get(`${portalAPI}/api/v1/events`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    // Pinned to the harness's own marking event rather than taking data[0].
    // /api/v1/events is unscoped and ordered event_date DESC, and other specs
    // create active events dated later through the same harness — with
    // fullyParallel they can interleave and take first place, so data[0] would
    // assert against a row this test knows nothing about.
    const all = (await events.json()).data as { id: string; event_date: string; end_date: string }[]
    const event = all.find((candidate) => candidate.id === MARKING_EVENT_ID)
    if (!event) {
      throw new Error(
        `the harness marking event ${MARKING_EVENT_ID} is not in the coordinator's list ` +
        `(got ${all.map((candidate) => candidate.id).join(', ') || 'nothing'}): see api/test/portal/main.go`,
      )
    }
    const categories = await request.get(`${portalAPI}/api/v1/events/${event.id}/categories`, {
      headers: { Authorization: 'Bearer portal-coordinator-token' },
    })
    const [category] = (await categories.json()).data

    const response = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${event.id}&category_id=${category.id}`,
      { headers: { Authorization: 'Bearer portal-coordinator-token' } },
    )
    expect(response.status()).toBe(200)
    const body = await response.json()

    expect(typeof body.server_time).toBe('string')
    expect(body.counters).toHaveProperty('mine')
    expect(body.counters).toHaveProperty('category_total')

    // The precondition this test is really guarding: an event whose days do not
    // include today can NEVER have an open slot, which would make every
    // assertion below vacuous. Checked directly, so it holds at any hour rather
    // than only during the 08:00-18:15 IST slot window.
    //
    // The rule is CONTAINMENT, not equality, since an event became a real date
    // range: the harness seeds yesterday -> tomorrow deliberately, so an
    // equality check here would fail on a correctly seeded harness while still
    // passing on a single-day one — the exact inversion of what it is for.
    // `en-CA` yields YYYY-MM-DD directly, in the requested zone.
    //
    // The obvious-looking `new Date(d.toLocaleString('en-US', { timeZone }))`
    // followed by `.toISOString().slice(0, 10)` is WRONG and was a time bomb:
    // the locale string is re-parsed as LOCAL time and `toISOString` then
    // converts it back to UTC, cancelling the shift. It agrees with IST only
    // while the IST and UTC dates happen to match, so it passed all day and
    // failed between 00:00 and 05:30 IST — which is exactly when it was caught.
    const todayIST = new Date().toLocaleDateString('en-CA', { timeZone: 'Asia/Kolkata' })
    expect(
      event.event_date <= todayIST && event.end_date >= todayIST,
      `the harness event's range must CONTAIN today in Asia/Kolkata or no slot can ever open ` +
        `(got ${event.event_date}..${event.end_date}, today is ${todayIST}): see api/test/portal/main.go`,
    ).toBe(true)

    // `slot` is REQUIRED and nullable: always present, null when nothing is open.
    // An omitted key would mean a client cannot tell "no slot" from "old server".
    expect(body).toHaveProperty('slot')

    if (body.slot === null) {
      // Outside 08:00-18:15 IST there is genuinely no slot. That is the state,
      // not a failure — and it must carry no stale window with it.
      return
    }

    expect(body.slot.slot).toBeGreaterThanOrEqual(1)
    expect(body.slot.slot).toBeLessThanOrEqual(12)

    const serverTime = new Date(body.server_time).getTime()
    const startedAt = new Date(body.slot.started_at).getTime()
    const endsAt = new Date(body.slot.ends_at).getTime()

    // The client's countdown is ends_at minus the server/device offset captured
    // at this same read. If server_time fell outside its own window, the
    // countdown would start negative or count a slot that is already closed.
    expect(serverTime).toBeGreaterThanOrEqual(startedAt)
    expect(serverTime).toBeLessThan(endsAt)
  })

  test('[P0] a camera decode resolves a real roll number through the real stack', async ({ page, request }) => {
    await installFakeBarcodeScanner(page)
    await openMarkingScreen(page)

    const { eventID, categoryID } = await selectedScope(page)
    const status = await request.get(
      `${portalAPI}/api/v1/mark/status?event_id=${eventID}&category_id=${categoryID}`,
      { headers: { Authorization: 'Bearer portal-coordinator-token' } },
    )
    const slotOpen = (await status.json()).slot !== null

    await page.getByRole('button', { name: /camera|scan/i }).click()

    // The widened identity pairing, exercised end to end: the printed barcode
    // carries a ROLL NUMBER, the client resolves it through the real search
    // endpoint, and the mark goes out as `camera` — not silently as `search`,
    // which the later shared camera+search credit cap would then mis-count.
    await page.evaluate(() => {
      ;(window as unknown as { __decodeBarcode: (text: string) => void }).__decodeBarcode('CB.SC.U4CSE24002')
    })

    if (!slotOpen) {
      // Outside slot hours the server refuses the mark (FR12). The decode and the
      // roll resolution still had to succeed to get that far, which is what this
      // test is about — a decode that failed would render a "code not recognised"
      // error instead.
      await expect(page.getByRole('alert')).toBeVisible()
      await expect(page.getByRole('alert')).toContainText(/slot/i)
      await expect(page.getByRole('alert')).not.toContainText(/not recognised|not recognized/i)
      return
    }

    await expect(page.getByRole('status')).toBeVisible()
    await expect(page.getByRole('status')).toContainText(/CB\.SC\.U4CSE24002|Rao/i)
  })
})
