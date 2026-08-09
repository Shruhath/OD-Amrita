/**
 * Fixtures for Story 0.11's dispute surface — public submission and portal
 * resolution.
 *
 * Follows tests/support/factories/public-attendance.ts: fixed ids so assertions
 * can name them, and an `overrides` parameter on everything so a test states the
 * ONE field it cares about.
 *
 * TWO FAMILIES, DELIBERATELY SEPARATE. `publicDisputeReceipt` mirrors what the
 * OPEN INTERNET receives; `portalDispute` mirrors what an authenticated teacher
 * receives. They overlap today, which is exactly why they are not one factory: a
 * shared fixture makes the disclosure bug — the receipt growing a resolution
 * note, or a submitted email — pass every mocked test in the suite. The public
 * receipt has no `submitted_email` and no `resolution_note`, and it must never
 * acquire either (AC 10).
 *
 * The dispute student REUSES the 0.10 public dashboard student. The form is
 * reached from a slot on that student's record and pre-fills read-only from the
 * records response already in the page (T4), so inventing a second student here
 * would test a form nobody can open.
 */

import type { Dispute } from '../../../src/api/client'
import { publicStudent, publicStudentID, publicStudentRoll } from './public-attendance'

export const disputeRoute = '/attendance'
export const portalDisputesRoute = '/portal/disputes'

export const disputeID = '97000000-0000-4000-8000-000000000001'
/** T19: the reference is the dispute uuid's first 8 characters. No sequence, no
 *  counter, no second id column. */
export const disputeReference = disputeID.slice(0, 8)

/** The institutional address for the fixture student (T1):
 *  lowercase(roll_no)@cb.students.amrita.edu. */
export const disputeEmail = `${publicStudentRoll.toLowerCase()}@cb.students.amrita.edu`

export const disputeEventID = '93000000-0000-4000-8000-000000000001'
export const disputeCategoryID = '94000000-0000-4000-8000-000000000001'

/**
 * The day the disputed OD belongs to.
 *
 * Required on the submission by the 2026-08-06 multi-day amendment
 * (docs/multi-day-events-plan.md §7). The multi-day change turns an
 * event into a real date range, after which "I was missed in slot 8" is ambiguous
 * across thirty days — so the form carries the day, constrained to the event's
 * range, and the column exists from the moment the table is created. Today an
 * event is one day, so this equals the event date; what changes under 0.12a is
 * the CONSTRAINT, not the field.
 */
export const disputeODDate = '2026-07-28'

/** NFR50's copy is FIXED. The test asserts this string, not a paraphrase of it. */
export const disputeConfirmationCopy = 'Your dispute has been submitted. A teacher will review it.'

export type PublicEventCategory = { id: string; name: string }

export type PublicEvent = {
  event_id: string
  name: string
  /** The FIRST day of the event's range. */
  event_date: string
  /** The LAST day. Added under Story 0.12a (T19): the day picker enumerates
   *  event_date..end_date inclusive, so without it the control has exactly one
   *  option and a student missed on day 17 of a festival cannot report it. */
  end_date: string
  categories: PublicEventCategory[]
}

export type PublicDisputeReceipt = {
  dispute_id: string
  reference: string
  status: 'open'
  submitted_at: string
  /** The SERVER-resolved student (FR42). Proof the auto-population came from the
   *  database rather than from the request body. */
  student: ReturnType<typeof publicStudent>
}

/**
 * The authenticated inbox row, typed from the CONTRACT rather than by hand.
 *
 * The hand-written version had drifted: it declared `resolved_by_name` (which the
 * API never sends) and omitted `reference` and `od_date` (both required on
 * `Dispute`). The component papered over the gap with `row.reference || …` and
 * `row.od_date || row.event_date` fallbacks, so the mocked tests never exercised
 * the server-supplied reference (T19) or the disputed day at all — a server that
 * stopped sending either would still have rendered plausibly.
 */
export type PortalDispute = Dispute

export function publicEvent(overrides: Partial<PublicEvent> = {}): PublicEvent {
  const eventDate = overrides.event_date ?? '2026-07-28'
  return {
    event_id: disputeEventID,
    name: 'Onam Celebrations',
    event_date: eventDate,
    // Defaults to the start date so the existing single-day fixtures keep
    // testing what they were written to test. `end_date` deriving from
    // `event_date` rather than being hardcoded stops an override producing a
    // backwards range the real database would refuse.
    end_date: eventDate,
    categories: [{ id: disputeCategoryID, name: 'Procession' }],
    ...overrides,
  }
}

/**
 * The thirty-day festival, for the day-picker assertions.
 *
 * 10 Aug -> 8 Sep inclusive is 30 options. A picker that renders one option, or
 * 29, or 31, is a different bug each time and the count distinguishes them.
 *
 * The id is deliberately the SAME as public-attendance.ts's
 * `publicMultiDayEventID`: this is the one event seen from two places, the
 * record history and the public events list. Giving it two ids would leave the
 * merge between those two sources untestable, because nothing would ever match
 * — and the day picker would silently keep the single day the records know
 * instead of the range only the public list carries.
 */
export const multiDayDisputeEventID = '93000000-0000-4000-8000-000000000002'
export const multiDayDisputeStart = '2026-08-10'
export const multiDayDisputeEnd = '2026-09-08'
export const multiDayDisputeDayCount = 30

export function multiDayPublicEvent(overrides: Partial<PublicEvent> = {}): PublicEvent {
  return publicEvent({
    event_id: multiDayDisputeEventID,
    name: 'Amritotsavam',
    event_date: multiDayDisputeStart,
    end_date: multiDayDisputeEnd,
    ...overrides,
  })
}

/**
 * The public event list.
 *
 * The archived event is in here on purpose (T8). A student who was never marked
 * is the person this endpoint exists for, and by the time they notice, the event
 * is very often already archived — filtering it out would hide exactly the event
 * being disputed.
 */
export function publicEventCollection(events: PublicEvent[] = [
  publicEvent(),
  publicEvent({
    event_id: '93000000-0000-4000-8000-000000000003',
    name: 'Sports Day Relay (archived)',
    event_date: '2026-07-20',
    categories: [{ id: '94000000-0000-4000-8000-000000000003', name: 'Athletics' }],
  }),
]) {
  return { data: events }
}

export function publicDisputeReceipt(overrides: Partial<PublicDisputeReceipt> = {}): PublicDisputeReceipt {
  return {
    dispute_id: disputeID,
    reference: disputeReference,
    status: 'open',
    submitted_at: '2026-07-28T17:05:00Z',
    student: publicStudent(),
    ...overrides,
  }
}

export function portalDispute(overrides: Partial<PortalDispute> = {}): PortalDispute {
  return {
    dispute_id: disputeID,
    status: 'open',
    // Always present, and null exactly while the dispute is open — the same
    // contract the wire has. A factory that omitted it would let a component
    // test pass against a row the server can never send.
    outcome: null,
    submitted_email: disputeEmail,
    submitted_at: '2026-07-28T17:05:00Z',
    slot: 3,
    event_id: disputeEventID,
    event_name: 'Onam Celebrations',
    event_date: '2026-07-28',
    category_id: disputeCategoryID,
    category_name: 'Procession',
    claimed_coordinator_name: null,
    student_note: 'I was at the procession but my card did not scan.',
    student: publicStudent(),
    resolution_note: null,
    resolved_by_user_id: null,
    resolved_at: null,
    reference: disputeReference,
    od_date: '2026-07-28',
    ...overrides,
  }
}

export function portalDisputeCollection(data: PortalDispute[] = [portalDispute()]) {
  return { data, total: data.length, page: 1, page_size: 20 }
}

/** The typed error envelope every dispute refusal returns. */
export function disputeError(code: string, message: string) {
  return { code, message }
}

/**
 * The ONE identity refusal (T3).
 *
 * A bad domain, an unknown address and an email/selection mismatch all return
 * THIS EXACT VALUE. Exported as a single frozen object rather than as a factory
 * so a test cannot accidentally hand the three paths three slightly different
 * bodies and then "prove" the UI treats them alike.
 */
export const identityUnverified = Object.freeze(
  disputeError(
    'DISPUTE_IDENTITY_UNVERIFIED',
    'We could not verify that email against the roster. Check that you used your university address.',
  ),
)

export { publicStudentID, publicStudentRoll }
