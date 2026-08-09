import { beforeEach, describe, expect, test, vi } from 'vitest'
import type { ApiClient } from '../../api/client'
import { flushOfflineQueue } from './sync'

const storage = vi.hoisted(() => ({
  getPendingScans: vi.fn(),
  removeScans: vi.fn(),
  markAsDeadLetter: vi.fn(),
}))

vi.mock('./storage', () => storage)

function scan(id: string) {
  return {
    id,
    eventID: 'event-1',
    categoryID: 'category-1',
    studentToken: 'student-1',
    captureMethod: 'search',
    scannedAt: '2026-08-09T10:00:00.000Z',
    status: 'pending' as const,
  }
}

describe('offline batch sync', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(navigator, 'onLine', { configurable: true, value: true })
  })

  test('sends the local ids in one batch and settles applied, duplicate, and permanent results', async () => {
    storage.getPendingScans.mockResolvedValue([scan('scan-1'), scan('scan-2'), scan('scan-3')])
    const submitScans = vi.fn().mockResolvedValue({
      results: [
        { id: 'scan-1', status: 'applied' },
        { id: 'scan-2', status: 'duplicate' },
        { id: 'scan-3', status: 'rejected', reason: 'CARD_NOT_RECOGNIZED' },
      ],
    })
    const complete = vi.fn()

    await flushOfflineQueue({ submitScans } as unknown as ApiClient, complete)

    expect(submitScans).toHaveBeenCalledWith({
      scans: [
        expect.objectContaining({ id: 'scan-1', event_id: 'event-1', category_id: 'category-1' }),
        expect.objectContaining({ id: 'scan-2' }),
        expect.objectContaining({ id: 'scan-3' }),
      ],
    })
    expect(storage.removeScans).toHaveBeenCalledWith(['scan-1', 'scan-2'])
    expect(storage.markAsDeadLetter).toHaveBeenCalledWith(['scan-3'])
    expect(complete).toHaveBeenCalledWith(3)
  })

  test('limits a flush to fifty scans so FIFO work continues in later flushes', async () => {
    vi.useFakeTimers()
    const pending = Array.from({ length: 51 }, (_, index) => scan(`scan-${index}`))
    storage.getPendingScans.mockResolvedValue(pending)
    const submitScans = vi.fn().mockResolvedValue({ results: pending.slice(0, 50).map((item) => ({ id: item.id, status: 'applied' })) })

    await flushOfflineQueue({ submitScans } as unknown as ApiClient)

    expect(submitScans.mock.calls[0][0].scans).toHaveLength(50)
    expect(storage.removeScans.mock.calls[0][0]).toHaveLength(50)
    vi.clearAllTimers()
    vi.useRealTimers()
  })
})
