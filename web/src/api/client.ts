import createClient from 'openapi-fetch'
import type { components, paths } from '@od-amrita/contracts'
import type { SessionPort } from '../features/auth/session'

export type Identity = components['schemas']['AuthenticatedIdentity']
export type Branch = components['schemas']['BranchSummary']
export type EventRecord = components['schemas']['Event']
export type EventCollection = components['schemas']['EventCollection']
export type CreateEventInput = components['schemas']['CreateEventRequest']
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

function createAuthorizedFetch(session: SessionPort): typeof fetch {
  return async (input, init) => {
    const initial = new Request(input, init)
    const invoke = async (request: Request, token: string | null) => {
      const headers = new Headers(request.headers)
      if (token) headers.set('Authorization', `Bearer ${token}`)
      headers.set('Accept', 'application/json')
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
    async events() {
      return unwrap(await client.GET('/api/v1/admin/events', { params: { query: { status: 'all', page: 1, page_size: 20 } } }))
    },
    async create(input: CreateEventInput) {
      return unwrap(await client.POST('/api/v1/admin/events', { body: input }))
    },
    async archive(eventID: string) {
      return unwrap(await client.POST('/api/v1/admin/events/{event_id}/archive', {
        params: { path: { event_id: eventID } },
      }))
    },
  }
}
