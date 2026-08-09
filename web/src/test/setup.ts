import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterAll, afterEach, beforeAll, beforeEach } from 'vitest'
import { captureCreditSummary } from '../../tests/support/factories/captureCredits'
import { server } from './server'

const storage = new Map<string, string>()
const localStorageStub: Storage = {
  get length() { return storage.size },
  clear: () => storage.clear(),
  getItem: (key) => storage.get(key) ?? null,
  key: (index) => [...storage.keys()][index] ?? null,
  removeItem: (key) => { storage.delete(key) },
  setItem: (key, value) => { storage.set(key, String(value)) },
}
Object.defineProperty(window, 'localStorage', { configurable: true, value: localStorageStub })
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: localStorageStub })

// Radix Select uses pointer capture in the browser. JSDOM does not implement
// these methods, so provide the no-op browser contract for component tests.
if (typeof HTMLElement !== 'undefined') {
  HTMLElement.prototype.hasPointerCapture ??= () => false
  HTMLElement.prototype.setPointerCapture ??= () => undefined
  HTMLElement.prototype.releasePointerCapture ??= () => undefined
  HTMLElement.prototype.scrollIntoView ??= () => undefined
}

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))

beforeEach(() => {
  server.use(
    http.get('*/api/v1/admin/events/:eventID/capture-credits', () =>
      HttpResponse.json(captureCreditSummary({ data: [] })),
    ),
  )
})

afterEach(() => {
  server.resetHandlers()
  window.localStorage.clear()
  cleanup()
})

afterAll(() => server.close())
