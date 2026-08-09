import { openDB, type DBSchema, type IDBPDatabase } from 'idb'

export type OfflineScan = {
  id: string
  studentToken?: string
  cardPayload?: { token: string; version: string }
  eventID: string
  categoryID: string
  captureMethod: string
  scannedAt: string
  status: 'pending' | 'failed_dead_letter'
}

interface OfflineDBSchema extends DBSchema {
  scan_queue: {
    key: string
    value: OfflineScan
    indexes: { 'by-status': string }
  }
}

const DB_NAME = 'od_amrita_offline_db'
const DB_VERSION = 1

let dbPromise: Promise<IDBPDatabase<OfflineDBSchema>> | null = null

function getDB() {
  if (!dbPromise) {
    dbPromise = openDB<OfflineDBSchema>(DB_NAME, DB_VERSION, {
      upgrade(db) {
        const store = db.createObjectStore('scan_queue', { keyPath: 'id' })
        store.createIndex('by-status', 'status')
      },
    })
  }
  return dbPromise
}

function uuidv7(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  const timestamp = Date.now()
  for (let index = 5; index >= 0; index -= 1) {
    bytes[index] = timestamp / 2 ** (8 * (5 - index))
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x70
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export async function enqueueScan(scan: Omit<OfflineScan, 'id' | 'scannedAt' | 'status'>): Promise<OfflineScan> {
  const db = await getDB()
  const fullScan: OfflineScan = {
    ...scan,
    id: uuidv7(),
    scannedAt: new Date().toISOString(),
    status: 'pending',
  }
  await db.put('scan_queue', fullScan)
  return fullScan
}

export async function getPendingScans(): Promise<OfflineScan[]> {
  const db = await getDB()
  const all = await db.getAllFromIndex('scan_queue', 'by-status', 'pending')
  return all.sort((a, b) => a.scannedAt.localeCompare(b.scannedAt))
}

export async function removeScans(ids: string[]): Promise<void> {
  const db = await getDB()
  const tx = db.transaction('scan_queue', 'readwrite')
  await Promise.all(ids.map((id) => tx.store.delete(id)))
  await tx.done
}

export async function markAsDeadLetter(ids: string[]): Promise<void> {
  const db = await getDB()
  const tx = db.transaction('scan_queue', 'readwrite')
  for (const id of ids) {
    const item = await tx.store.get(id)
    if (item) {
      item.status = 'failed_dead_letter'
      delete item.studentToken
      delete item.cardPayload
      await tx.store.put(item)
    }
  }
  await tx.done
}

export async function getPendingCount(): Promise<number> {
  const db = await getDB()
  return db.countFromIndex('scan_queue', 'by-status', 'pending')
}
