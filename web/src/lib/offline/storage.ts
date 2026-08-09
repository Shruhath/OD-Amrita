import { openDB, type DBSchema, type IDBPDatabase } from 'idb'

export type CardPayload = { token: string; version: string }

export type OfflineScan = {
  id: string
  studentToken?: string
  cardPayload?: CardPayload
  eventID: string
  categoryID: string
  captureMethod: string
  scannedAt: string
  status: 'pending' | 'failed_dead_letter'
}

type EncryptedCardPayload = {
  version: string
  iv: string
  ciphertext: string
}

type StoredOfflineScan = Omit<OfflineScan, 'cardPayload'> & {
  cardPayload?: EncryptedCardPayload | CardPayload
}

interface OfflineDBSchema extends DBSchema {
  scan_queue: {
    key: string
    value: StoredOfflineScan
    indexes: { 'by-status': string }
  }
  crypto_keys: {
    key: string
    value: CryptoKey
  }
}

const DB_NAME = 'od_amrita_offline_db'
const DB_VERSION = 2
const CARD_KEY_ID = 'card-payload-v1'
const MAX_SCAN_AGE_MS = 24 * 60 * 60 * 1000

let dbPromise: Promise<IDBPDatabase<OfflineDBSchema>> | null = null

function getDB() {
  if (!dbPromise) {
    dbPromise = openDB<OfflineDBSchema>(DB_NAME, DB_VERSION, {
      upgrade(db) {
        if (!db.objectStoreNames.contains('scan_queue')) {
          const store = db.createObjectStore('scan_queue', { keyPath: 'id' })
          store.createIndex('by-status', 'status')
        }
        if (!db.objectStoreNames.contains('crypto_keys')) db.createObjectStore('crypto_keys')
      },
    })
  }
  return dbPromise
}

async function encryptionKey(): Promise<CryptoKey> {
  const db = await getDB()
  const existing = await db.get('crypto_keys', CARD_KEY_ID)
  if (existing) return existing
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt'])
  await db.put('crypto_keys', key, CARD_KEY_ID)
  return key
}

function base64(value: ArrayBuffer | Uint8Array): string {
  const bytes = value instanceof ArrayBuffer ? new Uint8Array(value) : value
  return btoa(String.fromCharCode(...bytes))
}

function bytes(value: string): Uint8Array<ArrayBuffer> {
  const decoded = atob(value)
  const output = new Uint8Array(new ArrayBuffer(decoded.length))
  for (let index = 0; index < decoded.length; index += 1) output[index] = decoded.charCodeAt(index)
  return output
}

async function encryptCardPayload(payload: CardPayload): Promise<EncryptedCardPayload> {
  const iv = new Uint8Array(new ArrayBuffer(12))
  crypto.getRandomValues(iv)
  const encoded = new TextEncoder().encode(JSON.stringify(payload))
  const plaintext = new Uint8Array(new ArrayBuffer(encoded.byteLength))
  plaintext.set(encoded)
  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    await encryptionKey(),
    plaintext,
  )
  return { version: payload.version, iv: base64(iv), ciphertext: base64(ciphertext) }
}

async function decryptCardPayload(payload: EncryptedCardPayload): Promise<CardPayload> {
  const plaintext = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: bytes(payload.iv) },
    await encryptionKey(),
    bytes(payload.ciphertext),
  )
  return JSON.parse(new TextDecoder().decode(plaintext)) as CardPayload
}

function isEncrypted(payload: EncryptedCardPayload | CardPayload): payload is EncryptedCardPayload {
  return 'ciphertext' in payload && 'iv' in payload
}

function uuidv7(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  const timestamp = Date.now()
  for (let index = 5; index >= 0; index -= 1) bytes[index] = timestamp / 2 ** (8 * (5 - index))
  bytes[6] = (bytes[6] & 0x0f) | 0x70
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export async function enqueueScan(scan: Omit<OfflineScan, 'id' | 'scannedAt' | 'status'>): Promise<OfflineScan> {
  const db = await getDB()
  const stored: StoredOfflineScan = {
    ...scan,
    cardPayload: scan.cardPayload ? await encryptCardPayload(scan.cardPayload) : undefined,
    id: uuidv7(),
    scannedAt: new Date().toISOString(),
    status: 'pending',
  }
  await db.put('scan_queue', stored)
  return { ...stored, cardPayload: scan.cardPayload }
}

export async function getPendingScans(): Promise<OfflineScan[]> {
  const db = await getDB()
  const all = await db.getAllFromIndex('scan_queue', 'by-status', 'pending')
  const cutoff = Date.now() - MAX_SCAN_AGE_MS
  const fresh: OfflineScan[] = []

  for (const stored of all) {
    if (Date.parse(stored.scannedAt) < cutoff) {
      await db.delete('scan_queue', stored.id)
      continue
    }
    const cardPayload = stored.cardPayload
      ? isEncrypted(stored.cardPayload)
        ? await decryptCardPayload(stored.cardPayload)
        : stored.cardPayload
      : undefined
    // Upgrade legacy plaintext rows as soon as they are read.
    if (stored.cardPayload && !isEncrypted(stored.cardPayload)) {
      await db.put('scan_queue', { ...stored, cardPayload: await encryptCardPayload(stored.cardPayload) })
    }
    fresh.push({ ...stored, cardPayload })
  }
  return fresh.sort((a, b) => a.scannedAt.localeCompare(b.scannedAt))
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

export async function clearOfflineQueue(): Promise<void> {
  if (typeof indexedDB.databases === 'function') {
    const dbs = await indexedDB.databases()
    if (!dbs.some((db) => db.name === DB_NAME)) return
  }
  const db = await getDB()
  const tx = db.transaction(['scan_queue', 'crypto_keys'], 'readwrite')
  await tx.objectStore('scan_queue').clear()
  await tx.objectStore('crypto_keys').delete(CARD_KEY_ID)
  await tx.done
  dbPromise = null
}

export async function getPendingCount(): Promise<number> {
  const db = await getDB()
  return db.countFromIndex('scan_queue', 'by-status', 'pending')
}
