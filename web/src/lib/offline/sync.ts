import { getPendingScans, removeScans, markAsDeadLetter } from './storage'
import type { ApiClient } from '../../api/client'

let isSyncing = false
let retryDelay = 2000

export async function flushOfflineQueue(api: ApiClient, onSyncComplete?: (syncedCount: number) => void): Promise<number> {
  if (isSyncing || !navigator.onLine) return 0
  isSyncing = true

  try {
    const pending = await getPendingScans()
    if (pending.length === 0) {
      retryDelay = 2000
      isSyncing = false
      return 0
    }

    let syncedCount = 0

    for (const scan of pending) {
      try {
        if (scan.cardPayload) {
          await api.markAttendance({
            event_id: scan.eventID,
            category_id: scan.categoryID,
            payload: scan.cardPayload,
            capture_method: scan.captureMethod as any,
          })
        } else if (scan.studentToken) {
          await api.markAttendance({
            event_id: scan.eventID,
            category_id: scan.categoryID,
            payload: { student_id: scan.studentToken },
            capture_method: scan.captureMethod as any,
          })
        }
        await removeScans([scan.id])
        syncedCount++
      } catch (err: any) {
        if (err?.status >= 400 && err?.status < 500) {
          await markAsDeadLetter([scan.id])
        } else {
          const jitter = Math.random() * 400 - 200
          setTimeout(() => {
            flushOfflineQueue(api, onSyncComplete)
          }, retryDelay + jitter)
          retryDelay = Math.min(retryDelay * 2, 60000)
          break
        }
      }
    }

    if (syncedCount > 0 && onSyncComplete) {
      onSyncComplete(syncedCount)
    }

    retryDelay = 2000
    isSyncing = false
    return syncedCount
  } catch {
    isSyncing = false
    return 0
  }
}

export function registerSyncListener(api: ApiClient, onSyncComplete?: (syncedCount: number) => void) {
  const handler = () => {
    void flushOfflineQueue(api, onSyncComplete)
  }
  window.addEventListener('online', handler)
  return () => window.removeEventListener('online', handler)
}
