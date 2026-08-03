import type { Page } from '@playwright/test'

/**
 * Installs a fake `BarcodeDetector` and camera before the app boots.
 *
 * Headless Chromium has neither: on Android `BarcodeDetector` is backed by
 * Google Play services, and CI has no camera at all. A browser journey can
 * therefore only reach the camera path through a stub.
 *
 * Only the LENS is fake. A spec queues a code with `__decodeBarcode`, the app's
 * OWN decode loop picks it up on its next frame, and everything after that — the
 * search resolution, the mark, the record — is the real app against the real API.
 * The queue exists precisely so the app needs no test-only hook of its own.
 *
 * Nothing here may be read as evidence that a real camera decode works.
 */
export async function installFakeBarcodeScanner(page: Page) {
  await page.addInitScript(() => {
    // What the lens "sees" on the next frame. Drained on read, so one queued
    // code produces one decode rather than repeating every frame forever.
    const queue: string[] = []

    class FakeBarcodeDetector {
      static async getSupportedFormats() {
        return ['code_128', 'qr_code']
      }

      async detect() {
        const next = queue.shift()
        // An empty list is what a camera pointed at a wall actually reports.
        return next === undefined ? [] : [{ rawValue: next }]
      }
    }

    ;(window as unknown as { BarcodeDetector: unknown }).BarcodeDetector = FakeBarcodeDetector

    const track = { kind: 'video', stop() {} }
    const stream = { getTracks: () => [track] }
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: { getUserMedia: async () => stream },
    })

    /** Presents one barcode to the next decode frame. */
    ;(window as unknown as { __decodeBarcode: (text: string) => void }).__decodeBarcode = (text) => {
      queue.push(text)
    }
  })
}
