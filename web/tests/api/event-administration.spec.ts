import { expect, test, type APIRequestContext } from '@playwright/test'
import { faker } from '@faker-js/faker'

const API_BASE_URL = process.env.API_BASE_URL ?? 'http://127.0.0.1:8080'

type BranchSummary = {
  id: string
  code: string
  name: string
}

type EventStatus = 'active' | 'archived'

type EventResource = {
  id: string
  name: string
  event_date: string
  status: EventStatus
  branches: BranchSummary[]
  created_at: string
  archived_at: string | null
}

type CollectionEnvelope<T> = {
  data: T[]
  page: number
  page_size: number
  total: number
}

type DataEnvelope<T> = {
  data: T[]
}

type ValidationDetail = {
  field: string
  code: string
  message: string
}

type ErrorEnvelope = {
  code: string
  message: string
  details?: unknown
}

// These are explicit test fixtures, not an institution or production branch roster.
// The API fixture must seed matching normalized branch rows before an activated test.
const branchFixtures: readonly [BranchSummary, BranchSummary] = [
  {
    id: '10000000-0000-4000-8000-000000000001',
    code: 'ATDD-CSE',
    name: 'Computer Science Test Fixture',
  },
  {
    id: '10000000-0000-4000-8000-000000000002',
    code: 'ATDD-ECE',
    name: 'Electronics Test Fixture',
  },
]

const apiURL = (path: string): string => new URL(path, API_BASE_URL).toString()

const requiredEnv = (name: string): string => {
  const value = process.env[name]
  if (!value) {
    throw new Error(name + ' must be supplied by the API acceptance fixture')
  }
  return value
}

const bearer = (token: string): Record<string, string> => ({
  Authorization: 'Bearer ' + token,
  Accept: 'application/json',
})

const adminHeaders = (): Record<string, string> => bearer(requiredEnv('ATDD_ADMIN_TOKEN'))

const createEventRequest = (
  overrides: Partial<{ name: string; event_date: string; branch_ids: string[] }> = {},
) => ({
  name: faker.company.catchPhrase() + ' ' + faker.string.alphanumeric(8),
  event_date: '2026-08-15',
  branch_ids: branchFixtures.map((branch) => branch.id),
  ...overrides,
})

const readJSON = async <T>(response: { json(): Promise<unknown> }): Promise<T> =>
  (await response.json()) as T

const expectErrorEnvelope = (body: ErrorEnvelope, code: string): void => {
  expect(body).toEqual(
    expect.objectContaining({
      code,
      message: expect.any(String),
    }),
  )
}

const expectValidation = (
  body: ErrorEnvelope,
  expectedDetails: Array<Pick<ValidationDetail, 'field' | 'code'>>,
): void => {
  expect(body).toEqual({
    code: 'VALIDATION_FAILED',
    message: expect.any(String),
    details: expect.arrayContaining(
      expectedDetails.map((detail) =>
        expect.objectContaining({
          ...detail,
          message: expect.any(String),
        }),
      ),
    ),
  })
}

const expectEventWireShape = (
  event: EventResource,
  expected: Partial<EventResource>,
): void => {
  expect(event).toEqual(
    expect.objectContaining({
      id: expect.any(String),
      name: expect.any(String),
      event_date: expect.stringMatching(/^\d{4}-\d{2}-\d{2}$/),
      status: expect.stringMatching(/^(active|archived)$/),
      branches: expect.any(Array),
      created_at: expect.any(String),
      archived_at: event.status === 'archived' ? expect.any(String) : null,
      ...expected,
    }),
  )

  for (const branch of event.branches) {
    expect(branch).toEqual({
      id: expect.any(String),
      code: expect.any(String),
      name: expect.any(String),
    })
  }
}

const createEvent = async (
  request: APIRequestContext,
  overrides: Partial<{ name: string; event_date: string; branch_ids: string[] }> = {},
): Promise<EventResource> => {
  const response = await request.post(apiURL('/api/v1/admin/events'), {
    headers: adminHeaders(),
    data: createEventRequest(overrides),
  })
  expect(response.status()).toBe(201)
  return readJSON<EventResource>(response)
}

const listEvents = async (
  request: APIRequestContext,
  query = '',
): Promise<CollectionEnvelope<EventResource>> => {
  const response = await request.get(apiURL('/api/v1/admin/events' + query), {
    headers: adminHeaders(),
  })
  expect(response.status()).toBe(200)
  return readJSON<CollectionEnvelope<EventResource>>(response)
}

const archiveEvent = async (
  request: APIRequestContext,
  eventID: string,
): Promise<EventResource> => {
  const response = await request.post(
    apiURL('/api/v1/admin/events/' + eventID + '/archive'),
    { headers: adminHeaders() },
  )
  expect(response.status()).toBe(200)
  return readJSON<EventResource>(response)
}

test.describe('Admin event lifecycle API (ATDD red phase)', () => {
  test.skip('[P1] GET branches returns the server-owned normalized branch collection', async ({ request }) => {
    const response = await request.get(apiURL('/api/v1/admin/branches'), {
      headers: adminHeaders(),
    })

    expect(response.status()).toBe(200)
    const body = await readJSON<DataEnvelope<BranchSummary>>(response)
    expect(body).toEqual({
      data: expect.any(Array),
    })
    expect(body.data).toEqual(expect.arrayContaining(branchFixtures))
  })

  test.skip('[P1] POST events trims the name and returns the persisted multi-branch event', async ({ request }) => {
    const response = await request.post(apiURL('/api/v1/admin/events'), {
      headers: adminHeaders(),
      data: createEventRequest({
        name: '  Inter-Campus Robotics Challenge  ',
        event_date: '2026-08-18',
      }),
    })

    expect(response.status()).toBe(201)
    const created = await readJSON<EventResource>(response)
    expectEventWireShape(created, {
      name: 'Inter-Campus Robotics Challenge',
      event_date: '2026-08-18',
      status: 'active',
      archived_at: null,
    })
    expect(created.branches).toEqual(expect.arrayContaining(branchFixtures))

    const listed = await listEvents(request)
    expect(listed.data).toEqual(
      expect.arrayContaining([expect.objectContaining({ id: created.id })]),
    )
  })

  test.skip('[P1] GET events defaults to all statuses, retains archived rows, and follows the deterministic order', async ({ request }) => {
    const olderActive = await createEvent(request, {
      name: 'Older Active Event',
      event_date: '2026-08-17',
    })
    const newerArchived = await createEvent(request, {
      name: 'Newer Archived Event',
      event_date: '2026-08-18',
    })
    await archiveEvent(request, newerArchived.id)

    const body = await listEvents(request)
    expect(body).toEqual({
      data: expect.any(Array),
      page: 1,
      page_size: expect.any(Number),
      total: expect.any(Number),
    })

    const activeIndex = body.data.findIndex((event) => event.id === olderActive.id)
    const archivedIndex = body.data.findIndex((event) => event.id === newerArchived.id)
    expect(activeIndex).toBeGreaterThanOrEqual(0)
    expect(archivedIndex).toBeGreaterThanOrEqual(0)
    expect(archivedIndex).toBeLessThan(activeIndex)
    expect(body.data[archivedIndex]).toEqual(
      expect.objectContaining({ status: 'archived', archived_at: expect.any(String) }),
    )

    const returnedIDs = body.data.map((event) => event.id)
    const correctlyOrderedIDs = [...body.data]
      .sort((left, right) =>
        right.event_date.localeCompare(left.event_date) ||
        right.created_at.localeCompare(left.created_at) ||
        right.id.localeCompare(left.id),
      )
      .map((event) => event.id)
    expect(returnedIDs).toEqual(correctlyOrderedIDs)
  })

  test.skip('[P2] GET events applies active, archived, and all status filters', async ({ request }) => {
    const active = await createEvent(request, { name: 'Filter Active Event' })
    const toArchive = await createEvent(request, { name: 'Filter Archived Event' })
    await archiveEvent(request, toArchive.id)

    const activeOnly = await listEvents(request, '?status=active')
    const archivedOnly = await listEvents(request, '?status=archived')
    const explicitAll = await listEvents(request, '?status=all')

    expect(activeOnly.data.every((event) => event.status === 'active')).toBe(true)
    expect(activeOnly.data.some((event) => event.id === active.id)).toBe(true)
    expect(archivedOnly.data.every((event) => event.status === 'archived')).toBe(true)
    expect(archivedOnly.data.some((event) => event.id === toArchive.id)).toBe(true)
    expect(explicitAll.data.some((event) => event.id === active.id)).toBe(true)
    expect(explicitAll.data.some((event) => event.id === toArchive.id)).toBe(true)
  })

  test.skip('[P2] GET events returns the exact bounded pagination envelope', async ({ request }) => {
    await createEvent(request, { name: 'Pagination Event ' + faker.string.alphanumeric(8) })
    await createEvent(request, { name: 'Pagination Event ' + faker.string.alphanumeric(8) })
    await createEvent(request, { name: 'Pagination Event ' + faker.string.alphanumeric(8) })

    const first = await listEvents(request, '?status=all&page=1&page_size=2')
    const second = await listEvents(request, '?status=all&page=2&page_size=2')

    expect(first).toEqual({
      data: expect.any(Array),
      page: 1,
      page_size: 2,
      total: expect.any(Number),
    })
    expect(second).toEqual({
      data: expect.any(Array),
      page: 2,
      page_size: 2,
      total: first.total,
    })
    expect(first.data.length).toBeLessThanOrEqual(2)
    expect(second.data.length).toBeLessThanOrEqual(2)
    expect(first.data.map((event) => event.id)).not.toEqual(
      expect.arrayContaining(second.data.map((event) => event.id)),
    )
  })

  test.skip('[P1] GET events rejects invalid status and pagination values with typed validation details', async ({ request }) => {
    const cases = [
      { query: '?status=deleted', field: 'status', code: 'invalid_enum' },
      { query: '?page=0', field: 'page', code: 'minimum' },
      { query: '?page_size=0', field: 'page_size', code: 'minimum' },
      { query: '?page_size=100000', field: 'page_size', code: 'maximum' },
    ]

    for (const scenario of cases) {
      const response = await request.get(apiURL('/api/v1/admin/events' + scenario.query), {
        headers: adminHeaders(),
      })
      expect(response.status(), scenario.query).toBe(400)
      expectValidation(await readJSON<ErrorEnvelope>(response), [
        { field: scenario.field, code: scenario.code },
      ])
    }
  })

  test.skip('[P1] POST events rejects all documented field failures before persisting', async ({ request }) => {
    const cases = [
      {
        label: 'blank trimmed name',
        data: createEventRequest({ name: '   ' }),
        detail: { field: 'name', code: 'required' },
      },
      {
        label: 'invalid date-only value',
        data: createEventRequest({ event_date: '2026-02-31' }),
        detail: { field: 'event_date', code: 'invalid_format' },
      },
      {
        label: 'no branches',
        data: createEventRequest({ branch_ids: [] }),
        detail: { field: 'branch_ids', code: 'required' },
      },
      {
        label: 'duplicate branch IDs',
        data: createEventRequest({
          branch_ids: [branchFixtures[0].id, branchFixtures[0].id],
        }),
        detail: { field: 'branch_ids', code: 'duplicate' },
      },
      {
        label: 'malformed branch UUID',
        data: createEventRequest({ branch_ids: ['not-a-uuid'] }),
        detail: { field: 'branch_ids[0]', code: 'invalid_format' },
      },
      {
        label: 'unknown normalized branch',
        data: createEventRequest({
          branch_ids: ['ffffffff-ffff-4fff-8fff-ffffffffffff'],
        }),
        detail: { field: 'branch_ids[0]', code: 'not_found' },
      },
    ]

    for (const scenario of cases) {
      const response = await request.post(apiURL('/api/v1/admin/events'), {
        headers: adminHeaders(),
        data: scenario.data,
      })
      expect(response.status(), scenario.label).toBe(400)
      expectValidation(await readJSON<ErrorEnvelope>(response), [scenario.detail])
    }
  })

  test.skip('[P1] POST events rejects malformed JSON in the canonical error envelope', async ({ request }) => {
    const response = await request.post(apiURL('/api/v1/admin/events'), {
      headers: {
        ...adminHeaders(),
        'Content-Type': 'application/json',
      },
      data: '{"name":',
    })

    expect(response.status()).toBe(400)
    expectValidation(await readJSON<ErrorEnvelope>(response), [
      { field: '$', code: 'invalid_json' },
    ])
  })

  test.skip('[P0] first archive returns the archived resource as a successful state transition', async ({ request }) => {
    const created = await createEvent(request, {
      name: 'Archive Transition ' + faker.string.alphanumeric(8),
      event_date: '2026-08-20',
    })

    const archived = await archiveEvent(request, created.id)
    expectEventWireShape(archived, {
      id: created.id,
      name: created.name,
      event_date: created.event_date,
      status: 'archived',
      archived_at: expect.any(String) as unknown as string,
    })
    expect(archived.branches).toEqual(created.branches)
  })

  test.skip('[P2] repeat archive is idempotent and returns the same archived representation', async ({ request }) => {
    const created = await createEvent(request, {
      name: 'Idempotent Archive ' + faker.string.alphanumeric(8),
    })
    const first = await archiveEvent(request, created.id)
    const repeated = await archiveEvent(request, created.id)

    expect(repeated).toEqual(first)
    expect(repeated.status).toBe('archived')
    // The real-Postgres conservation scaffold owns the no-second-audit-row assertion.
  })

  test.skip('[P1] archive of an unknown event returns 404 EVENT_NOT_FOUND', async ({ request }) => {
    const response = await request.post(
      apiURL('/api/v1/admin/events/ffffffff-ffff-4fff-8fff-ffffffffffff/archive'),
      { headers: adminHeaders() },
    )

    expect(response.status()).toBe(404)
    expectErrorEnvelope(await readJSON<ErrorEnvelope>(response), 'EVENT_NOT_FOUND')
  })

  test.skip('[P0] archived events expose no update or DELETE operation and remain visible', async ({ request }) => {
    const created = await createEvent(request, {
      name: 'Read Only Aggregate ' + faker.string.alphanumeric(8),
    })
    await archiveEvent(request, created.id)

    for (const method of ['PATCH', 'PUT', 'DELETE'] as const) {
      const response = await request.fetch(apiURL('/api/v1/admin/events/' + created.id), {
        method,
        headers: {
          ...adminHeaders(),
          'Content-Type': 'application/json',
        },
        data: method === 'DELETE' ? undefined : { name: 'Forbidden Mutation' },
      })
      expect(response.status(), method).toBe(404)
      const error = await readJSON<ErrorEnvelope>(response)
      expect(error).toEqual(
        expect.objectContaining({
          code: expect.any(String),
          message: expect.any(String),
        }),
      )
    }

    const listed = await listEvents(request)
    expect(listed.data).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: created.id, status: 'archived' }),
      ]),
    )
  })
})

type DeniedCaller = {
  label: string
  expectedStatus: 401 | 403
  expectedCode: 'AUTH_TOKEN_INVALID' | 'RBAC_FORBIDDEN'
  headers: () => Record<string, string>
}

const deniedCallers: DeniedCaller[] = [
  {
    label: 'missing token',
    expectedStatus: 401,
    expectedCode: 'AUTH_TOKEN_INVALID',
    headers: () => ({ Accept: 'application/json' }),
  },
  {
    label: 'invalid token',
    expectedStatus: 401,
    expectedCode: 'AUTH_TOKEN_INVALID',
    headers: () => bearer('not-a-valid-jwt'),
  },
  {
    label: 'revoked token',
    expectedStatus: 401,
    expectedCode: 'AUTH_TOKEN_INVALID',
    headers: () => bearer(requiredEnv('ATDD_REVOKED_TOKEN')),
  },
  {
    label: 'unprovisioned identity',
    expectedStatus: 403,
    expectedCode: 'RBAC_FORBIDDEN',
    headers: () => bearer(requiredEnv('ATDD_UNPROVISIONED_TOKEN')),
  },
  {
    label: 'Teacher',
    expectedStatus: 403,
    expectedCode: 'RBAC_FORBIDDEN',
    headers: () => bearer(requiredEnv('ATDD_TEACHER_TOKEN')),
  },
  {
    label: 'Coordinator',
    expectedStatus: 403,
    expectedCode: 'RBAC_FORBIDDEN',
    headers: () => bearer(requiredEnv('ATDD_COORDINATOR_TOKEN')),
  },
  {
    label: 'Student',
    expectedStatus: 403,
    expectedCode: 'RBAC_FORBIDDEN',
    headers: () => bearer(requiredEnv('ATDD_STUDENT_TOKEN')),
  },
]

for (const caller of deniedCallers) {
  test.skip('[P0] ' + caller.label + ' is denied by every Admin event endpoint', async ({ request }) => {
    const headers = caller.headers()
    const operations = [
      () => request.get(apiURL('/api/v1/admin/branches'), { headers }),
      () => request.get(apiURL('/api/v1/admin/events'), { headers }),
      () =>
        request.post(apiURL('/api/v1/admin/events'), {
          headers,
          data: createEventRequest(),
        }),
      () =>
        request.post(
          apiURL('/api/v1/admin/events/10000000-0000-4000-8000-000000000099/archive'),
          { headers },
        ),
    ]

    for (const invoke of operations) {
      const response = await invoke()
      expect(response.status()).toBe(caller.expectedStatus)
      expectErrorEnvelope(
        await readJSON<ErrorEnvelope>(response),
        caller.expectedCode,
      )
    }
  })
}
