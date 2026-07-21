import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
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

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))

afterEach(() => {
  server.resetHandlers()
  window.localStorage.clear()
  cleanup()
})

afterAll(() => server.close())
