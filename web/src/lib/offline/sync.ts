import { getPendingScans, removeScans, markAsDeadLetter } from './storage'
import type { ApiClient, ScanBatchItem } from '../../api/client'

const BATCH_SIZE = 50
const INITIAL_RETRY_MS = 2_000
const MAX_RETRY_MS = 60_000

let isSyncing = false
let retryDelay = INITIAL_RETRY_MS

function asBatchItem(scan: Awaited<ReturnType<typeof getPendingScans>>[number]): ScanBatchItem {
  return {
    id: scan.id,
    event_id: scan.eventID,
    category_id: scan.categoryID,
    method: scan.captureMethod as ScanBatchItem['method'],
    client_scanned_at: scan.scannedAt,
    ...(scan.studentToken ? { student_id: scan.studentToken } : {}),
    ...(scan.cardPayload
      ? { card_token: scan.cardPayload.token, card_version: scan.cardPayload.version }
      : {}),
  }
}

function isTransient(reason: string | null | undefined): boolean {
  return reason === 'INTERNAL' || reason === 'RATE_LIMITED'
}

function scheduleRetry(api: ApiClient, onSyncComplete?: (settledCount: number) => void) {
  const jitter = Math.random() * 400 - 200
  const delay = Math.max(0, retryDelay + jitter)
  retryDelay = Math.min(retryDelay * 2, MAX_RETRY_MS)
  setTimeout(() => void flushOfflineQueue(api, onSyncComplete), delay)
}

export async function flushOfflineQueue(
  api: ApiClient,
  onSyncComplete?: (settledCount: number) => void,
): Promise<number> {
  if (isSyncing || !navigator.onLine) return 0
  isSyncing = true

  try {
    const pending = (await getPendingScans()).slice(0, BATCH_SIZE)
    if (pending.length === 0) {
      retryDelay = INITIAL_RETRY_MS
      return 0
    }

    const response = await api.submitScans({ scans: pending.map(asBatchItem) })
    const applied: string[] = []
    const deadLetters: string[] = []
    let hasTransientFailure = false

    for (const result of response.results) {
      if (result.status === 'applied' || result.status === 'duplicate') {
        applied.push(result.id)
      } else if (isTransient(result.reason)) {
        hasTransientFailure = true
      } else {
        deadLetters.push(result.id)
      }
    }

    if (applied.length > 0) await removeScans(applied)
    if (deadLetters.length > 0) await markAsDeadLetter(deadLetters)
    const settledCount = applied.length + deadLetters.length
    if (settledCount > 0) onSyncComplete?.(settledCount)

    if (hasTransientFailure || response.results.length < pending.length) {
      scheduleRetry(api, onSyncComplete)
    } else {
      retryDelay = INITIAL_RETRY_MS
    }
    return settledCount
  } catch {
    scheduleRetry(api, onSyncComplete)
    return 0
  } finally {
    isSyncing = false
  }
}

export function registerSyncListener(api: ApiClient, onSyncComplete?: (settledCount: number) => void) {
  const handler = () => void flushOfflineQueue(api, onSyncComplete)
  window.addEventListener('online', handler)
  return () => window.removeEventListener('online', handler)
}
