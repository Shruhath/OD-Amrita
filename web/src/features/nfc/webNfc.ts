/**
 * Web NFC adapter — the single place this app touches `NDEFReader`.
 *
 * Everything above this module deals in `CardPayload` and typed failures, so
 * the UI and its tests never need a real NFC stack. Web NFC is Chrome-on-Android
 * only, is not Baseline, and needs a secure context, a user gesture, granted
 * permission, NFC-capable hardware, NFC switched on, and a tapped NDEF tag
 * before any promise settles — none of which feature detection can prove.
 * See https://developer.mozilla.org/en-US/docs/Web/API/Web_NFC_API
 */

import type { CardPayload } from '../../api/client'

/**
 * The media type of the one record this app writes.
 *
 * Reads filter on it so a tag that also carries an unrelated record (a URL, a
 * vendor record from a previous life) still verifies, and so a foreign tag
 * cannot be mistaken for one of ours.
 */
export const CARD_MEDIA_TYPE = 'application/vnd.od-amrita.card+json'

/** Why the card path is unavailable, or that it is usable. */
export type WebNfcStatus =
  /** `NDEFReader` exists on a secure origin. Hardware may still be absent or off. */
  | 'available'
  /** The page is not a secure context, so `NDEFReader` is not exposed at all. */
  | 'insecure'
  /** No `NDEFReader` — not Chrome on Android, or the feature is disabled. */
  | 'unsupported'

/** A card failure the UI can render distinctly. Never carries token material. */
export class CardNfcError extends Error {
  readonly kind: CardNfcErrorKind

  constructor(kind: CardNfcErrorKind, message: string) {
    super(message)
    this.name = 'CardNfcError'
    this.kind = kind
  }
}

export type CardNfcErrorKind =
  | 'unsupported'
  | 'insecure'
  | 'permission-denied'
  | 'write-failed'
  | 'read-failed'
  | 'card-payload'
  | 'cancelled'

export type WebNfcAdapter = {
  status(): WebNfcStatus
  /** Writes one MIME record holding exactly `payload`. Requires a user gesture. */
  write(payload: CardPayload, signal?: AbortSignal): Promise<void>
  /** Scans until a tag carrying our record is tapped, then returns its payload. */
  read(signal?: AbortSignal): Promise<CardPayload>
  /**
   * Runs ONE continuous scan until `signal` aborts, calling `onPayload` for every
   * tap and `onError` for every unreadable one.
   *
   * Marking is a continuous activity — a coordinator taps ~90 cards in a row and
   * must not press "start" between them — so this starts `NDEFReader.scan()` once
   * from the user gesture rather than re-arming per tap. A bad tag is reported
   * and the session continues; the coordinator still has the rest of the queue.
   */
  scanSession(
    onPayload: (payload: CardPayload) => void,
    onError: (error: CardNfcError) => void,
    signal: AbortSignal,
  ): Promise<void>
}

const READ_TIMEOUT_MS = 30_000

/**
 * How long an identical token is ignored after it is delivered.
 *
 * One physical tap commonly raises several `reading` events, and two marks from
 * one tap would show the coordinator a duplicate error for a student they marked
 * correctly. The server's `SCAN_DUPLICATE` remains the correctness guarantee —
 * this is UI hygiene, and it deliberately keys on the TOKEN so a different card
 * tapped inside the window is still a real second student and always gets through.
 */
const SCAN_DEBOUNCE_MS = 2_000

// ---------------------------------------------------------------------------
// Minimal ambient shape for Web NFC.
//
// TypeScript's DOM lib does not declare Web NFC (it is not Baseline). These
// declarations are deliberately narrow — only what this adapter calls — rather
// than a dependency, so there is nothing to keep in sync with a spec that is
// still moving.
// ---------------------------------------------------------------------------

type NdefRecordInit = {
  recordType: string
  mediaType?: string
  data?: BufferSource | string
}

type NdefRecordLike = {
  recordType: string
  mediaType?: string | null
  data?: DataView | null
  encoding?: string | null
}

type NdefReadingEventLike = Event & { message: { records: readonly NdefRecordLike[] } }

type NdefReaderLike = {
  write(message: { records: NdefRecordInit[] }, options?: { signal?: AbortSignal }): Promise<void>
  scan(options?: { signal?: AbortSignal }): Promise<void>
  addEventListener(type: 'reading', listener: (event: NdefReadingEventLike) => void): void
  addEventListener(type: 'readingerror', listener: (event: Event) => void): void
  removeEventListener(type: string, listener: (event: never) => void): void
}

type NdefReaderConstructor = new () => NdefReaderLike

type WebNfcWindow = Window & { NDEFReader?: NdefReaderConstructor }

/**
 * Builds an adapter bound to `windowRef`.
 *
 * The window is a parameter rather than a global read so tests can supply a
 * fake without touching jsdom's real globals.
 */
export function createWebNfcAdapter(windowRef: Window): WebNfcAdapter {
  const target = windowRef as WebNfcWindow

  const status = (): WebNfcStatus => {
    // Order matters: on an insecure origin `NDEFReader` is never exposed, so
    // checking support first would report every plain-HTTP page as "unsupported"
    // and send the operator hunting for the wrong problem.
    if (!target.isSecureContext) return 'insecure'
    if (typeof target.NDEFReader !== 'function') return 'unsupported'
    return 'available'
  }

  const readerOrThrow = (): NdefReaderLike => {
    const current = status()
    if (current === 'insecure') {
      throw new CardNfcError('insecure', 'NFC needs an HTTPS page. Open the populator over HTTPS and try again.')
    }
    if (current === 'unsupported') {
      throw new CardNfcError('unsupported', 'This browser cannot use NFC. Use Chrome on Android.')
    }
    return new (target.NDEFReader as NdefReaderConstructor)()
  }

  return {
    status,

    async write(payload, signal) {
      const reader = readerOrThrow()
      // Serialised from an explicitly rebuilt object, never by spreading the
      // seal response: spreading would put whatever else the server sent — the
      // student block included — onto the card.
      const body = JSON.stringify({ version: payload.version, token: payload.token })
      try {
        await reader.write(
          { records: [{ recordType: 'mime', mediaType: CARD_MEDIA_TYPE, data: new TextEncoder().encode(body) }] },
          signal ? { signal } : undefined,
        )
      } catch (error) {
        throw toCardError(error, 'write-failed', 'The card could not be written. Keep the card still on the phone and retry.')
      }
    },

    async read(signal) {
      const reader = readerOrThrow()

      return new Promise<CardPayload>((resolve, reject) => {
        // One settle only. A tag can raise several events per tap, and a second
        // resolve would let a LATER tag decide a verification that a previous
        // one already answered.
        let settled = false
        const finish = (outcome: () => void) => {
          if (settled) return
          settled = true
          cleanup()
          outcome()
        }

        const onReading = (event: NdefReadingEventLike) => {
          try {
            const payload = parseCardMessage(event.message.records)
            finish(() => resolve(payload))
          } catch (error) {
            finish(() => reject(error))
          }
        }
        const onReadingError = () => {
          finish(() =>
            reject(new CardNfcError('read-failed', 'The card could not be read. Keep the card still on the phone and retry.')),
          )
        }
        const onAbort = () => {
          finish(() => reject(new CardNfcError('cancelled', 'Card read cancelled.')))
        }

        if (signal?.aborted) {
          settled = true
          reject(new CardNfcError('cancelled', 'Card read cancelled.'))
          return
        }

        let timeoutID: ReturnType<typeof setTimeout> | undefined
        const cleanup = () => {
          reader.removeEventListener('reading', onReading as (event: never) => void)
          reader.removeEventListener('readingerror', onReadingError as (event: never) => void)
          signal?.removeEventListener('abort', onAbort)
          if (timeoutID !== undefined) clearTimeout(timeoutID)
        }
        timeoutID = setTimeout(() => {
          finish(() => reject(new CardNfcError('read-failed', 'Card read timed out. Tap the card again and retry.')))
        }, READ_TIMEOUT_MS)

        reader.addEventListener('reading', onReading)
        reader.addEventListener('readingerror', onReadingError)
        signal?.addEventListener('abort', onAbort)

        reader.scan(signal ? { signal } : undefined).catch((error: unknown) => {
          finish(() =>
            reject(toCardError(error, 'read-failed', 'NFC scanning could not start. Check that NFC is switched on.')),
          )
        })
      })
    },

    async scanSession(onPayload, onError, signal) {
      const reader = readerOrThrow()
      if (signal.aborted) return

      // Keyed by token, not by time alone: two different students tapping in
      // quick succession are two real marks, and dropping the second would lose
      // one silently.
      const lastSeenAt = new Map<string, number>()
      let stopped = false

      const onReading = (event: NdefReadingEventLike) => {
        if (stopped) return
        let payload: CardPayload
        try {
          payload = parseCardMessage(event.message.records)
        } catch (error) {
          // Reported, never fatal. A blank or foreign tag must not end a session
          // the coordinator still needs for the rest of their queue.
          onError(toCardError(error, 'card-payload', 'This card could not be read. Try tapping it again.'))
          return
        }

        const now = Date.now()
        const previous = lastSeenAt.get(payload.token)
        if (previous !== undefined && now - previous < SCAN_DEBOUNCE_MS) return

        // Pruned as we go so a long session cannot grow this map without bound.
        // It holds tokens, so it must never be persisted anywhere (AR20).
        for (const [token, at] of lastSeenAt) {
          if (now - at >= SCAN_DEBOUNCE_MS) lastSeenAt.delete(token)
        }
        lastSeenAt.set(payload.token, now)
        onPayload(payload)
      }

      const onReadingError = () => {
        if (stopped) return
        onError(new CardNfcError('read-failed', 'A card could not be read. Keep it still on the phone and tap again.'))
      }

      const stop = () => {
        if (stopped) return
        stopped = true
        // Every token this session saw is dropped here. Nothing about a card
        // survives the screen.
        lastSeenAt.clear()
        reader.removeEventListener('reading', onReading as (event: never) => void)
        reader.removeEventListener('readingerror', onReadingError as (event: never) => void)
        signal.removeEventListener('abort', stop)
      }

      reader.addEventListener('reading', onReading)
      reader.addEventListener('readingerror', onReadingError)
      signal.addEventListener('abort', stop)

      try {
        await reader.scan({ signal })
      } catch (error) {
        stop()
        const failure = toCardError(error, 'read-failed', 'NFC scanning could not start. Check that NFC is switched on.')
        if (failure.kind === 'cancelled') return
        onError(failure)
      }
    },
  }
}

/**
 * Extracts our payload from a tag's records.
 *
 * Defensive on purpose. A tag is untrusted input: it may be blank, hold someone
 * else's data, or hold a truncated write from an interrupted tap. Every one of
 * those is a card-payload error the operator can retry, never a crash and never
 * a partially-trusted result.
 */
export function parseCardMessage(records: readonly NdefRecordLike[]): CardPayload {
  const record = records.find((candidate) => candidate.recordType === 'mime' && candidate.mediaType === CARD_MEDIA_TYPE)
  if (!record) {
    throw new CardNfcError('card-payload', 'This card has no OD-Amrita card payload. It may be blank or written for something else.')
  }
  if (!record.data) {
    throw new CardNfcError('card-payload', 'This card payload is empty. Write the card again.')
  }

  let parsed: unknown
  try {
    const text = new TextDecoder(record.encoding ?? 'utf-8').decode(record.data)
    parsed = JSON.parse(text)
  } catch {
    // The cause is deliberately dropped: a JSON parse error echoes the source
    // text, which is the token.
    throw new CardNfcError('card-payload', 'This card payload is not readable. Write the card again.')
  }

  if (typeof parsed !== 'object' || parsed === null) {
    throw new CardNfcError('card-payload', 'This card payload has the wrong shape. Write the card again.')
  }
  const { version, token } = parsed as Record<string, unknown>
  if (typeof version !== 'string' || version === '') {
    throw new CardNfcError('card-payload', 'This card payload has no version. Write the card again.')
  }
  if (typeof token !== 'string' || token === '') {
    throw new CardNfcError('card-payload', 'This card payload has no token. Write the card again.')
  }
  // Shape only. Whether the token is VALID is the server's answer, never this
  // client's — the seal key never reaches the device (NFR16, AR20).
  return { version, token }
}

function toCardError(error: unknown, fallbackKind: CardNfcErrorKind, fallbackMessage: string): CardNfcError {
  if (error instanceof CardNfcError) return error

  const name = error instanceof Error ? error.name : ''
  if (name === 'NotAllowedError') {
    return new CardNfcError('permission-denied', 'NFC permission was refused. Allow NFC for this site and retry.')
  }
  if (name === 'AbortError') {
    return new CardNfcError('cancelled', 'Card operation cancelled.')
  }
  if (name === 'NotSupportedError') {
    return new CardNfcError('unsupported', 'This device has no usable NFC hardware.')
  }
  // The original message is dropped rather than chained. A write rejection can
  // quote the record it failed on, and that record is the token.
  return new CardNfcError(fallbackKind, fallbackMessage)
}
