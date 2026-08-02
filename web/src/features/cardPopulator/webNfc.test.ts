import { describe, expect, test, vi } from 'vitest'
import { cardPayload } from '../../../tests/support/factories/card-populator'
import { CARD_MEDIA_TYPE, CardNfcError, createWebNfcAdapter, parseCardMessage } from './webNfc'

type RecordInit = { recordType: string; mediaType?: string; data?: BufferSource | string }
type ReadingListener = (event: { message: { records: unknown[] } }) => void

/**
 * Stands in for `NDEFReader`. `emitReading` lets a test deliver a tap at the
 * exact moment it wants one, which is what makes the read path testable
 * without a phone.
 */
function fakeReaderFactory() {
  const state = {
    writes: [] as { records: RecordInit[] }[],
    writeError: undefined as Error | undefined,
    scanError: undefined as Error | undefined,
    scanCalls: 0,
    listeners: { reading: [] as ReadingListener[], readingerror: [] as (() => void)[] },
    removed: 0,
  }

  class FakeNDEFReader {
    async write(message: { records: RecordInit[] }) {
      if (state.writeError) throw state.writeError
      state.writes.push(message)
    }
    async scan() {
      state.scanCalls += 1
      if (state.scanError) throw state.scanError
    }
    addEventListener(type: 'reading' | 'readingerror', listener: never) {
      if (type === 'reading') state.listeners.reading.push(listener as unknown as ReadingListener)
      else state.listeners.readingerror.push(listener as unknown as () => void)
    }
    removeEventListener() {
      state.removed += 1
    }
  }

  const emitReading = (records: unknown[]) => {
    for (const listener of [...state.listeners.reading]) listener({ message: { records } })
  }
  const emitReadingError = () => {
    for (const listener of [...state.listeners.readingerror]) listener()
  }

  const windowRef = { isSecureContext: true, NDEFReader: FakeNDEFReader } as unknown as Window

  return { state, windowRef, emitReading, emitReadingError }
}

function cardRecord(body: string, overrides: Partial<{ recordType: string; mediaType: string }> = {}) {
  return {
    recordType: 'mime',
    mediaType: CARD_MEDIA_TYPE,
    data: new DataView(new TextEncoder().encode(body).buffer),
    ...overrides,
  }
}

describe('Web NFC adapter for card population', () => {
  test('[P0] reports unsupported states before requesting NFC permission', () => {
    const insecureWindow = { isSecureContext: false } as Window
    const unsupportedWindow = { isSecureContext: true } as Window

    expect(createWebNfcAdapter(insecureWindow).status()).toBe('insecure')
    expect(createWebNfcAdapter(unsupportedWindow).status()).toBe('unsupported')
  })

  // An insecure origin never exposes NDEFReader, so a support-first check would
  // report every plain-HTTP page as "unsupported" and point the operator at the
  // wrong fix.
  test('[P0] reports insecure ahead of unsupported when both are true', () => {
    const insecureAndUnsupported = { isSecureContext: false } as Window

    expect(createWebNfcAdapter(insecureAndUnsupported).status()).toBe('insecure')
  })

  test('[P0] reports available when NDEFReader exists on a secure origin', () => {
    const { windowRef } = fakeReaderFactory()

    expect(createWebNfcAdapter(windowRef).status()).toBe('available')
  })

  test('[P0] writes exactly the server payload as one MIME NDEF record', async () => {
    const { state, windowRef } = fakeReaderFactory()
    const payload = cardPayload()

    await createWebNfcAdapter(windowRef).write(payload)

    expect(state.writes).toHaveLength(1)
    const records = state.writes[0].records
    expect(records).toHaveLength(1)
    expect(records[0].recordType).toBe('mime')
    expect(records[0].mediaType).toBe(CARD_MEDIA_TYPE)

    const written = new TextDecoder().decode(records[0].data as Uint8Array)
    expect(JSON.parse(written)).toEqual({ version: payload.version, token: payload.token })
    expect(written).not.toMatch(/roll_no|display_name|student_id|branch|section|batch|email|key/i)
  })

  // The seal response also carries the student block. Writing the payload by
  // rebuilding it — never by spreading the response — is what keeps identity
  // off the card (AR20).
  test('[P0] writes only version and token even when handed extra fields', async () => {
    const { state, windowRef } = fakeReaderFactory()
    const contaminated = {
      ...cardPayload(),
      roll_no: 'CB.SC.U4CSE24001',
      display_name: 'Ananya Rao',
      student_id: '95000000-0000-4000-8000-000000000001',
    } as unknown as ReturnType<typeof cardPayload>

    await createWebNfcAdapter(windowRef).write(contaminated)

    const written = new TextDecoder().decode(state.writes[0].records[0].data as Uint8Array)
    expect(Object.keys(JSON.parse(written)).sort()).toEqual(['token', 'version'])
    expect(written).not.toContain('Ananya Rao')
    expect(written).not.toContain('CB.SC.U4CSE24001')
  })

  test('[P0] refuses to write on an unsupported or insecure surface', async () => {
    await expect(createWebNfcAdapter({ isSecureContext: false } as Window).write(cardPayload())).rejects.toMatchObject({
      kind: 'insecure',
    })
    await expect(createWebNfcAdapter({ isSecureContext: true } as Window).write(cardPayload())).rejects.toMatchObject({
      kind: 'unsupported',
    })
  })

  test('[P0] maps a refused NFC permission to a distinct failure', async () => {
    const { state, windowRef } = fakeReaderFactory()
    const denied = new Error('denied')
    denied.name = 'NotAllowedError'
    state.writeError = denied

    await expect(createWebNfcAdapter(windowRef).write(cardPayload())).rejects.toMatchObject({
      kind: 'permission-denied',
    })
  })

  // A write rejection can quote the record it failed on, and that record is the
  // token. The adapter must replace the message rather than chain it.
  test('[P0] does not leak the token through a write failure message', async () => {
    const { state, windowRef } = fakeReaderFactory()
    const payload = cardPayload()
    state.writeError = new Error(`failed writing {"version":"v1","token":"${payload.token}"}`)

    let failure: CardNfcError | undefined
    try {
      await createWebNfcAdapter(windowRef).write(payload)
    } catch (error) {
      failure = error as CardNfcError
    }

    expect(failure).toBeInstanceOf(CardNfcError)
    expect(failure?.message).not.toContain(payload.token)
  })

  test('[P0] resolves the payload from the tapped card', async () => {
    const { windowRef, emitReading } = fakeReaderFactory()
    const payload = cardPayload()

    const pending = createWebNfcAdapter(windowRef).read()
    await Promise.resolve()
    emitReading([cardRecord(JSON.stringify(payload))])

    await expect(pending).resolves.toEqual({ version: payload.version, token: payload.token })
  })

  // A card may carry unrelated records from a previous life. Verification must
  // still succeed on ours rather than tripping over the first record.
  test('[P0] ignores unrelated records on the same card', async () => {
    const { windowRef, emitReading } = fakeReaderFactory()
    const payload = cardPayload()

    const pending = createWebNfcAdapter(windowRef).read()
    await Promise.resolve()
    emitReading([
      { recordType: 'url', data: new DataView(new TextEncoder().encode('https://example.test').buffer) },
      cardRecord(JSON.stringify(payload)),
    ])

    await expect(pending).resolves.toEqual({ version: payload.version, token: payload.token })
  })

  // One tap can raise several events. A second resolve would let a LATER card
  // answer a verification an earlier one already settled — the exact way a card
  // gets marked verified against the wrong student.
  test('[P0] settles once even when the reader fires repeatedly', async () => {
    const { windowRef, emitReading } = fakeReaderFactory()
    const first = cardPayload({ token: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' })
    const second = cardPayload({ token: 'BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB' })

    const pending = createWebNfcAdapter(windowRef).read()
    await Promise.resolve()
    emitReading([cardRecord(JSON.stringify(first))])
    emitReading([cardRecord(JSON.stringify(second))])

    await expect(pending).resolves.toMatchObject({ token: first.token })
  })

  test('[P0] rejects a reading error from the tag', async () => {
    const { windowRef, emitReadingError } = fakeReaderFactory()

    const pending = createWebNfcAdapter(windowRef).read()
    await Promise.resolve()
    emitReadingError()

    await expect(pending).rejects.toMatchObject({ kind: 'read-failed' })
  })

  test('[P0] rejects when scanning cannot start', async () => {
    const { state, windowRef } = fakeReaderFactory()
    state.scanError = new Error('nfc is off')

    await expect(createWebNfcAdapter(windowRef).read()).rejects.toMatchObject({ kind: 'read-failed' })
  })

  test('[P0] cancels a pending read when the caller aborts', async () => {
    const { windowRef } = fakeReaderFactory()
    const controller = new AbortController()

    const pending = createWebNfcAdapter(windowRef).read(controller.signal)
    await Promise.resolve()
    controller.abort()

    await expect(pending).rejects.toMatchObject({ kind: 'cancelled' })
  })

  test('[P0] rejects immediately when handed an already-aborted signal', async () => {
    const { state, windowRef } = fakeReaderFactory()
    const controller = new AbortController()
    controller.abort()

    await expect(createWebNfcAdapter(windowRef).read(controller.signal)).rejects.toMatchObject({ kind: 'cancelled' })
    expect(state.scanCalls).toBe(0)
  })

  test('[P0] detaches its listeners once a read settles', async () => {
    const { state, windowRef, emitReading } = fakeReaderFactory()

    const pending = createWebNfcAdapter(windowRef).read()
    await Promise.resolve()
    emitReading([cardRecord(JSON.stringify(cardPayload()))])
    await pending

    expect(state.removed).toBeGreaterThan(0)
  })
})

describe('card payload parsing', () => {
  const malformed: [string, unknown[]][] = [
    ['a blank card', []],
    ['only foreign records', [{ recordType: 'text', data: new DataView(new TextEncoder().encode('hello').buffer) }]],
    ['a right-type record with no data', [{ recordType: 'mime', mediaType: CARD_MEDIA_TYPE, data: null }]],
    ['truncated JSON', [cardRecord('{"version":"v1","tok')]],
    ['a JSON array', [cardRecord('[]')]],
    ['JSON null', [cardRecord('null')]],
    ['a missing version', [cardRecord(JSON.stringify({ token: cardPayload().token }))]],
    ['a missing token', [cardRecord(JSON.stringify({ version: 'v1' }))]],
    ['a blank version', [cardRecord(JSON.stringify({ version: '', token: cardPayload().token }))]],
    ['a blank token', [cardRecord(JSON.stringify({ version: 'v1', token: '' }))]],
    ['a non-string token', [cardRecord(JSON.stringify({ version: 'v1', token: 12345 }))]],
  ]

  test.each(malformed)('[P0] rejects %s as a card payload error', (_name, records) => {
    let caught: unknown
    try {
      parseCardMessage(records as never)
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(CardNfcError)
    expect((caught as CardNfcError).kind).toBe('card-payload')
  })

  // A JSON parse error echoes the source text, which IS the token. The adapter
  // must not pass that message through, and must not log it either.
  test('[P0] rejects malformed payloads without logging tokens', () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const consoleLog = vi.spyOn(console, 'log').mockImplementation(() => undefined)
    const token = cardPayload().token

    let failure: CardNfcError | undefined
    try {
      parseCardMessage([cardRecord(`{"version":"v1","token":"${token}`)] as never)
    } catch (error) {
      failure = error as CardNfcError
    }

    expect(failure).toBeInstanceOf(CardNfcError)
    expect(failure?.message).toMatch(/card payload/i)
    expect(failure?.message).not.toContain(token)
    expect(consoleError).not.toHaveBeenCalled()
    expect(consoleLog).not.toHaveBeenCalled()
    consoleError.mockRestore()
    consoleLog.mockRestore()
  })

  test('[P0] returns only version and token, discarding extra keys from the card', () => {
    const payload = cardPayload()
    const parsed = parseCardMessage([
      cardRecord(JSON.stringify({ ...payload, roll_no: 'CB.SC.U4CSE24001', injected: true })),
    ] as never)

    expect(Object.keys(parsed).sort()).toEqual(['token', 'version'])
  })
})
