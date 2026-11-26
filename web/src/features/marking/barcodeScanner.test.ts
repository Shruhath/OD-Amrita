import { beforeEach, describe, expect, test, vi } from 'vitest'

const zxing = vi.hoisted(() => {
  const controls = { stop: vi.fn() }
  return {
    controls,
    decodeFromStream: vi.fn(async (_stream: MediaStream, _video: HTMLVideoElement, callback: (result?: { getText(): string }) => void) => {
      callback({ getText: () => 'CB.SC.U4CSE99002' })
      return controls
    }),
  }
})

vi.mock('@zxing/browser', () => ({
  BrowserMultiFormatReader: vi.fn(function BrowserMultiFormatReader() {
    return {
      decodeFromStream: zxing.decodeFromStream,
    }
  }),
}))

beforeEach(() => {
  zxing.decodeFromStream.mockClear()
  zxing.controls.stop.mockClear()
})

/**
 * The camera capture seam.
 *
 * WHAT THE CAMERA ACTUALLY READS. Not a sealed token. The card populator writes
 * the token to the NFC TAG only; nothing in this app ever prints one. What the camera
 * sees is the barcode already printed on the physical university ID card, which
 * carries the ROLL NUMBER. So this module decodes text, and the screen above it
 * resolves that text to a student_id through the search endpoint.
 *
 * WHY A SEAM AT ALL. CI Chromium and jsdom have neither a camera nor
 * `BarcodeDetector` — on Android it is backed by Google Play services, and on
 * desktop Chrome it is frequently absent. Nothing above this module may touch
 * `BarcodeDetector` or `navigator.mediaDevices`, or the marking screen becomes
 * untestable without hardware.
 *
 */

async function loadScannerModule() {
  return await import('./barcodeScanner')
}

/** Builds a window/navigator pair with a controllable BarcodeDetector + camera. */
function fakeEnvironment(options: {
  secure?: boolean
  hasDetector?: boolean
  hasCamera?: boolean
  formats?: string[]
  decoded?: string[]
} = {}) {
  const { secure = true, hasDetector = true, hasCamera = true, formats = ['code_128', 'qr_code'], decoded = [] } = options

  const stoppedTracks: string[] = []
  const track = {
    kind: 'video',
    stop: vi.fn(() => stoppedTracks.push('video')),
  }
  const stream = { getTracks: () => [track] }

  class FakeBarcodeDetector {
    static async getSupportedFormats() {
      return formats
    }

    async detect() {
      return decoded.map((rawValue) => ({ rawValue }))
    }
  }

  const windowRef = {
    isSecureContext: secure,
    document: { createElement: () => ({ play: async () => {}, pause: () => {} }) },
    ...(hasDetector ? { BarcodeDetector: FakeBarcodeDetector } : {}),
    requestAnimationFrame: (cb: FrameRequestCallback) => setTimeout(() => cb(0), 0) as unknown as number,
    cancelAnimationFrame: (id: number) => clearTimeout(id),
  } as unknown as Window

  const getUserMedia = vi.fn(async () => stream)
  const navigatorRef = (hasCamera ? { mediaDevices: { getUserMedia } } : {}) as unknown as Navigator

  return { windowRef, navigatorRef, getUserMedia, stoppedTracks, track }
}

describe('camera capture behind a fakeable seam', () => {
  test('[P0] an insecure origin is reported before an unsupported browser', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ secure: false, hasDetector: false })

    // Same ordering rule as the NFC adapter, for the same reason: on an insecure
    // origin the API is not exposed at all, so "unsupported" would send the
    // operator to fix the wrong thing.
    expect(await createBarcodeScanner(env.windowRef, env.navigatorRef).status()).toBe('insecure')
  })

  test('[P0] a browser without a camera API reports unsupported explicitly', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ hasDetector: false, hasCamera: false })

    expect(await createBarcodeScanner(env.windowRef, env.navigatorRef).status()).toBe('unsupported')
  })

  test('[P0] a laptop browser without native BarcodeDetector can still use the bundled decoder', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ hasDetector: false })
    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)

    expect(await scanner.status()).toBe('available')

    const seen: string[] = []
    const controller = new AbortController()
    void scanner.start((text) => seen.push(text), () => {}, controller.signal)

    await vi.waitFor(() => expect(zxing.decodeFromStream).toHaveBeenCalled())
    expect(seen).toEqual(['CB.SC.U4CSE99002'])
    controller.abort()
    await vi.waitFor(() => expect(zxing.controls.stop).toHaveBeenCalled())
  })

  test('[P0] getSupportedFormats() is consulted before choosing the native decoder', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ formats: [] })
    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)

    expect(await scanner.status()).toBe('available')

    const controller = new AbortController()
    void scanner.start(() => {}, () => {}, controller.signal)
    await vi.waitFor(() => expect(zxing.decodeFromStream).toHaveBeenCalled())
    controller.abort()
  })

  test('[P0] the camera is requested with the rear lens', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ decoded: ['CB.SC.U4CSE99001'] })
    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)

    const controller = new AbortController()
    void scanner.start(() => {}, () => {}, controller.signal)
    await vi.waitFor(() => expect(env.getUserMedia).toHaveBeenCalled())

    expect(env.getUserMedia).toHaveBeenCalledWith({ video: { facingMode: 'environment' } })
    controller.abort()
  })

  test('[P0] decoded text is emitted to the caller', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ decoded: ['CB.SC.U4CSE99001'] })
    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)

    const seen: string[] = []
    const controller = new AbortController()
    void scanner.start((text) => seen.push(text), () => {}, controller.signal)

    await vi.waitFor(() => expect(seen.length).toBeGreaterThan(0))
    expect(seen[0]).toBe('CB.SC.U4CSE99001')

    controller.abort()
  })

  test('[P0] every MediaStreamTrack is stopped when the mode closes', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ decoded: ['CB.SC.U4CSE99001'] })
    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)

    const controller = new AbortController()
    void scanner.start(() => {}, () => {}, controller.signal)
    await vi.waitFor(() => expect(env.getUserMedia).toHaveBeenCalled())

    controller.abort()

    // A camera light left on after the coordinator switches modes is both a
    // battery cost through a long event and an obvious trust problem.
    await vi.waitFor(() => expect(env.track.stop).toHaveBeenCalled())
    expect(env.stoppedTracks).toEqual(['video'])
  })

  test('[P0] a denied camera permission surfaces as a typed error, not a crash', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment()
    const denied = Object.assign(new Error('Permission denied'), { name: 'NotAllowedError' })
    ;(env.navigatorRef.mediaDevices.getUserMedia as ReturnType<typeof vi.fn>).mockRejectedValue(denied)

    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)
    const errors: { kind: string; message: string }[] = []
    const controller = new AbortController()
    await scanner.start(() => {}, (e) => errors.push(e), controller.signal)

    expect(errors).toHaveLength(1)
    expect(errors[0].kind).toBe('permission-denied')
    expect(errors[0].message).toMatch(/camera/i)
  })

  test('[P0] the decode loop never overlaps itself', async () => {
    const { createBarcodeScanner } = await loadScannerModule()
    const env = fakeEnvironment({ decoded: ['CB.SC.U4CSE99001'] })

    let inFlight = 0
    let maxInFlight = 0
    const DetectorClass = (env.windowRef as unknown as { BarcodeDetector: { prototype: { detect: () => Promise<unknown> } } }).BarcodeDetector
    const original = DetectorClass.prototype.detect
    DetectorClass.prototype.detect = async function patched() {
      inFlight += 1
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise((resolve) => setTimeout(resolve, 5))
      const result = await original.call(this)
      inFlight -= 1
      return result
    }

    const scanner = createBarcodeScanner(env.windowRef, env.navigatorRef)
    const controller = new AbortController()
    void scanner.start(() => {}, () => {}, controller.signal)
    await new Promise((resolve) => setTimeout(resolve, 40))
    controller.abort()

    // requestAnimationFrame fires far faster than a decode completes. Without an
    // in-flight guard the frames stack up and the phone stalls.
    expect(maxInFlight).toBe(1)
  })
})
