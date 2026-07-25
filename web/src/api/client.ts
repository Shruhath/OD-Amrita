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
export type TeacherProfile = components['schemas']['TeacherProfile']
export type TeacherProfileCollection = components['schemas']['TeacherProfileCollection']
export type CreateTeacherProfileInput = components['schemas']['CreateTeacherProfileRequest']
export type ValidationError = components['schemas']['ValidationError']
export type ErrorEnvelope = components['schemas']['Error']

export class ApiFailure extends Error {
  readonly status: number
  readonly body: ErrorEnvelope | ValidationError

  constructor(status: number, body: ErrorEnvelope | ValidationError) {
    super(body.message)
    this.status = status
    this.body = body
  }
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
    const body = result.error ?? { code: 'INTERNAL', message: 'The request failed.' }
    throw new ApiFailure(result.response.status, body)
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
    async createCategory(eventID: string, input: CreateCategoryInput) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/categories', {
        params: { path: { event_id: eventID } },
        body: input,
      }))
    },
    async teachers(page = 1, pageSize = 20) {
      return unwrap(await client.GET('/api/v1/admin/teachers', { params: { query: { page, page_size: pageSize } } }))
    },
    async createTeacher(input: CreateTeacherProfileInput) {
      return unwrap(await client.POST('/api/v1/admin/teachers', { body: input }))
    },
  }
}
