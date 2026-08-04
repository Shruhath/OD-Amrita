import createClient from 'openapi-fetch'
import type { components, paths } from '@od-amrita/contracts'
import type { SessionPort } from '../features/auth/session'

export type Identity = components['schemas']['AuthenticatedIdentity']
export type Branch = components['schemas']['BranchSummary']
export type EventRecord = components['schemas']['Event']
export type EventCollection = components['schemas']['EventCollection']
export type CreateEventInput = components['schemas']['CreateEventRequest']
export type CategoryRecord = components['schemas']['Category']
export type CategoryCollection = components['schemas']['CategoryCollection']
export type CreateCategoryInput = components['schemas']['CreateCategoryRequest']
export type CoordinatorAssignment = components['schemas']['CoordinatorAssignment']
export type CoordinatorAssignmentCollection = components['schemas']['CoordinatorAssignmentCollection']
export type CreateCoordinatorAssignmentInput = components['schemas']['CreateCoordinatorAssignmentRequest']
export type CaptureCreditConfig = components['schemas']['CaptureCreditConfig']
export type CaptureCreditUsage = components['schemas']['CaptureCreditUsage']
export type CaptureCreditSummary = components['schemas']['CaptureCreditSummary']
export type UpdateCaptureCreditConfigInput = components['schemas']['UpdateCaptureCreditConfigRequest']
export type TeacherProfile = components['schemas']['TeacherProfile']
export type TeacherProfileCollection = components['schemas']['TeacherProfileCollection']
export type CreateTeacherProfileInput = components['schemas']['CreateTeacherProfileRequest']
export type AttendanceActor = components['schemas']['AttendanceActor']
export type AttendanceRecord = components['schemas']['AttendanceRecord']
export type AttendanceRecordCollection = components['schemas']['AttendanceRecordCollection']
export type StudentRosterImportResult = components['schemas']['StudentRosterImportResult']
export type StudentRosterImportError = components['schemas']['StudentRosterImportError']
export type StudentCollection = components['schemas']['StudentCollection']
export type ResolvedCardStudent = components['schemas']['ResolvedCardStudent']
export type CardSeal = components['schemas']['CardSeal']
export type CardTokenResolveRequest = components['schemas']['CardTokenResolveRequest']
/** The exact object written to an NFC card — version and token, nothing else. */
export type CardPayload = CardSeal['payload']
export type ValidationError = components['schemas']['ValidationError']
export type ErrorEnvelope = components['schemas']['Error']

// Coordinator marking surface.
export type StudentSearchResults = components['schemas']['StudentSearchResults']
export type MarkStatus = components['schemas']['MarkStatus']
export type MarkSlotWindow = components['schemas']['MarkSlotWindow']
export type MarkCounters = components['schemas']['MarkCounters']
export type MarkAttendanceInput = components['schemas']['MarkAttendanceRequest']
export type MarkAttendanceResult = components['schemas']['MarkAttendanceResult']
export type CaptureMethod = components['schemas']['CaptureMethod']

export class ApiFailure extends Error {
  readonly status: number
  readonly body: ErrorEnvelope | ValidationError

  constructor(status: number, body: ErrorEnvelope | ValidationError) {
    super(body.message)
    this.status = status
    this.body = body
  }
}

/**
 * Coerces whatever the transport produced into a real error envelope.
 *
 * THIS IS NOT DEFENSIVE PROGRAMMING — it fixes an observed failure. Not every
 * error response is JSON: gin answers an unmatched route with the plain text
 * `404 page not found`, and a proxy or tunnel in front of the API answers with
 * an HTML page. `openapi-fetch` handles those by returning the RAW STRING as
 * `error` (it does `let error = await response.text()` and only then attempts
 * `JSON.parse`). A non-empty string is truthy, so a `?? fallback` never fires
 * and `body.code` / `body.message` are both `undefined`.
 *
 * Every caller reads `body.code` to choose a message, so the result was a red
 * banner with NO reason in it — which is exactly the state the banner exists to
 * prevent, and which was seen for real when the deployed API was briefly serving
 * a build without these routes. Normalising here fixes every surface at once,
 * because this is the single choke point they all throw through.
 */
function errorEnvelope(raw: unknown, response: Response): ErrorEnvelope | ValidationError {
  if (raw && typeof raw === 'object' && typeof (raw as ErrorEnvelope).code === 'string') {
    return raw as ErrorEnvelope | ValidationError
  }
  // The body text is deliberately NOT echoed into the message. An error body can
  // quote the request that produced it, and on the card paths that request
  // carries a token.
  return {
    code: 'INTERNAL',
    message: `The server returned an unreadable ${response.status} response.`,
  } as ErrorEnvelope
}

const apiBaseURL = import.meta.env.VITE_API_BASE_URL ?? window.location.origin

// Pre-production only: true while the API is reached through an ngrok tunnel,
// false the moment VITE_API_BASE_URL points at the API's own domain (Fly). Gating
// the tunnel bypass header on this means it disappears automatically at cutover
// rather than relying on someone remembering to delete a line — and it keeps the
// header (which forces a CORS preflight on every call) off the production origin.
const isNgrokTunnel = /\bngrok\b/.test(apiBaseURL)

function createAuthorizedFetch(session: SessionPort): typeof fetch {
  return async (input, init) => {
    const initial = new Request(input, init)
    const invoke = async (request: Request, token: string | null) => {
      const headers = new Headers(request.headers)
      if (token) headers.set('Authorization', `Bearer ${token}`)
      headers.set('Accept', 'application/json')
      // Pre-production only: while the API is reached through an ngrok tunnel,
      // the tunnel's free tier answers browser-looking requests with an HTML
      // interstitial instead of proxying them, which breaks every API call from
      // the deployed web app. This header opts out of that page. Sent only when
      // the API base URL is the tunnel (see isNgrokTunnel) so it drops off
      // automatically once the API moves to its own domain.
      if (isNgrokTunnel) headers.set('ngrok-skip-browser-warning', 'true')
      return fetch(new Request(request, { headers }))
    }

    let response = await invoke(initial.clone(), await session.accessToken())
    if (response.status !== 401) return response

    const refreshed = await session.refresh()
    if (!refreshed) {
      await session.signOut()
      return response
    }
    response = await invoke(initial.clone(), refreshed)
    if (response.status === 401) await session.signOut()
    return response
  }
}

export type EventApi = ReturnType<typeof createEventApi>

export function createEventApi(session: SessionPort) {
  const client = createClient<paths>({ baseUrl: apiBaseURL, fetch: createAuthorizedFetch(session) })

  const unwrap = <T>(result: { data?: T; error?: ErrorEnvelope | ValidationError; response: Response }): T => {
    if (result.data !== undefined) return result.data
    throw new ApiFailure(result.response.status, errorEnvelope(result.error, result.response))
  }

  return {
    async identity() {
      return unwrap(await client.GET('/api/v1/auth/me'))
    },
    async branches() {
      return unwrap(await client.GET('/api/v1/admin/branches'))
    },
    async events(page = 1, pageSize = 20) {
      return unwrap(await client.GET('/api/v1/admin/events', { params: { query: { status: 'all', page, page_size: pageSize } } }))
    },
    async event(eventID: string) {
      return unwrap(await client.GET('/api/v1/admin/events/{event_id}', {
        params: { path: { event_id: eventID } },
      }))
    },
    async create(input: CreateEventInput) {
      return unwrap(await client.POST('/api/v1/admin/events', { body: input }))
    },
    async archive(eventID: string) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/archive', {
        params: { path: { event_id: eventID } },
      }))
    },
    async categories(eventID: string) {
      return unwrap(await client.GET('/api/v1/admin/events/{event_id}/categories', {
        params: { path: { event_id: eventID } },
      }))
    },
    async portalMarkEvents(page = 1, pageSize = 20) {
      return unwrap(await client.GET('/api/v1/portal/mark/events', {
        params: { query: { page, page_size: pageSize } },
      }))
    },
    async portalMarkCategories(eventID: string) {
      return unwrap(await client.GET('/api/v1/portal/mark/events/{event_id}/categories', {
        params: { path: { event_id: eventID } },
      }))
    },
    async createCategory(eventID: string, input: CreateCategoryInput) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/categories', {
        params: { path: { event_id: eventID } },
        body: input,
      }))
    },
    async coordinators(eventID: string) {
      return unwrap(await client.GET('/api/v1/admin/events/{event_id}/coordinators', {
        params: { path: { event_id: eventID } },
      }))
    },
    async createCoordinator(eventID: string, input: CreateCoordinatorAssignmentInput) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/coordinators', {
        params: { path: { event_id: eventID } },
        body: input,
      }))
    },
    async captureCredits(eventID: string) {
      return unwrap(await client.GET('/api/v1/admin/events/{event_id}/capture-credits', {
        params: { path: { event_id: eventID } },
      }))
    },
    async updateCaptureCreditConfig(eventID: string, input: UpdateCaptureCreditConfigInput) {
      return unwrap(await client.PUT('/api/v1/admin/events/{event_id}/capture-credits/config', {
        params: { path: { event_id: eventID } },
        body: input,
      }))
    },
    async resetCaptureCredits(eventID: string, userID: string) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/capture-credits/coordinators/{user_id}/reset', {
        params: { path: { event_id: eventID, user_id: userID } },
      }))
    },
    async teachers(page = 1, pageSize = 20) {
      return unwrap(await client.GET('/api/v1/admin/teachers', { params: { query: { page, page_size: pageSize } } }))
    },
    async createTeacher(input: CreateTeacherProfileInput) {
      return unwrap(await client.POST('/api/v1/admin/teachers', { body: input }))
    },
    async importStudents(file: File) {
      const body = new FormData()
      body.append('file', file)
      return unwrap(await client.POST('/api/v1/admin/students/import', {
        body: { file: file.name },
        bodySerializer: () => body,
      }))
    },
    /**
     * One page of the card-populator roster.
     *
     * Filters are omitted rather than sent blank when unset — an empty
     * `section=` would be a filter for the empty string, which no student row
     * can match, silently returning an empty roster.
     */
    async students(filters: {
      branchID?: string
      batch?: string
      section?: string
      page?: number
      pageSize?: number
    } = {}) {
      const query: Record<string, string | number> = {
        page: filters.page ?? 1,
        page_size: filters.pageSize ?? 100,
      }
      if (filters.branchID) query.branch_id = filters.branchID
      if (filters.batch) query.batch = filters.batch
      if (filters.section) query.section = filters.section
      return unwrap(await client.GET('/api/v1/admin/students', { params: { query } }))
    },
    /**
     * Seals one student's card server-side and returns the payload to write.
     *
     * Deliberately per-student: there is no bulk seal, so the number of raw
     * tokens this client has ever held is bounded by the cards actually being
     * written (AR20, and the endpoint scope Story 0.3 established).
     */
    async sealStudentCard(studentID: string) {
      return unwrap(await client.POST('/api/v1/admin/students/{student_id}/card-seal', {
        params: { path: { student_id: studentID } },
      }))
    },
    /** Resolves a token read back off a card to its student, server-side. */
    async resolveCardToken(input: CardTokenResolveRequest) {
      return unwrap(await client.POST('/api/v1/admin/card-tokens/resolve', { body: input }))
    },

    // -----------------------------------------------------------------------
    // Coordinator marking surface.
    //
    // These call the SCOPED endpoints, never an `/admin/` one. The admin reads
    // are admin-gated by design, so calling one from `/mark` produces a 403 that
    // reads as a bug in the marking screen rather than as the deliberate
    // boundary it is.
    // -----------------------------------------------------------------------

    /** The active events this coordinator may mark for. */
    async markableEvents() {
      return unwrap(await client.GET('/api/v1/events'))
    },

    /** The categories of one markable event. */
    async markableCategories(eventID: string) {
      return unwrap(await client.GET('/api/v1/events/{event_id}/categories', {
        params: { path: { event_id: eventID } },
      }))
    },

    /**
     * Prefix-searches the roster.
     *
     * The query is trimmed here so the URL carries what the coordinator meant;
     * the server trims and bounds again, and owns the minimum length.
     */
    async searchStudents(query: string, limit?: number) {
      const search: { q: string; limit?: number } = { q: query.trim() }
      if (limit !== undefined) search.limit = limit
      return unwrap(await client.GET('/api/v1/students/search', { params: { query: search } }))
    },

    /**
     * The server-computed counters and active slot window.
     *
     * `slot: null` is passed through untouched — it is the explicit "no slot is
     * open" state, and coercing it to 0 would render a countdown for a slot that
     * does not exist.
     */
    async markStatus(eventID: string, categoryID: string) {
      return unwrap(await client.GET('/api/v1/mark/status', {
        params: { query: { event_id: eventID, category_id: categoryID } },
      }))
    },
    async adminAttendance(
      eventID: string,
      filters: { categoryID?: string; slot?: number; page?: number; pageSize?: number } = {},
    ) {
      const query: Record<string, string | number> = {
        page: filters.page ?? 1,
        page_size: filters.pageSize ?? 20,
      }
      if (filters.categoryID) query.category_id = filters.categoryID
      if (filters.slot !== undefined) query.slot = filters.slot
      return unwrap(await client.GET('/api/v1/admin/events/{event_id}/attendance', {
        params: { path: { event_id: eventID }, query },
      }))
    },

    /**
     * Marks one student present. Synchronous and live: the 201 is the ONLY
     * evidence a mark exists. There is no queue, so a failure here is red and
     * retried by the human — never an amber "queued".
     */
    async mark(input: MarkAttendanceInput) {
      return unwrap(await client.POST('/api/v1/mark', { body: input }))
    },
  }
}
