import { describe, expect, test } from 'vitest'

/**
 * The SHARED, CONTINUOUS NFC scan session.
 *
 * The adapter lives at `web/src/features/nfc/webNfc.ts` and is imported by both
 * the card populator and the marking screen (FR65: the reader is shared, not
 * duplicated). `webNfc.test.ts` beside this file is the card populator's regression gate
 * for the write/read behaviour; this file covers the session added for marking.
 *
 * Marking is a continuous activity: a coordinator taps ~90 cards in a row and
 * must not press "start" between them. One `NDEFReader.scan()` started from a
 * user gesture, emitting every tap until aborted.
 */

type CardPayload = { version: string; token: string }

async function loadAdapterModule() {
  return await import('./webNfc')
}

/**
 * A fake NDEFReader that can deliver taps on demand.
 *
 * Modelled on the harness the CardPopulatorPortal test already uses, so
 * the two suites describe the same radio in the same terms.
 */
function installFakeReader(mediaType: string) {
  type ReadingListener = (event: { message: { records: unknown[] } }) => void
  const listeners: ReadingListener[] = []
  let scanCalls = 0
  let aborted = false

  class FakeNDEFReader {
    addEventListener(type: string, listener: ReadingListener) {
      if (type === 'reading') listeners.push(listener)
    }

    removeEventListener() {}

    async scan(options?: { signal?: AbortSignal }) {
      scanCalls += 1
      options?.signal?.addEventListener('abort', () => {
        aborted = true
      })
    }

    async write() {}
  }

  const windowRef = {
    isSecureContext: true,
    NDEFReader: FakeNDEFReader,
  } as unknown as Window

  return {
    windowRef,
    get scanCalls() {
      return scanCalls
    },
    get aborted() {
      return aborted
    },
    /** Simulates one physical tap carrying `payload`. */
    tap(payload: CardPayload) {
      const records = [{
        recordType: 'mime',
        mediaType,
        data: new DataView(new TextEncoder().encode(JSON.stringify(payload)).buffer),
      }]
      for (const listener of [...listeners]) listener({ message: { records } })
    },
    /** Simulates a tap on a blank or foreign tag. */
    tapBlank() {
      for (const listener of [...listeners]) listener({ message: { records: [] } })
    },
  }
}

describe('shared continuous NFC scan session', () => {
  test('[P0] the adapter lives at features/nfc and is imported, not duplicated', async () => {
    const nfc = await loadAdapterModule()

    expect(typeof nfc.createWebNfcAdapter).toBe('function')
    expect(nfc.CARD_MEDIA_TYPE).toBe('application/vnd.od-amrita.card+json')

    // FR65: the populator imports the MOVED module rather than keeping a copy.
    // A second NDEFReader wrapper is what this assertion exists to prevent.
    //
    // Asserted with import.meta.glob rather than a dynamic import: Vite resolves
    // imports at transform time, so importing a path that does not exist fails
    // the whole suite instead of returning null. A glob with no matches is an
    // empty object, which is exactly the "it is gone" signal wanted here.
    const strays = import.meta.glob('../cardPopulator/**/webNfc.ts')
    expect(
      Object.keys(strays),
      'features/cardPopulator/webNfc.ts should no longer exist — it moved to features/nfc/ (FR65)',
    ).toEqual([])
  })

  test('[P0] every tap in a session produces its own payload without re-arming', async () => {
    const nfc = await loadAdapterModule()
    const reader = installFakeReader(nfc.CARD_MEDIA_TYPE)
    const adapter = nfc.createWebNfcAdapter(reader.windowRef)

    const seen: CardPayload[] = []
    const controller = new AbortController()
    void adapter.scanSession((payload) => seen.push(payload), () => {}, controller.signal)
    await Promise.resolve()

    reader.tap({ version: 'v1', token: 'token-one' })
    reader.tap({ version: 'v1', token: 'token-two' })
    reader.tap({ version: 'v1', token: 'token-three' })

    expect(seen.map((p) => p.token)).toEqual(['token-one', 'token-two', 'token-three'])
    expect(
      reader.scanCalls,
      'scan() must be started ONCE from the user gesture, not re-armed per tap',
    ).toBe(1)

    controller.abort()
  })

  test('[P0] an identical token repeated inside the debounce window fires once', async () => {
    const nfc = await loadAdapterModule()
    const reader = installFakeReader(nfc.CARD_MEDIA_TYPE)
    const adapter = nfc.createWebNfcAdapter(reader.windowRef)

    const seen: CardPayload[] = []
    const controller = new AbortController()
    void adapter.scanSession((payload) => seen.push(payload), () => {}, controller.signal)
    await Promise.resolve()

    // One physical tap commonly raises several reading events.
    reader.tap({ version: 'v1', token: 'same-token' })
    reader.tap({ version: 'v1', token: 'same-token' })
    reader.tap({ version: 'v1', token: 'same-token' })

    expect(
      seen,
      'one physical tap must not become several marks; the server SCAN_DUPLICATE stays the correctness guarantee, this is UI hygiene',
    ).toHaveLength(1)

    // A DIFFERENT card during the same window is a real second student and must
    // always get through — debouncing by time alone would drop them.
    reader.tap({ version: 'v1', token: 'another-token' })
    expect(seen).toHaveLength(2)

    controller.abort()
  })

  test('[P0] a blank or foreign tag reports an error and the session stays alive', async () => {
    const nfc = await loadAdapterModule()
    const reader = installFakeReader(nfc.CARD_MEDIA_TYPE)
    const adapter = nfc.createWebNfcAdapter(reader.windowRef)

    const seen: CardPayload[] = []
    const errors: { kind: string; message: string }[] = []
    const controller = new AbortController()
    void adapter.scanSession((p) => seen.push(p), (e) => errors.push(e), controller.signal)
    await Promise.resolve()

    reader.tapBlank()

    expect(errors).toHaveLength(1)
    expect(errors[0].kind).toBe('card-payload')
    // The token is never echoed: a parse error quotes its source text, and that
    // text IS the token. The adapter drops causes for this reason.
    expect(errors[0].message).not.toContain('token')

    // The coordinator has 89 more students. A bad tag must not end the session.
    reader.tap({ version: 'v1', token: 'good-token' })
    expect(seen.map((p) => p.token)).toEqual(['good-token'])

    controller.abort()
  })

  test('[P0] aborting the signal ends the session so no radio outlives the screen', async () => {
    const nfc = await loadAdapterModule()
    const reader = installFakeReader(nfc.CARD_MEDIA_TYPE)
    const adapter = nfc.createWebNfcAdapter(reader.windowRef)

    const seen: CardPayload[] = []
    const controller = new AbortController()
    void adapter.scanSession((p) => seen.push(p), () => {}, controller.signal)
    await Promise.resolve()

    controller.abort()
    await Promise.resolve()

    expect(reader.aborted, 'the abort signal must reach NDEFReader.scan()').toBe(true)

    reader.tap({ version: 'v1', token: 'after-abort' })
    expect(
      seen,
      'a tap after abort must not be delivered — the session must not outlive unmount, sign-out or an event change',
    ).toHaveLength(0)
  })

  test('[P0] an insecure origin is reported before an unsupported browser', async () => {
    const nfc = await loadAdapterModule()
    const adapter = nfc.createWebNfcAdapter({ isSecureContext: false } as unknown as Window)

    // Ordering matters: on an insecure origin NDEFReader is never exposed, so
    // checking support first would report every plain-HTTP page as "unsupported"
    // and send the operator hunting for the wrong problem.
    expect(adapter.status()).toBe('insecure')
  })
})
