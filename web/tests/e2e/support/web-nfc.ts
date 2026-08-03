import type { Page } from '@playwright/test'

/**
 * Installs a fake `NDEFReader` before the app boots.
 *
 * Headless Chromium has no Web NFC and no tag reader, so a browser journey can
 * only reach the write/verify path through a stub. The card payload is still
 * sealed by the real API and resolved by the real API — only the radio is fake.
 *
 * `readPayload` is what a simulated tap presents. Leave it undefined to echo
 * back whatever was last written, which is what a correctly-written card does.
 */
export async function installFakeWebNfc(
  page: Page,
  options: { readPayload?: { version: string; token: string } | null } = {},
) {
  await page.addInitScript((injected: { readPayload?: { version: string; token: string } | null }) => {
    const mediaType = 'application/vnd.od-amrita.card+json'
    const state: { lastWritten: string | null } = { lastWritten: null }
    // Exposed so a spec can assert on exactly what reached the radio.
    ;(window as unknown as { __nfcWrites: string[] }).__nfcWrites = []

    type ReadingListener = (event: { message: { records: unknown[] } }) => void

    // Every live listener across every reader instance. The marking screen runs
    // ONE continuous session and expects taps to arrive on demand rather than
    // once per scan() call, so a spec drives it through __tapCard below.
    const liveListeners: ReadingListener[] = []

    const cardRecords = (payload: { version: string; token: string } | null) =>
      payload === null
        ? []
        : [
            {
              recordType: 'mime',
              mediaType,
              data: new DataView(new TextEncoder().encode(JSON.stringify(payload)).buffer),
            },
          ]

    /** Simulates one physical tap presenting `payload`. */
    ;(window as unknown as { __tapCard: (p: { version: string; token: string } | null) => void }).__tapCard = (
      payload,
    ) => {
      for (const listener of [...liveListeners]) listener({ message: { records: cardRecords(payload) } })
    }

    class FakeNDEFReader {
      private readers: ReadingListener[] = []

      async write(message: { records: { data?: BufferSource; mediaType?: string }[] }) {
        const record = message.records[0]
        const text = new TextDecoder().decode(record.data as ArrayBufferView)
        state.lastWritten = text
        ;(window as unknown as { __nfcWrites: string[] }).__nfcWrites.push(text)
      }

      async scan() {
        queueMicrotask(() => {
          const body = injected.readPayload ? JSON.stringify(injected.readPayload) : state.lastWritten
          const records =
            injected.readPayload === null || body === null
              ? []
              : [
                  {
                    recordType: 'mime',
                    mediaType,
                    data: new DataView(new TextEncoder().encode(body).buffer),
                  },
                ]
          for (const listener of [...this.readers]) listener({ message: { records } })
        })
      }

      addEventListener(type: string, listener: ReadingListener) {
        if (type !== 'reading') return
        this.readers.push(listener)
        liveListeners.push(listener)
      }

      removeEventListener(_type: string, listener?: ReadingListener) {
        if (!listener) return
        for (const list of [this.readers, liveListeners]) {
          const at = list.indexOf(listener)
          if (at >= 0) list.splice(at, 1)
        }
      }
    }

    ;(window as unknown as { NDEFReader: unknown }).NDEFReader = FakeNDEFReader
  }, options)
}

/** Removes Web NFC so a spec can assert the explicit unsupported state. */
export async function removeWebNfc(page: Page) {
  await page.addInitScript(() => {
    delete (window as Window & { NDEFReader?: unknown }).NDEFReader
  })
}

/** Everything the fake radio was asked to write, in order. */
export async function nfcWrites(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __nfcWrites?: string[] }).__nfcWrites ?? [])
}
